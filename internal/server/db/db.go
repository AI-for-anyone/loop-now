// Package db is the server's SQLite access layer: data.db (business tables) and events.db (prunable log tables,
// attached as ev on every connection) in the data directory, one read-only connection pool and one write pool that
// holds a single connection.
//
// The call shape matches the former pgx one (Exec, Query, QueryRow, transaction callbacks); this package converts
// arguments and scanned values between Go types and SQLite storage: times are UTC microsecond integers, UUIDs are
// lowercase text, arrays and JSON are JSON text, booleans are 0/1.
//
// All writes go through the write connection: transactions start with BEGIN IMMEDIATE, and the single connection
// serializes all writes by construction, so no row locks or advisory locks are needed.
// There is no consistency between the two files: a transaction commits data.db and events.db separately, and a
// reader takes its snapshots of the two files one after the other, so events.db only holds logs where losing a few
// rows or seeing them late does no harm, and business decisions never read it.
// Acquiring the write connection is bounded by the caller's ctx; once acquired, the commit cannot be cancelled, so
// the caller only ever sees "committed" or "not committed".
// Every write is a transaction (a single Exec too), and the global write hook (SetWriteHook) runs before commit.
package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
)

// File names in the data directory.
const (
	DataFile   = "data.db"
	EventsFile = "events.db"
	// EventsSchema is the name events.db is attached as on every connection; SQL writes ev.<table>.
	EventsSchema = "ev"
)

// Options controls how the database is opened; zero-valued fields take their defaults.
type Options struct {
	// Dir is the directory holding data.db and events.db; it must already exist.
	Dir string
	// ReadConns is the size limit of the read-only pool, default 4.
	ReadConns int
	// Now is the clock, default time.Now; tests may inject one. A transaction's Now() is the transaction start time.
	Now func() time.Time
	// SlowTx is how long a write transaction may hold the write connection before a warning is logged, default
	// 200ms; negative disables it.
	SlowTx time.Duration
	// WriterWaitWarn is how long a caller may wait for the write connection before a warning is logged (a hint of a
	// possible nested write or long transaction), default 10s; negative disables it.
	WriterWaitWarn time.Duration
	// Logger discards by default.
	Logger *slog.Logger
}

// DB holds the two connection pools. It is safe for concurrent use.
type DB struct {
	w, r   *sql.DB
	dir    string
	now    func() time.Time
	slowTx time.Duration
	wait   time.Duration
	log    *slog.Logger
	// holder records the call site currently holding the write connection, for the wait warning and nested-write
	// detection.
	holder atomic.Pointer[writerHolder]
	// writeHook is the global hook run before every write transaction commits (see SetWriteHook).
	writeHook atomic.Pointer[WriteHook]
	// writeTxNow is the start time (UTC microseconds) of the write transaction in progress; SQL now() on the write
	// connection returns it. It is 0 when no write transaction is open.
	writeTxNow atomic.Int64
	// queryHook is called before every statement runs (see SetQueryHook).
	queryHook atomic.Pointer[QueryHook]
	// waiters is the number of calls currently waiting for the write connection (see WriteWaiters).
	waiters atomic.Int64
}

// WriteWaiters returns the number of calls currently waiting for the write connection. Tests use it to confirm a
// write is blocked waiting for the write connection (formerly done by looking at lock waits in pg_stat_activity).
func (d *DB) WriteWaiters() int64 { return d.waiters.Load() }

// QueryHook is called before every statement runs; for statements inside a transaction it is called inside the
// transaction. Tests use it to pause before a given statement and check behavior under concurrency (formerly done
// with pgx's QueryTracer).
type QueryHook func(ctx context.Context, query string)

// SetQueryHook sets the statement hook; nil removes it. It applies to transactions and statements started
// afterwards.
func (d *DB) SetQueryHook(h QueryHook) {
	if h == nil {
		d.queryHook.Store(nil)
		return
	}
	d.queryHook.Store(&h)
}

type writerHolder struct {
	caller string
	goid   int64
}

// changeOrder is the counter behind the SQL function change_order(), shared by all databases in the process.
var changeOrder atomic.Int64

// DetectNestedWrites, when on, makes a goroutine that already holds the write connection and asks for it again get
// ErrNestedWrite immediately instead of waiting until it times out. It has to parse goroutine stacks, so it is only
// turned on in tests (sqlitetest turns it on).
var DetectNestedWrites atomic.Bool

// ErrNestedWrite means a write was started through *DB inside a write transaction callback: there is only one write
// connection, so the call would wait on itself.
var ErrNestedWrite = errors.New("db: 写事务内又经 *DB 发起写入（应使用事务对象 tx）")

// busyTimeoutMS is the maximum wait when another process (an admin subcommand, a backup tool) holds the lock.
// Writes inside the process are already serialized by the single write connection, so it is normally never hit.
const busyTimeoutMS = 10000

// connPragmas are set on every connection. busy_timeout must be set first; the driver puts it at the front.
var connPragmas = []string{
	fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS),
	"foreign_keys(1)",
	"temp_store(MEMORY)",
}

