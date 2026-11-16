package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Querier is the set of read and write methods shared by *DB and *Tx, for functions that may be called either
// inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *Row
}

var (
	_ Querier = (*DB)(nil)
	_ Querier = (*Tx)(nil)
)

// Tx is a transaction. A write transaction (InTx) runs on the single write connection and starts with BEGIN
// IMMEDIATE; a read-only transaction (ReadTx) runs on the read-only pool and sees one snapshot from its first query
// on.
type Tx struct {
	tx       *sql.Tx
	now      time.Time
	readOnly bool
	before   []func(context.Context, *Tx) error
	after    []func()
	done     bool
	values   map[any]any
	hook     *QueryHook
}

// SetValue records a value on the transaction (for example "plugin events were written") for hooks that run later
// in the same transaction to read; key should be a type private to the caller.
func (t *Tx) SetValue(key, value any) {
	if t.values == nil {
		t.values = map[any]any{}
	}
	t.values[key] = value
}

// Value returns the value recorded by SetValue, or nil if there is none.
func (t *Tx) Value(key any) any { return t.values[key] }

// InTx runs fn in a write transaction: when fn returns nil it runs the BeforeCommit hooks and then the global write
// hook (SetWriteHook) and commits; after a successful commit it runs the AfterCommit hooks in order. If any step
// fails it rolls back and returns that error. Waiting for the write connection is bounded by ctx; after BEGIN,
// statements still use the caller's ctx (cancelling interrupts the statement and the transaction rolls back), but
// the commit itself cannot be cancelled.
func (d *DB) InTx(ctx context.Context, fn func(*Tx) error) (err error) {
	t, end, err := d.beginWrite(ctx)
	if err != nil {
		return err
	}
	ended := false
	defer func() {
		// Roll back and return the connection even when fn panics, or the connection goes back to the pool with an
		// unfinished transaction.
		if !ended {
			_ = end(errors.New("db: the write transaction callback panicked"))
		}
	}()
	fnErr := fn(t)
	ended = true
	return end(fnErr)
}

// beginWrite acquires the write connection and starts a transaction with BEGIN IMMEDIATE; end(fnErr) finishes it:
// when fnErr is nil it runs the before-commit hooks and the global write hook and commits, otherwise it rolls back;
// either way it returns the write connection. end may be called only once.
func (d *DB) beginWrite(ctx context.Context) (*Tx, func(error) error, error) {
	c, release, err := d.acquireWriter(ctx)
	if err != nil {
		return nil, nil, err
	}
	stx, err := c.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("db: begin the write transaction: %w", err)
	}
	t := &Tx{tx: stx, now: d.now(), hook: d.queryHook.Load()}
	d.writeTxNow.Store(t.now.UnixMicro())
	end := func(fnErr error) error {
		defer release()
		defer d.writeTxNow.Store(0)
		if fnErr == nil {
			fnErr = t.runBefore(ctx, d.writeHook.Load())
		}
		if fnErr != nil {
			// Errors such as an interrupted statement make SQLite roll back the whole transaction on its own; rolling
			// back again then reports "no transaction is active", which is safe to ignore.
			t.done = true
			_ = stx.Rollback()
			return fnErr
		}
		t.done = true
		if err := stx.Commit(); err != nil {
			return fmt.Errorf("db: commit: %w", err)
		}
		for _, f := range t.after {
			f()
		}
		return nil
	}
	return t, end, nil
}

