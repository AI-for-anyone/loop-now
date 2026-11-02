package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// checkpointBusyMS 是检查点期间写连接等待读者的上限（毫秒）。
const checkpointBusyMS = 200

// CheckpointMode 是 wal_checkpoint 的模式。
type CheckpointMode string

const (
	// CheckpointPassive 不等待读者，尽量把 WAL 写回主文件；平时周期性执行。
	CheckpointPassive CheckpointMode = "PASSIVE"
	// CheckpointTruncate 写回全部帧并把 WAL 截为 0；有读者占着旧快照时做不完（Busy）。
	CheckpointTruncate CheckpointMode = "TRUNCATE"
)

// CheckpointResult 是一个库的检查点结果。
type CheckpointResult struct {
	Schema string
	// Busy 表示有读者或写者挡住，没能做完。
	Busy bool
	// LogFrames 是 WAL 中的帧数，Checkpointed 是已写回的帧数。
	LogFrames, Checkpointed int64
}

// Checkpoint 对 data.db 与 events.db 各执行一次检查点。在写连接上执行，与写事务互斥。
func (d *DB) Checkpoint(ctx context.Context, mode CheckpointMode) ([]CheckpointResult, error) {
	c, release, err := d.acquireWriter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// TRUNCATE 遇到占着旧快照的读者会按 busy_timeout（10s）等待，而它占着唯一的写连接；
	// 检查点期间把等待缩短，做不完就报 Busy，下次再做。
	bg := context.WithoutCancel(ctx)
	if _, err := c.ExecContext(bg, fmt.Sprintf("PRAGMA busy_timeout=%d", checkpointBusyMS)); err != nil {
		return nil, err
	}
	defer func() {
		if _, err := c.ExecContext(bg, fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeoutMS)); err != nil {
			_ = c.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	var out []CheckpointResult
	for _, schema := range []string{"main", EventsSchema} {
		r := CheckpointResult{Schema: schema}
		var busy int
		if err := c.QueryRowContext(bg, fmt.Sprintf("PRAGMA %s.wal_checkpoint(%s)", schema, mode)).Scan(&busy, &r.LogFrames, &r.Checkpointed); err != nil {
			return out, fmt.Errorf("db: %s 检查点失败：%w", schema, err)
		}
		r.Busy = busy != 0
		out = append(out, r)
	}
	return out, nil
}

// Optimize 执行 PRAGMA optimize，让 SQLite 按需更新统计信息。与 TRUNCATE 检查点一起每天执行一次。
func (d *DB) Optimize(ctx context.Context) error {
	_, err := d.Exec(ctx, "PRAGMA optimize")
	return err
}

// WALSize 返回两个库 -wal 文件的字节数（不存在时为 0），供告警与状态页使用。
func (d *DB) WALSize() (data, events int64) {
	size := func(name string) int64 {
		st, err := os.Stat(filepath.Join(d.dir, name+"-wal"))
		if err != nil {
			return 0
		}
		return st.Size()
	}
	return size(DataFile), size(EventsFile)
}

// BackupTo 用 VACUUM INTO 把两个库各写成 dir 下的一个完整、一致的新文件（data.db、events.db）。
// 在只读连接上执行，不挡写；目标文件已存在时拒绝，不覆盖。
func (d *DB) BackupTo(ctx context.Context, dir string) error {
	targets := map[string]string{"main": filepath.Join(dir, DataFile), EventsSchema: filepath.Join(dir, EventsFile)}
	for _, p := range targets {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("db: 备份目标 %s 已存在", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	c, err := d.r.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	// VACUUM INTO 不改源库，但 query_only 也会拦它；只在这次借用期间放开，恢复失败就让连接池丢弃这个连接。
	if _, err := c.ExecContext(ctx, "PRAGMA query_only=0"); err != nil {
		return err
	}
	defer func() {
		if _, err := c.ExecContext(context.WithoutCancel(ctx), "PRAGMA query_only=1"); err != nil {
			_ = c.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	for _, schema := range []string{"main", EventsSchema} {
		if _, err := c.ExecContext(ctx, "VACUUM "+schema+" INTO ?", targets[schema]); err != nil {
			return fmt.Errorf("db: 备份 %s 失败：%w", schema, err)
		}
	}
	return nil
}
