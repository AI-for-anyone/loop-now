package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Querier 是 *DB 与 *Tx 共有的读写方法，供既可在事务内也可在事务外调用的函数使用。
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *Row
}

var (
	_ Querier = (*DB)(nil)
	_ Querier = (*Tx)(nil)
)

// Tx 是一个事务。写事务（InTx）在唯一的写连接上，以 BEGIN IMMEDIATE 开始；只读事务（ReadTx）在只读池上，
// 从第一条查询起看到同一个快照。
type Tx struct {
	tx       *sql.Tx
	now      time.Time
	readOnly bool
	before   []func(context.Context, *Tx) error
	after    []func()
	done     bool
}

// InTx 在写事务中执行 fn：fn 返回 nil 时依次执行 BeforeCommit 钩子再提交，提交成功后依次执行 AfterCommit 钩子；
// 任一步出错则回滚并返回该错误。等待写连接受 ctx 约束；BEGIN 之后语句仍用调用方 ctx（取消会中断语句、
// 事务回滚），提交本身不可取消。
func (d *DB) InTx(ctx context.Context, fn func(*Tx) error) error {
	c, release, err := d.acquireWriter(ctx)
	if err != nil {
		return err
	}
	defer release()
	stx, err := c.BeginTx(context.WithoutCancel(ctx), nil)
	if err != nil {
		return fmt.Errorf("db: 开始写事务失败：%w", err)
	}
	t := &Tx{tx: stx, now: d.now()}
	defer func() {
		// fn 恐慌时也要回滚，否则连接带着未结束的事务回到池里。
		if !t.done {
			t.done = true
			_ = stx.Rollback()
		}
	}()
	if err := t.run(ctx, fn); err != nil {
		// 语句被中断等错误会让 SQLite 自己回滚整个事务，此时再回滚会报“没有活动事务”，忽略即可。
		t.done = true
		_ = stx.Rollback()
		return err
	}
	t.done = true
	if err := stx.Commit(); err != nil {
		return fmt.Errorf("db: 提交失败：%w", err)
	}
	for _, f := range t.after {
		f()
	}
	return nil
}

func (t *Tx) run(ctx context.Context, fn func(*Tx) error) error {
	if err := fn(t); err != nil {
		return err
	}
	// 钩子可以再登记钩子（例如插件事件在提交前补写投递记录），按登记顺序执行到没有新的为止。
	for i := 0; i < len(t.before); i++ {
		if err := t.before[i](ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// ReadTx 在只读池上执行 fn，fn 内的查询看到同一个快照。只读事务里登记的钩子不会执行。
func (d *DB) ReadTx(ctx context.Context, fn func(*Tx) error) error {
	stx, err := d.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("db: 开始只读事务失败：%w", err)
	}
	t := &Tx{tx: stx, now: d.now(), readOnly: true}
	defer func() { t.done = true; _ = stx.Rollback() }()
	return fn(t)
}

// Now 返回事务开始时刻，对应 PostgreSQL 的 now()：同一事务里所有语句取同一个值。
func (t *Tx) Now() time.Time { return t.now }

// BeforeCommit 登记一个在提交前、仍在事务内执行的钩子；它返回错误会使事务回滚。
// 用于原来由延迟约束触发器完成的工作（例如按提交时的状态生成插件事件、校验绑定组完整）。
func (t *Tx) BeforeCommit(fn func(context.Context, *Tx) error) {
	t.before = append(t.before, fn)
}

// AfterCommit 登记一个只在提交成功后执行的钩子（例如唤醒插件宿主），对应 pg_notify 的事务语义。
// 钩子不能失败，也不应阻塞。
func (t *Tx) AfterCommit(fn func()) {
	t.after = append(t.after, fn)
}

// Exec 在事务内执行语句。
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	if err := t.usable(query); err != nil {
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

// Query 在事务内执行查询。
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	if err := t.usable(query); err != nil {
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

// QueryRow 在事务内执行只取一行的查询。
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	rows, err := t.Query(ctx, query, args...)
	return &Row{rows: rows, err: err}
}

func (t *Tx) usable(query string) error {
	if t.done {
		return sql.ErrTxDone
	}
	if t.readOnly && isWrite(query) {
		return errors.New("db: 只读事务里不能写")
	}
	return nil
}

// NextSeq 从 sequences 表取下一个号（对应原来的 identity 列）。只能在写事务里调用。
func (t *Tx) NextSeq(ctx context.Context, name string) (int64, error) {
	var v int64
	err := t.QueryRow(ctx, `UPDATE sequences SET value = value + 1 WHERE name = ? RETURNING value`, name).Scan(&v)
	if errors.Is(err, ErrNoRows) {
		return 0, fmt.Errorf("db: 没有序列 %q", name)
	}
	return v, err
}
