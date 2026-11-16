package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
)

func openTest(t testing.TB) *DB {
	t.Helper()
	return openTestDir(t, t.TempDir())
}

func openTestDir(t testing.TB, dir string) *DB {
	t.Helper()
	d, err := Open(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func mustExec(t testing.TB, q Querier, query string, args ...any) {
	t.Helper()
	if _, err := q.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func pragmaValue(t *testing.T, c *sql.Conn, p string) string {
	t.Helper()
	var v string
	if err := c.QueryRowContext(context.Background(), "PRAGMA "+p).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", p, err)
	}
	return v
}

func TestOpenPragmas(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	want := map[string]string{
		"busy_timeout": "10000", "foreign_keys": "1", "temp_store": "2",
		"main.journal_mode": "wal", "ev.journal_mode": "wal",
		"main.synchronous": "1", "ev.synchronous": "1",
		"main.journal_size_limit": "200000000", "ev.journal_size_limit": "200000000",
		"main.cache_size": "-32000", "ev.cache_size": "-8000",
	}
	for name, pool := range map[string]*sql.DB{"writer": d.Writer(), "reader": d.Reader()} {
		c, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for p, v := range want {
			if got := pragmaValue(t, c, p); got != v {
				t.Errorf("%s: PRAGMA %s = %s, want %s", name, p, got, v)
			}
		}
		wantRO := map[string]string{"writer": "0", "reader": "1"}[name]
		if got := pragmaValue(t, c, "query_only"); got != wantRO {
			t.Errorf("%s: query_only = %s, want %s", name, got, wantRO)
		}
		c.Close()
	}
	for _, f := range []string{DataFile, EventsFile} {
		if _, err := os.Stat(filepath.Join(d.Dir(), f)); err != nil {
			t.Errorf("%s was not created: %v", f, err)
		}
	}
}

func TestIsWrite(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                                    false,
		"  -- c\n /* x */ select * from t":            false,
		"VALUES (1)":                                  false,
		"WITH x AS (SELECT 1) SELECT * FROM x":        false,
		"WITH x AS (SELECT 'update') SELECT * FROM x": false,
		"WITH due AS (SELECT id FROM t) UPDATE t SET a=1 WHERE id IN (SELECT id FROM due) RETURNING id": true,
		"INSERT INTO t VALUES (1) RETURNING id":                                                         true,
		"update t set a = 1":                                                                            true,
		"DELETE FROM t":                                                                                 true,
		"PRAGMA optimize":                                                                               true,
		"SELECT updated_at FROM t":                                                                      false,
	}
	for q, want := range cases {
		if got := isWrite(q); got != want {
			t.Errorf("isWrite(%q) = %v, want %v", q, got, want)
		}
	}
}

func TestRoutingAndReadOnlyPool(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT NOT NULL) STRICT`)
	var id int64
	if err := d.QueryRow(ctx, `INSERT INTO t (v) VALUES (?) RETURNING id`, "a").Scan(&id); err != nil || id != 1 {
		t.Fatalf("INSERT … RETURNING via QueryRow: id=%d err=%v", id, err)
	}
	// The write connection has been returned: the next write transaction does not wait.
	ctxT, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := d.InTx(ctxT, func(tx *Tx) error { _, err := tx.Exec(ctx, `INSERT INTO t (v) VALUES ('b')`); return err }); err != nil {
		t.Fatalf("InTx after RETURNING: %v", err)
	}
	if _, err := d.Reader().ExecContext(ctx, `INSERT INTO t (v) VALUES ('c')`); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("a write on the read-only pool should be rejected, got %v", err)
	}
	var n int
	if err := d.QueryRow(ctx, `WITH x AS (SELECT id FROM t WHERE v='a') UPDATE t SET v='z' WHERE id IN (SELECT id FROM x) RETURNING id`).Scan(&n); err != nil {
		t.Fatalf("WITH … UPDATE should use the write connection: %v", err)
	}
}

type status string

type payload struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func TestArgsAndScan(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE x (
		at INTEGER NOT NULL, at_null INTEGER, id TEXT NOT NULL, id_null TEXT, names TEXT, ids TEXT,
		raw TEXT, doc TEXT, m TEXT, ok INTEGER NOT NULL, st TEXT NOT NULL, b BLOB, n INTEGER) STRICT`)
	loc := time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 11, 2, 21, 30, 15, 123456789, loc)
	id := uuid.New()
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	var nilTime *time.Time
	var nilID *uuid.UUID
	mustExec(t, d, `INSERT INTO x VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		at, nilTime, id, nilID, []string{"a", "b"}, ids, json.RawMessage(`{"k":1}`),
		payload{Name: "p", Tags: []string{"t"}}, map[string]int{"x": 2}, true, status("ready"), []byte{0, 1, 2}, uint16(7))

	var (
		gotAt    time.Time
		gotNull  *time.Time
		gotID    uuid.UUID
		gotIDN   *uuid.UUID
		names    []string
		gotIDs   []uuid.UUID
		raw      json.RawMessage
		doc      payload
		m        map[string]int
		ok       bool
		st       status
		b        []byte
		n        int
		atAgain  *time.Time
		rawFromN *json.RawMessage
	)
	err := d.QueryRow(ctx, `SELECT at, at_null, id, id_null, names, ids, raw, doc, m, ok, st, b, n, at, NULL FROM x`).
		Scan(&gotAt, &gotNull, &gotID, &gotIDN, &names, &gotIDs, &raw, &doc, &m, &ok, &st, &b, &n, &atAgain, &rawFromN)
	if err != nil {
		t.Fatal(err)
	}
	if !gotAt.Equal(at.Truncate(time.Microsecond)) || gotAt.Location() != time.UTC {
		t.Errorf("time: got %v want %v (UTC)", gotAt, at.Truncate(time.Microsecond))
	}
	if gotNull != nil || gotIDN != nil || rawFromN != nil {
		t.Errorf("NULL should scan as nil: %v %v %v", gotNull, gotIDN, rawFromN)
	}
	if atAgain == nil || !atAgain.Equal(gotAt) {
		t.Errorf("**time.Time: %v", atAgain)
	}
	if gotID != id || len(gotIDs) != 2 || gotIDs[1] != ids[1] {
		t.Errorf("uuid: %v %v", gotID, gotIDs)
	}
	if strings.Join(names, ",") != "a,b" || string(raw) != `{"k":1}` || doc.Name != "p" || doc.Tags[0] != "t" || m["x"] != 2 {
		t.Errorf("json: %v %s %+v %v", names, raw, doc, m)
	}
	if !ok || st != "ready" || len(b) != 3 || b[2] != 2 || n != 7 {
		t.Errorf("scalars: %v %v %v %v", ok, st, b, n)
	}
	var stored string
	if err := d.QueryRow(ctx, `SELECT id || '|' || typeof(at) || '|' || names FROM x`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != id.String()+"|integer|[\"a\",\"b\"]" {
		t.Errorf("stored form: %s", stored)
	}
	if _, err := d.Exec(ctx, `SELECT ?`, time.Second); !errors.Is(err, errDuration) {
		t.Errorf("a Duration argument should be rejected, got %v", err)
	}
	var nilSlice []string
	var isNull bool
	if err := d.QueryRow(ctx, `SELECT ? IS NULL`, nilSlice).Scan(&isNull); err != nil || !isNull {
		t.Errorf("a nil slice should be NULL: %v %v", isNull, err)
	}
}

func TestCollectHelpers(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	rows, err := d.Query(ctx, `SELECT value FROM json_each(?)`, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := CollectRows(rows, RowTo[string])
	if err != nil || strings.Join(got, "") != "abc" {
		t.Fatalf("CollectRows: %v %v", got, err)
	}
	rows, err = d.Query(ctx, `SELECT value FROM json_each('[1,2,3]')`)
	if err != nil {
		t.Fatal(err)
	}
	var v, sum int
	if res, err := ForEachRow(rows, []any{&v}, func() error { sum += v; return nil }); err != nil || sum != 6 || res.RowsAffected() != 3 {
		t.Fatalf("ForEachRow: %d %v", sum, err)
	}
	if err := d.QueryRow(ctx, `SELECT 1 WHERE 0`).Scan(&v); !IsNoRows(err) {
		t.Fatalf("no rows should give ErrNoRows: %v", err)
	}
}

func TestInTxHooks(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE log (s TEXT NOT NULL) STRICT`)
	var after []string
	err := d.InTx(ctx, func(tx *Tx) error {
		mustExec(t, tx, `INSERT INTO log VALUES ('body')`)
		tx.BeforeCommit(func(ctx context.Context, tx *Tx) error {
			mustExec(t, tx, `INSERT INTO log VALUES ('before1')`)
			tx.BeforeCommit(func(ctx context.Context, tx *Tx) error {
				mustExec(t, tx, `INSERT INTO log VALUES ('before2')`)
				return nil
			})
			return nil
		})
		tx.AfterCommit(func() { after = append(after, "after") })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := d.QueryRow(ctx, `SELECT group_concat(s, ',') FROM log`).Scan(&s); err != nil || s != "body,before1,before2" || len(after) != 1 {
		t.Fatalf("hook order: %q %v %v", s, after, err)
	}

	boom := errors.New("boom")
	for name, fn := range map[string]func(*Tx) error{
		"fn error": func(tx *Tx) error {
			mustExec(t, tx, `INSERT INTO log VALUES ('x')`)
			tx.AfterCommit(func() { t.Error("AfterCommit should not run after a rollback") })
			return boom
		},
		"before error": func(tx *Tx) error {
			mustExec(t, tx, `INSERT INTO log VALUES ('x')`)
			tx.BeforeCommit(func(context.Context, *Tx) error { return boom })
			tx.AfterCommit(func() { t.Error("AfterCommit should not run after a rollback") })
			return nil
		},
	} {
		if err := d.InTx(ctx, fn); !errors.Is(err, boom) {
			t.Errorf("%s: %v", name, err)
		}
	}
	func() {
		defer func() { _ = recover() }()
		_ = d.InTx(ctx, func(tx *Tx) error {
			mustExec(t, tx, `INSERT INTO log VALUES ('x')`)
			panic("boom")
		})
	}()
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM log WHERE s='x'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("no rows should be left after the rollback: %d %v", n, err)
	}
	// After the panic the write connection is still usable and not inside a transaction.
	ctxT, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := d.InTx(ctxT, func(tx *Tx) error { _, err := tx.Exec(ctx, `INSERT INTO log VALUES ('ok')`); return err }); err != nil {
		t.Fatalf("write transaction after a panic: %v", err)
	}
}