// schemaPragmas are set on each database (main and ev) separately.
var schemaPragmas = []string{
	"synchronous=NORMAL",
	"journal_size_limit=200000000",
}

const mainCacheKiB, eventsCacheKiB = 32000, 8000

// Open opens (creating them if needed) the two database files in the data directory. It opens the write pool first
// (creating the files and setting WAL), then the read-only pool.
// Open does not take the instance lock; processes that need the data directory to themselves (the server, the
// importer) call LockDir separately.
func Open(ctx context.Context, o Options) (*DB, error) {
	if o.Dir == "" {
		return nil, errors.New("db: 未指定数据目录")
	}
	if st, err := os.Stat(o.Dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("db: 数据目录 %s 不存在或不是目录", o.Dir)
	}
	if o.ReadConns <= 0 {
		o.ReadConns = 4
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SlowTx == 0 {
		o.SlowTx = 200 * time.Millisecond
	}
	if o.WriterWaitWarn == 0 {
		o.WriterWaitWarn = 10 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	d := &DB{dir: o.Dir, now: o.Now, slowTx: o.SlowTx, wait: o.WriterWaitWarn, log: o.Logger}
	data, events := filepath.Join(o.Dir, DataFile), filepath.Join(o.Dir, EventsFile)

	// now() and clock_timestamp() in SQL are provided by this package (UTC microseconds, from the injected clock):
	// now() on the write connection is the start time of the current write transaction, as in PostgreSQL;
	// clock_timestamp() and now() on read-only connections return the time of the call.
	clock := func() int64 { return d.now().UnixMicro() }
	writerNow := func() int64 {
		if v := d.writeTxNow.Load(); v != 0 {
			return v
		}
		return clock()
	}
	w := sql.OpenDB(connector(data, events, "immediate", writerNow, clock, func(c sqlite.ExecQuerierContext) error {
		for _, schema := range []string{"main", EventsSchema} {
			if err := pragma(c, schema+".journal_mode=WAL"); err != nil {
				return err
			}
		}
		return nil
	}))
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	w.SetConnMaxIdleTime(0)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("db: 打开写连接失败：%w", err)
	}

	r := sql.OpenDB(connector(data, events, "deferred", clock, clock, func(c sqlite.ExecQuerierContext) error {
		return pragma(c, "query_only=1")
	}))
	r.SetMaxOpenConns(o.ReadConns)
	r.SetMaxIdleConns(o.ReadConns)
	r.SetConnMaxIdleTime(5 * time.Minute)
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("db: 打开只读连接失败：%w", err)
	}
	d.w, d.r = w, r
	return d, nil
}

// connector returns a driver instance that serves only this database: the attach and PRAGMAs live in its own
// connection hook, so other SQLite connections in the process are not affected.
// On every new connection it attaches events.db, sets each database's PRAGMAs, and finally runs hook (WAL for the
// write pool, read-only for the read pool).
func connector(data, events, txlock string, now, clock func() int64, hook func(sqlite.ExecQuerierContext) error) driver.Connector {
	q := url.Values{}
	for _, p := range connPragmas {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", txlock)
	dsn := "file:" + filepath.ToSlash(data) + "?" + q.Encode()
	drv := &sqlite.Driver{}
	for name, f := range map[string]func() int64{"now": now, "clock_timestamp": clock} {
		if err := drv.RegisterFunction(name, &sqlite.FunctionImpl{NArgs: 0, Scalar: func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			return f(), nil
		}}); err != nil {
			panic(err) // can only be a wrong name or argument count
		}
	}
	// change_order() returns a sequence number that increases monotonically within the process: the capture triggers
	// record it in the change tables of both databases, and readers use it to restore the write order.
	if err := drv.RegisterFunction("change_order", &sqlite.FunctionImpl{NArgs: 0, Scalar: func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return changeOrder.Add(1), nil
	}}); err != nil {
		panic(err)
	}
	// iso_time(microseconds) turns a time column into RFC 3339 (UTC, keeping microseconds) for JSON built in SQL:
	// when Go decodes JSON, time.Time only accepts that kind of string. NULL in, NULL out.
	if err := drv.RegisterFunction("iso_time", &sqlite.FunctionImpl{NArgs: 1, Deterministic: true, Scalar: func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case nil:
			return nil, nil
		case int64:
			return time.UnixMicro(v).UTC().Format(time.RFC3339Nano), nil
		}
		return nil, fmt.Errorf("iso_time 需要整数微秒，得到 %T", args[0])
	}}); err != nil {
		panic(err)
	}
	drv.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, _ string) error {
		if err := attachEvents(c, events); err != nil {
			return err
		}
		for _, schema := range []string{"main", EventsSchema} {
			for _, p := range schemaPragmas {
				if err := pragma(c, schema+"."+p); err != nil {
					return err
				}
			}
		}
		if err := pragma(c, fmt.Sprintf("main.cache_size=-%d", mainCacheKiB)); err != nil {
			return err
		}
		if err := pragma(c, fmt.Sprintf("%s.cache_size=-%d", EventsSchema, eventsCacheKiB)); err != nil {
			return err
		}
		return hook(c)
	})
	return &sqliteConnector{dsn: dsn, drv: drv}
}

