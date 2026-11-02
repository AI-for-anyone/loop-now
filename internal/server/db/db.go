// Package db 是服务端对 SQLite 的访问层：数据目录下的 data.db（业务表）与 events.db（可清理的日志表，
// 每个连接以 ev 挂载），一个只读连接池与一个只有一个连接的写连接池。
//
// 调用形状与原来的 pgx 保持一致（Exec、Query、QueryRow、事务回调），参数与扫描由本包在 Go 类型与
// SQLite 存储之间转换：时间是 UTC 微秒整数，UUID 是小写文本，数组与 JSON 是 JSON 文本，布尔是 0/1。
//
// 写入一律经写连接：事务以 BEGIN IMMEDIATE 开始，单连接使所有写天然串行，因此不需要行锁与咨询锁。
// 两个文件之间没有一致性：一个事务对 data.db 与 events.db 分别提交，读者对两个文件的快照也是先后各取，
// 所以 events.db 只放丢几条、晚几条都无妨的日志，业务判断不读它。
// 取得写连接受调用方 ctx 约束；取得之后提交不可取消，调用方看到的结果只有“已提交”或“未提交”。
// 每个写入都是一个事务（单条 Exec 也是），提交前执行全局写钩子（SetWriteHook）。
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

// 数据目录下的文件名。
const (
	DataFile   = "data.db"
	EventsFile = "events.db"
	// EventsSchema 是 events.db 在每个连接上的挂载名，SQL 里写 ev.<表名>。
	EventsSchema = "ev"
)

// Options 控制打开方式；零值字段取默认。
type Options struct {
	// Dir 是存放 data.db 与 events.db 的目录，必须已存在。
	Dir string
	// ReadConns 是只读连接池的上限，默认 4。
	ReadConns int
	// Now 是时钟，默认 time.Now；测试可注入。事务的 Now() 取事务开始时刻。
	Now func() time.Time
	// SlowTx 是写事务持有写连接超过多久记警告，默认 200ms；负数关闭。
	SlowTx time.Duration
	// WriterWaitWarn 是等待写连接超过多久记警告（提示可能的嵌套写或长事务），默认 10s；负数关闭。
	WriterWaitWarn time.Duration
	// Logger 默认丢弃。
	Logger *slog.Logger
}

// DB 持有两个连接池。可并发使用。
type DB struct {
	w, r   *sql.DB
	dir    string
	now    func() time.Time
	slowTx time.Duration
	wait   time.Duration
	log    *slog.Logger
	// holder 记录当前持有写连接的调用点，供等待告警与嵌套写检测使用。
	holder atomic.Pointer[writerHolder]
	// writeHook 是每个写事务提交前执行的全局钩子（见 SetWriteHook）。
	writeHook atomic.Pointer[WriteHook]
	// writeTxNow 是进行中写事务的开始时刻（UTC 微秒），写连接上的 SQL now() 返回它；没有写事务时为 0。
	writeTxNow atomic.Int64
}

type writerHolder struct {
	caller string
	goid   int64
}

// DetectNestedWrites 打开后，同一 goroutine 在持有写连接时再次请求写连接会立即返回 ErrNestedWrite，
// 而不是等到超时。需要解析 goroutine 栈，只在测试中打开（sqlitetest 会打开）。
var DetectNestedWrites atomic.Bool

// ErrNestedWrite 表示在写事务回调里又经 *DB 发起了写：写连接只有一个，这样会自己等自己。
var ErrNestedWrite = errors.New("db: 写事务内又经 *DB 发起写入（应使用事务对象 tx）")

// busyTimeoutMS 是遇到其他进程（管理子命令、备份工具）持锁时的等待上限。进程内的写已由单个写连接串行，
// 平时碰不到它。
const busyTimeoutMS = 10000

// 每个连接都设置的 PRAGMA。busy_timeout 必须最先设置，驱动会把它排到最前。
var connPragmas = []string{
	fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS),
	"foreign_keys(1)",
	"temp_store(MEMORY)",
}

// 每个库（main 与 ev）各自的 PRAGMA。
var schemaPragmas = []string{
	"synchronous=NORMAL",
	"journal_size_limit=200000000",
}

const mainCacheKiB, eventsCacheKiB = 32000, 8000

// Open 打开（必要时创建）数据目录下的两个库文件。先打开写连接池（建文件、设 WAL），再打开只读池。
// Open 不取实例锁；需要独占数据目录的进程（服务、导入）另外调用 LockDir。
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

	// SQL 里的 now() 与 clock_timestamp() 由本包提供（UTC 微秒，经注入的时钟）：写连接上的 now() 是当前写事务的
	// 开始时刻，与 PostgreSQL 一致；clock_timestamp() 与只读连接上的 now() 取调用时刻。
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

// connector 返回一个只服务本库的驱动实例：挂载与 PRAGMA 写在它自己的连接钩子里，不影响进程里其他 SQLite 连接。
// 每个新连接：挂载 events.db、设置两个库各自的 PRAGMA，最后执行 hook（写池设 WAL，读池设只读）。
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
			panic(err) // 只会是名字或参数个数写错
		}
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

// Close 关闭两个连接池。
func (d *DB) Close() error {
	return errors.Join(d.r.Close(), d.w.Close())
}

// Dir 返回数据目录。
func (d *DB) Dir() string { return d.dir }

// Now 返回当前时刻（经注入的时钟）。
func (d *DB) Now() time.Time { return d.now() }

// Writer 返回写连接池，只给迁移与维护使用；业务代码经 Exec、InTx。
func (d *DB) Writer() *sql.DB { return d.w }

// Reader 返回只读连接池，只给维护与诊断使用；业务代码经 Query、QueryRow、ReadTx。
func (d *DB) Reader() *sql.DB { return d.r }

// Ping 确认两个连接池可用。
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

// Result 是写语句的结果，形状同 pgconn.CommandTag。
type Result struct{ rows int64 }

// RowsAffected 返回受影响的行数。
func (r Result) RowsAffected() int64 { return r.rows }

// Exec 在写连接上执行一条语句。它自成一个写事务（BEGIN IMMEDIATE、钩子、提交），与 InTx 的保证相同。
func (d *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	var res Result
	err := d.InTx(ctx, func(tx *Tx) error {
		r, err := tx.Exec(ctx, query, args...)
		res = r
		return err
	})
	return res, err
}

// Query 执行查询。只读语句走只读池；带 INSERT/UPDATE/DELETE 的语句（如 … RETURNING）在写连接上自成一个写事务，
// 写连接一直占用到 Rows 关闭，关闭时执行钩子并提交，提交的错误由 Rows.Err（或 Row.Scan）返回。
func (d *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	if !isWrite(query) {
		vals, err := convertArgs(args)
		if err != nil {
			return nil, err
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

// QueryRow 执行只取一行的查询；路由同 Query。
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := d.Query(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

// acquireWriter 取得唯一的写连接；等待受 ctx 约束。release 归还连接。
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
	c, err := d.w.Conn(ctx)
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

// callerOutsidePackage 返回本包之外最近的调用点，用于日志。
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