// runBefore runs the before-commit hooks in registration order (a hook may register more hooks), then the global
// write hook.
func (t *Tx) runBefore(ctx context.Context, global *WriteHook) error {
	for i := 0; i < len(t.before); i++ {
		if err := t.before[i](ctx, t); err != nil {
			return err
		}
	}
	if global != nil && *global != nil {
		if err := (*global)(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// WriteHook runs before every write transaction commits, after all BeforeCommit hooks, including single write
// statements on *DB.
// store uses it to turn the changes recorded by triggers (plugin_changes) into plugin events in the same
// transaction.
type WriteHook func(context.Context, *Tx) error

// SetWriteHook sets the global write hook; set it before any writes start.
func (d *DB) SetWriteHook(h WriteHook) { d.writeHook.Store(&h) }

// ReadTx runs fn on the read-only pool; the queries inside fn see one snapshot. Hooks registered in a read-only
// transaction are not run.
func (d *DB) ReadTx(ctx context.Context, fn func(*Tx) error) error {
	stx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("db: begin a read-only transaction: %w", err)
	}
	t := &Tx{tx: stx, now: d.now(), readOnly: true, hook: d.queryHook.Load()}
	defer func() { t.done = true; _ = stx.Rollback() }()
	return fn(t)
}

// BeginRead starts a read-only transaction on the read-only pool; the caller must Commit or Rollback it (deferring
// Rollback is fine, it does nothing after a commit).
// Use ReadTx when the work can be written as a callback.
func (d *DB) BeginRead(ctx context.Context) (*Tx, error) {
	stx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("db: begin a read-only transaction: %w", err)
	}
	return &Tx{tx: stx, now: d.now(), readOnly: true, hook: d.queryHook.Load()}, nil
}

// Commit ends a read-only transaction started by BeginRead. Write transactions are committed by InTx and must not
// call it.
func (t *Tx) Commit(context.Context) error {
	if !t.readOnly {
		return errors.New("db: write transactions are committed by InTx")
	}
	if t.done {
		return sql.ErrTxDone
	}
	t.done = true
	return t.tx.Commit()
}

// Rollback ends a read-only transaction started by BeginRead; it does nothing if the transaction already ended.
func (t *Tx) Rollback(context.Context) error {
	if !t.readOnly {
		return errors.New("db: write transactions are rolled back by InTx")
	}
	if t.done {
		return nil
	}
	t.done = true
	return t.tx.Rollback()
}

// Now returns the transaction start time, the equivalent of PostgreSQL's now(): every statement in the same
// transaction gets the same value.
func (t *Tx) Now() time.Time { return t.now }

// BeforeCommit registers a hook that runs before commit, still inside the transaction; if it returns an error the
// transaction rolls back.
// It is used for work formerly done by deferred constraint triggers (for example generating plugin events from the
// state at commit time, or checking that a binding group is complete).
func (t *Tx) BeforeCommit(fn func(context.Context, *Tx) error) {
	t.before = append(t.before, fn)
}

// AfterCommit registers a hook that runs only after a successful commit (for example waking the plugin host),
// matching the transactional semantics of pg_notify.
// The hook cannot fail and should not block.
func (t *Tx) AfterCommit(fn func()) {
	t.after = append(t.after, fn)
}

// Exec runs a statement inside the transaction.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	if err := t.usable(ctx, query); err != nil {
		return Result{}, err
	}
	vals, err := convertArgs(args)
	if err != nil {
		return Result{}, err
	}
	res, err := t.tx.ExecContext(ctx, query, vals...)
	if err != nil {
		return Result{}, err
	}
	n, _ := res.RowsAffected()
	return Result{rows: n}, nil
}

// Query runs a query inside the transaction.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	if err := t.usable(ctx, query); err != nil {
		return nil, err
	}
	vals, err := convertArgs(args)
	if err != nil {
		return nil, err
	}
	rows, err := t.tx.QueryContext(ctx, query, vals...)
	if err != nil {
		return nil, err
	}
	return &Rows{rows: rows}, nil
}

// QueryRow runs a query that reads a single row inside the transaction.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := t.Query(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

func (t *Tx) usable(ctx context.Context, query string) error {
	if t.done {
		return sql.ErrTxDone
	}
	if t.readOnly && isWrite(query) {
		return errors.New("db: cannot write in a read-only transaction")
	}
	if t.hook != nil {
		(*t.hook)(ctx, query)
	}
	return nil
}

// NextSeq takes the next number from the sequences table (replacing the former identity columns). It may only be
// called in a write transaction.
func (t *Tx) NextSeq(ctx context.Context, name string) (int64, error) {
	var v int64
	err := t.QueryRow(ctx, `UPDATE sequences SET value = value + 1 WHERE name = ? RETURNING value`, name).Scan(&v)
	if errors.Is(err, ErrNoRows) {
		return 0, fmt.Errorf("db: no sequence %q", name)
	}
	return v, err
}