type sqliteConnector struct {
	dsn string
	drv *sqlite.Driver
}

func (c *sqliteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.drv.Open(c.dsn)
}

func (c *sqliteConnector) Driver() driver.Driver { return c.drv }

func attachEvents(c sqlite.ExecQuerierContext, file string) error {
	_, err := c.ExecContext(context.Background(), "ATTACH DATABASE ? AS "+EventsSchema, []driver.NamedValue{{Ordinal: 1, Value: file}})
	if err != nil {
		return fmt.Errorf("挂载 %s 失败：%w", EventsFile, err)
	}
	return nil
}

func pragma(c sqlite.ExecQuerierContext, p string) error {
	if _, err := c.ExecContext(context.Background(), "PRAGMA "+p, nil); err != nil {
		return fmt.Errorf("PRAGMA %s 失败：%w", p, err)
	}
	return nil
}

// Close closes both connection pools.
func (d *DB) Close() error {
	return errors.Join(d.r.Close(), d.w.Close())
}

// Dir returns the data directory.
func (d *DB) Dir() string { return d.dir }

// Now returns the current time (from the injected clock).
func (d *DB) Now() time.Time { return d.now() }

// Writer returns the write pool, only for migrations and maintenance; business code goes through Exec and InTx.
func (d *DB) Writer() *sql.DB { return d.w }

// Reader returns the read-only pool, only for maintenance and diagnostics; business code goes through Query,
// QueryRow and ReadTx.
func (d *DB) Reader() *sql.DB { return d.r }

// Ping checks that both connection pools are usable.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.r.PingContext(ctx); err != nil {
		return err
	}
	c, release, err := d.acquireWriter(ctx)
	if err != nil {
		return err
	}
	defer release()
	return c.PingContext(ctx)
}

// Result is the result of a write statement, shaped like pgconn.CommandTag.
type Result struct{ rows int64 }

// RowsAffected returns the number of affected rows.
func (r Result) RowsAffected() int64 { return r.rows }

// Exec runs one statement on the write connection. It forms its own write transaction (BEGIN IMMEDIATE, hook,
// commit), with the same guarantees as InTx.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	var res Result
	err := d.InTx(ctx, func(tx *Tx) error {
		r, err := tx.Exec(ctx, query, args...)
		res = r
		return err
	})
	return res, err
}

// Query runs a query. Read-only statements go to the read-only pool; statements containing INSERT/UPDATE/DELETE
// (such as ... RETURNING) form their own write transaction on the write connection, which stays held until Rows is
// closed. Closing runs the hook and commits, and a commit error is returned by Rows.Err (or Row.Scan).
func (d *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	if !isWrite(query) {
		vals, err := convertArgs(args)
		if err != nil {
			return nil, err
		}
		if h := d.queryHook.Load(); h != nil {
			(*h)(ctx, query)
		}
		rows, err := d.r.QueryContext(ctx, query, vals...)
		if err != nil {
			return nil, err
		}
		return &Rows{rows: rows}, nil
	}
	t, end, err := d.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := t.Query(ctx, query, args...)
	if err != nil {
		return nil, end(err)
	}
	rows.finish = end
	return rows, nil
}

// QueryRow runs a query that reads a single row; it is routed like Query.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := d.Query(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// acquireWriter acquires the single write connection; the wait is bounded by ctx. release returns the connection.
func (d *DB) acquireWriter(ctx context.Context) (*sql.Conn, func(), error) {
	var gid int64
	if DetectNestedWrites.Load() {
		gid = goid()
		if h := d.holder.Load(); h != nil && h.goid == gid {
			return nil, nil, fmt.Errorf("%w（持有者：%s）", ErrNestedWrite, h.caller)
		}
	}
	var warn *time.Timer
	if d.wait > 0 {
		warn = time.AfterFunc(d.wait, func() {
			caller := ""
			if h := d.holder.Load(); h != nil {
				caller = h.caller
			}
			d.log.Warn("等待写连接超时未得", "waited", d.wait, "holder", caller, "waiter", callerOutsidePackage())
		})
	}
	d.waiters.Add(1)
	c, err := d.w.Conn(ctx)
	d.waiters.Add(-1)
	if warn != nil {
		warn.Stop()
	}
	if err != nil {
		return nil, nil, err
	}
	d.holder.Store(&writerHolder{caller: callerOutsidePackage(), goid: gid})
	start := time.Now()
	return c, func() {
		h := d.holder.Swap(nil)
		if held := time.Since(start); d.slowTx > 0 && held > d.slowTx && h != nil {
			d.log.Warn("写连接持有过久", "held", held, "caller", h.caller)
		}
		c.Close()
	}, nil
}

// callerOutsidePackage returns the nearest call site outside this package, for logging.
func callerOutsidePackage() string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if !isThisPackage(f.Function) {
			return fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		if !more {
			return ""
		}
	}
}