// TestBeginImmediate checks that a write transaction takes the write lock at BEGIN (IMMEDIATE), so another
// connection starting a write transaction at that point gets SQLITE_BUSY immediately.
func TestBeginImmediate(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER) STRICT`)
	other, err := sql.Open("sqlite", "file:"+filepath.Join(d.Dir(), DataFile)+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	err = d.InTx(ctx, func(tx *Tx) error {
		c, err := other.Conn(ctx)
		if err != nil {
			return err
		}
		defer c.Close()
		_, err = c.ExecContext(ctx, `BEGIN IMMEDIATE`)
		if Code(err) != CodeBusy {
			t.Errorf("after BEGIN another connection should get SQLITE_BUSY, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNestedWriteDetected(t *testing.T) {
	DetectNestedWrites.Store(true)
	t.Cleanup(func() { DetectNestedWrites.Store(false) })
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER) STRICT`)
	err := d.InTx(ctx, func(tx *Tx) error {
		var n int
		if err := d.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
			t.Errorf("a read through *DB inside the transaction should work: %v", err)
		}
		_, err := d.Exec(ctx, `INSERT INTO t VALUES (1)`)
		return err
	})
	if !errors.Is(err, ErrNestedWrite) {
		t.Fatalf("a nested write should give ErrNestedWrite, got %v", err)
	}
}

func TestCancellation(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER) STRICT`)

	// Waiting for the write connection is bounded by ctx.
	hold := make(chan struct{})
	held := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = d.InTx(ctx, func(tx *Tx) error { close(held); <-hold; return nil })
	}()
	<-held
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	err := d.InTx(waitCtx, func(tx *Tx) error { return nil })
	cancel()
	close(hold)
	wg.Wait()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timing out while waiting for the write connection should give the ctx error, got %v", err)
	}

	// Cancelled only after the last statement: the commit completes as usual.
	c2, cancel2 := context.WithCancel(ctx)
	if err := d.InTx(c2, func(tx *Tx) error {
		mustExec(t, tx, `INSERT INTO t VALUES (1)`)
		cancel2()
		return nil
	}); err != nil {
		t.Fatalf("a cancel after the statements should not affect the commit: %v", err)
	}
	// A statement run under an already cancelled ctx: the statement fails and the transaction rolls back.
	c3, cancel3 := context.WithCancel(ctx)
	err = d.InTx(c3, func(tx *Tx) error {
		mustExec(t, tx, `INSERT INTO t VALUES (2)`)
		cancel3()
		_, err := tx.Exec(c3, `INSERT INTO t VALUES (3)`)
		return err
	})
	if err == nil {
		t.Fatal("a statement under a cancelled ctx should fail")
	}
	var got string
	if err := d.QueryRow(ctx, `SELECT group_concat(v) FROM t`).Scan(&got); err != nil || got != "1" {
		t.Fatalf("only 1 should be left, got %q %v", got, err)
	}
}

func TestReadTxSnapshot(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER) STRICT`)
	mustExec(t, d, `INSERT INTO t VALUES (1)`)
	err := d.ReadTx(ctx, func(tx *Tx) error {
		count := func() int {
			var n int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := count()
		mustExec(t, d, `INSERT INTO t VALUES (2)`)
		if after := count(); after != before {
			t.Errorf("a read transaction should see a single snapshot: %d → %d", before, after)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO t VALUES (3)`); err == nil {
			t.Error("a write in a read transaction should fail")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a new query should see the latest commit: %d %v", n, err)
	}
}

func TestErrorHelpers(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE p (id TEXT PRIMARY KEY) STRICT`)
	mustExec(t, d, `CREATE TABLE r (id TEXT PRIMARY KEY, agent TEXT NOT NULL, status TEXT NOT NULL, origin TEXT,
		p TEXT REFERENCES p (id), UNIQUE (agent, origin)) STRICT`)
	mustExec(t, d, `CREATE UNIQUE INDEX r_one_active ON r (agent) WHERE status IN ('running')`)
	mustExec(t, d, `CREATE UNIQUE INDEX r_expr ON r (agent, coalesce(origin, '')) WHERE status = 'queued'`)
	mustExec(t, d, `CREATE TRIGGER r_append_only BEFORE DELETE ON r BEGIN SELECT RAISE(ABORT, 'r_is_append_only'); END`)
	mustExec(t, d, `INSERT INTO r VALUES ('1', 'a', 'running', 'x', NULL), ('2', 'a', 'queued', NULL, NULL)`)

	_, err := d.Exec(ctx, `INSERT INTO r VALUES ('3', 'a', 'running', 'y', NULL)`)
	if !IsUniqueViolation(err, "r", "agent") || IsUniqueViolation(err, "r", "agent", "origin") || !IsUniqueViolation(err, "r") {
		t.Errorf("partial unique index: %v", err)
	}
	_, err = d.Exec(ctx, `INSERT INTO r VALUES ('4', 'a', 'queued', NULL, NULL)`)
	if !IsUniqueIndexViolation(err, "r_expr") {
		t.Errorf("expression unique index: %v", err)
	}
	_, err = d.Exec(ctx, `INSERT INTO r VALUES ('5', 'a', 'done', 'x', NULL)`)
	if !IsUniqueViolation(err, "r", "agent", "origin") {
		t.Errorf("table-level unique constraint: %v", err)
	}
	_, err = d.Exec(ctx, `INSERT INTO r VALUES ('1', 'b', 'done', 'z', NULL)`)
	if Code(err) != CodeConstraintPK || !IsUniqueViolation(err, "r", "id") || !IsAnyUniqueViolation(err) {
		t.Errorf("primary key: %v", err)
	}
	_, err = d.Exec(ctx, `INSERT INTO r VALUES ('6', 'c', 'done', 'z', 'missing')`)
	if Code(err) != CodeConstraintFK || !IsConstraint(err) || IsAnyUniqueViolation(err) {
		t.Errorf("foreign key: %v", err)
	}
	_, err = d.Exec(ctx, `DELETE FROM r`)
	if !IsRaise(err, "r_is_append_only") || !IsConstraint(err) {
		t.Errorf("RAISE: %v", err)
	}
	var se *sqlite.Error
	if !errors.As(err, &se) {
		t.Errorf("the driver error should be kept: %T", err)
	}
}

func TestNextSeqAndEvents(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE sequences (name TEXT PRIMARY KEY, value INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `INSERT INTO sequences VALUES ('runs.queue_seq', 0)`)
	mustExec(t, d, `CREATE TABLE runs (id TEXT PRIMARY KEY, seq INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `CREATE TABLE ev.log (run TEXT NOT NULL, line TEXT NOT NULL) STRICT`)
	for i := 1; i <= 3; i++ {
		err := d.InTx(ctx, func(tx *Tx) error {
			seq, err := tx.NextSeq(ctx, "runs.queue_seq")
			if err != nil {
				return err
			}
			if seq != int64(i) {
				t.Errorf("seq = %d, want %d", seq, i)
			}
			id := strconv.Itoa(i)
			if _, err := tx.Exec(ctx, `INSERT INTO runs VALUES (?, ?)`, id, seq); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO ev.log VALUES (?, 'started')`, id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := d.InTx(ctx, func(tx *Tx) error { _, err := tx.NextSeq(ctx, "nope"); return err }); err == nil {
		t.Error("an unknown sequence should return an error")
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM runs JOIN ev.log l ON l.run = runs.id`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("cross-database JOIN: %d %v", n, err)
	}
}

func TestLockDir(t *testing.T) {
	dir := t.TempDir()
	l, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockDir(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("a second lock should give ErrLocked, got %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, LockFile))
	if err != nil || strings.TrimSpace(string(b)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("lock file content: %q %v", b, err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := LockDir(dir)
	if err != nil {
		t.Fatalf("should be able to lock again after the release: %v", err)
	}
	l2.Release()
}

func TestBackupAndCheckpoint(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER) STRICT`)
	mustExec(t, d, `CREATE TABLE ev.e (v INTEGER) STRICT`)
	for i := range 100 {
		mustExec(t, d, `INSERT INTO t VALUES (?)`, i)
		mustExec(t, d, `INSERT INTO ev.e VALUES (?)`, i)
	}
	if data, events := d.WALSize(); data == 0 || events == 0 {
		t.Fatalf("the WAL should not be empty after writes: %d %d", data, events)
	}
	res, err := d.Checkpoint(ctx, CheckpointTruncate)
	if err != nil || len(res) != 2 || res[0].Busy || res[1].Busy {
		t.Fatalf("Checkpoint: %+v %v", res, err)
	}
	if data, events := d.WALSize(); data != 0 || events != 0 {
		t.Fatalf("the WAL should be 0 after TRUNCATE: %d %d", data, events)
	}
	if err := d.Optimize(ctx); err != nil {
		t.Fatal(err)
	}
	backup := t.TempDir()
	if err := d.BackupTo(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := d.BackupTo(ctx, backup); err == nil {
		t.Fatal("the backup should refuse an existing target")
	}
	b := openTestDir(t, backup)
	var n, m int
	if err := b.QueryRow(ctx, `SELECT (SELECT count(*) FROM t), (SELECT count(*) FROM ev.e)`).Scan(&n, &m); err != nil || n != 100 || m != 100 {
		t.Fatalf("backup content: %d %d %v", n, m, err)
	}
}

// TestModerncLibcPinned: modernc.org/sqlite requires downstream modules to pin the same modernc.org/libc version as
// its own go.mod (see its GitLab issue #177).
// When upgrading sqlite, first look up the libc version in its go.mod, then change it here and in go.mod.
func TestModerncLibcPinned(t *testing.T) {
	want := map[string]string{"modernc.org/sqlite": "v1.60.1", "modernc.org/libc": "v1.77.1"}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	got := map[string]string{}
	for _, m := range info.Deps {
		if _, tracked := want[m.Path]; tracked {
			got[m.Path] = m.Version
		}
	}
	for path, v := range want {
		if got[path] != v {
			t.Errorf("%s = %q, want %s (the two must be upgraded together)", path, got[path], v)
		}
	}
}

// TestWriteHookRunsForEveryWrite checks that the global write hook runs before every write transaction commits:
// InTx, a single Exec, and Query/QueryRow on the write connection all count; if the hook fails, the whole write
// rolls back.
func TestWriteHookRunsForEveryWrite(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER NOT NULL) STRICT`)
	mustExec(t, d, `CREATE TABLE seen (n INTEGER NOT NULL) STRICT`)
	var calls int
	var fail bool
	d.SetWriteHook(func(ctx context.Context, tx *Tx) error {
		calls++
		if fail {
			return errors.New("hook failed")
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO seen VALUES (?)`, n)
		return err
	})
	mustExec(t, d, `INSERT INTO t VALUES (1)`)
	if err := d.InTx(ctx, func(tx *Tx) error { _, err := tx.Exec(ctx, `INSERT INTO t VALUES (2)`); return err }); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := d.QueryRow(ctx, `INSERT INTO t VALUES (3) RETURNING v`).Scan(&v); err != nil || v != 3 {
		t.Fatalf("RETURNING: %d %v", v, err)
	}
	rows, err := d.Query(ctx, `UPDATE t SET v = v * 10 RETURNING v`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CollectRows(rows, RowTo[int])
	if err != nil || len(got) != 3 {
		t.Fatalf("Query on the write connection: %v %v", got, err)
	}
	if calls != 4 {
		t.Fatalf("the hook should run 4 times, ran %d", calls)
	}
	var seen string
	if err := d.QueryRow(ctx, `SELECT group_concat(n) FROM seen`).Scan(&seen); err != nil || seen != "1,2,3,3" {
		t.Fatalf("the hook should see every write in the same transaction: %q %v", seen, err)
	}
	// RETURNING that matches no row: ErrNoRows and the transaction rolls back (the hook must still leave no trace).
	if err := d.QueryRow(ctx, `UPDATE t SET v = 0 WHERE v < 0 RETURNING v`).Scan(&v); !IsNoRows(err) {
		t.Fatalf("want ErrNoRows: %v", err)
	}
	fail = true
	if _, err := d.Exec(ctx, `INSERT INTO t VALUES (4)`); err == nil {
		t.Fatal("the write should fail when the hook fails")
	}
	if err := d.QueryRow(ctx, `INSERT INTO t VALUES (5) RETURNING v`).Scan(&v); err == nil {
		t.Fatal("a RETURNING write should fail when the hook fails")
	}
	rows, err = d.Query(ctx, `INSERT INTO t VALUES (6) RETURNING v`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if rows.Err() == nil {
		t.Fatal("Rows.Err should report the hook failure")
	}
	fail = false
	var n, s int
	if err := d.QueryRow(ctx, `SELECT (SELECT count(*) FROM t), (SELECT count(*) FROM seen)`).Scan(&n, &s); err != nil || n != 3 || s != 4 {
		t.Fatalf("both the failed writes and the hook should roll back: t=%d seen=%d %v", n, s, err)
	}
	// The write connection has been returned: writing can continue.
	mustExec(t, d, `INSERT INTO t VALUES (7)`)
}

// TestSQLNowAndClockTimestamp checks that SQL now() in a write transaction is the transaction start time (the same
// for every statement in the transaction) and clock_timestamp() is the time of the call; both use the injected clock.
func TestSQLNowAndClockTimestamp(t *testing.T) {
	var mu sync.Mutex
	clock := time.Date(2026, 11, 2, 8, 0, 0, 123456000, time.UTC)
	tick := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(time.Second)
		return clock
	}
	d, err := Open(context.Background(), Options{Dir: t.TempDir(), Now: tick})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (a INTEGER NOT NULL, b INTEGER NOT NULL) STRICT`)
	var txNow time.Time
	if err := d.InTx(ctx, func(tx *Tx) error {
		txNow = tx.Now()
		for range 2 {
			if _, err := tx.Exec(ctx, `INSERT INTO t VALUES (now(), clock_timestamp())`); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Query(ctx, `SELECT a, b FROM t ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	var as, bs []time.Time
	var a, b time.Time
	if _, err := ForEachRow(rows, []any{&a, &b}, func() error { as, bs = append(as, a), append(bs, b); return nil }); err != nil {
		t.Fatal(err)
	}
	if !as[0].Equal(txNow) || !as[1].Equal(txNow) {
		t.Errorf("now() should be the transaction start %v, got %v", txNow, as)
	}
	if !bs[0].After(txNow) || !bs[1].After(bs[0]) {
		t.Errorf("clock_timestamp() should read the clock on every call: %v (transaction start %v)", bs, txNow)
	}
	var readNow time.Time
	if err := d.QueryRow(ctx, `SELECT now()`).Scan(&readNow); err != nil || !readNow.After(bs[1]) {
		t.Errorf("now() on a read connection should be the call time: %v %v", readNow, err)
	}
}

func TestISOTime(t *testing.T) {
	d := openTest(t)
	at := time.Date(2026, 11, 2, 8, 0, 0, 123456000, time.UTC)
	var raw string
	if err := d.QueryRow(context.Background(), `SELECT json_object('at', iso_time($1), 'none', iso_time(NULL))`, at).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var v struct {
		At   time.Time  `json:"at"`
		None *time.Time `json:"none"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil || !v.At.Equal(at) || v.None != nil {
		t.Fatalf("iso_time should keep microseconds and be parsable by Go: %s %+v %v", raw, v, err)
	}
}

// TestQueryHookPausesInsideTransaction checks that the statement hook is called before every statement, inside that
// statement's transaction: while it is paused in a write transaction other writes wait for it; queries on the
// read-only pool go through it too.
func TestQueryHookPausesInsideTransaction(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	mustExec(t, d, `CREATE TABLE t (v INTEGER)`)
	entered, release := make(chan struct{}), make(chan struct{})
	var seen sync.Map
	var once sync.Once
	d.SetQueryHook(func(_ context.Context, q string) {
		seen.Store(q, true)
		if strings.Contains(q, "pause here") {
			once.Do(func() { close(entered); <-release })
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- d.InTx(ctx, func(tx *Tx) error {
			if _, err := tx.Exec(ctx, `INSERT INTO t VALUES (1)`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO t VALUES (2) -- pause here`)
			return err
		})
	}()
	<-entered
	other := make(chan error, 1)
	go func() { _, err := d.Exec(ctx, `INSERT INTO t VALUES (3)`); other <- err }()
	select {
	case err := <-other:
		t.Fatalf("while a write transaction is paused, other writes should wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("read an uncommitted write: %d %v", n, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-other; err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`INSERT INTO t VALUES (1)`, `INSERT INTO t VALUES (3)`, `SELECT count(*) FROM t`} {
		if _, ok := seen.Load(q); !ok {
			t.Errorf("the hook did not see: %s", q)
		}
	}
	d.SetQueryHook(nil)
	seen.Delete(`SELECT count(*) FROM t`)
	if err := d.QueryRow(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, ok := seen.Load(`SELECT count(*) FROM t`); ok {
		t.Fatal("the hook was still called after it was removed")
	}
}

// TestWriteWaiters checks that WriteWaiters counts the calls waiting for the write connection, and drops them once
// they get it or give up.
func TestWriteWaiters(t *testing.T) {
	d := openTest(t)
	ctx := context.Background()
	held, release, ended := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { ended <- d.InTx(ctx, func(*Tx) error { close(held); <-release; return nil }) }()
	<-held
	if n := d.WriteWaiters(); n != 0 {
		t.Fatalf("the holder should not count as a waiter: %d", n)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	results := make(chan error, 2)
	go func() { results <- d.InTx(ctx, func(*Tx) error { return nil }) }()
	go func() { results <- d.InTx(waitCtx, func(*Tx) error { return nil }) }()
	deadline := time.Now().Add(5 * time.Second)
	for d.WriteWaiters() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("waiters: %d", d.WriteWaiters())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait should return the cancellation: %v", err)
	}
	if n := d.WriteWaiters(); n != 1 {
		t.Fatalf("waiters after the cancel: %d", n)
	}
	close(release)
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if n := d.WriteWaiters(); n != 0 {
		t.Fatalf("waiters after all got the connection: %d", n)
	}
}

// TestCommitFailureRollsBackAndKeepsWriter: deferred foreign keys are only checked at commit; when the commit fails
// SQLite leaves the transaction open, so the write connection must be rolled back before it is returned, or every
// later write fails.
func TestCommitFailureRollsBackAndKeepsWriter(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	mustExec(t, d, `CREATE TABLE parent(id TEXT PRIMARY KEY)`)
	mustExec(t, d, `CREATE TABLE child(id TEXT PRIMARY KEY, parent_id TEXT REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	after := false
	err := d.InTx(ctx, func(tx *Tx) error {
		tx.AfterCommit(func() { after = true })
		_, err := tx.Exec(ctx, `INSERT INTO child VALUES('c1','missing')`)
		return err
	})
	if Code(err) != CodeConstraintFK || after {
		t.Fatalf("a foreign key violation at commit should fail without running the after-commit hook: %v %v", err, after)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM child`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a failed commit should leave no data: %d %v", n, err)
	}
	if err := d.InTx(ctx, func(tx *Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO parent VALUES('p1')`)
		return err
	}); err != nil {
		t.Fatalf("the write connection should still work after a failed commit: %v", err)
	}
}
