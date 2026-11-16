package db

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// checkpointBusyMS is how long (in milliseconds) the write connection waits for readers during a checkpoint.
const checkpointBusyMS = 200

// CheckpointMode is a wal_checkpoint mode.
type CheckpointMode string

const (
	// CheckpointPassive does not wait for readers and writes back as much of the WAL into the main file as it can;
	// it runs periodically in normal operation.
	CheckpointPassive CheckpointMode = "PASSIVE"
	// CheckpointTruncate writes back all frames and truncates the WAL to 0; it cannot finish (Busy) while a reader
	// holds an old snapshot.
	CheckpointTruncate CheckpointMode = "TRUNCATE"
)

// CheckpointResult is the checkpoint result for one database.
type CheckpointResult struct {
	Schema string
	// Busy means a reader or writer was in the way and the checkpoint did not finish.
	Busy bool
	// LogFrames is the number of frames in the WAL; Checkpointed is the number of frames written back.
	LogFrames, Checkpointed int64
}

// Checkpoint runs one checkpoint each on data.db and events.db. It runs on the write connection, so it excludes
// write transactions.
func (d *DB) Checkpoint(ctx context.Context, mode CheckpointMode) ([]CheckpointResult, error) {
	c, release, err := d.acquireWriter(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// TRUNCATE waits up to busy_timeout (10s) for a reader holding an old snapshot, all the while holding the single
	// write connection; shorten the wait during the checkpoint, and if it cannot finish report Busy and try next time.
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

// Optimize runs PRAGMA optimize so SQLite updates statistics as needed. It runs once a day together with the
// TRUNCATE checkpoint.
func (d *DB) Optimize(ctx context.Context) error {
	_, err := d.Exec(ctx, "PRAGMA optimize")
	return err
}

// WALWarnBytes is the size above which a single WAL is considered too large: exceeding it usually means a read
// transaction that never ends is blocking the checkpoint.
// Background maintenance logs a warning based on it, and the health endpoint reports walWarning based on it.
const WALWarnBytes int64 = 256 << 20

// WALSize returns the byte size of the two databases' -wal files (0 when missing), for warnings and the status page.
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

// BackupTo uses VACUUM INTO to write each of the two databases as a complete, consistent new file in dir (data.db,
// events.db).
// It runs on a read-only connection and does not block writes; it refuses, rather than overwrites, when a target
// file already exists.
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
	// VACUUM INTO does not change the source database, but query_only blocks it anyway; lift it only for this
	// borrow, and if restoring it fails, make the pool discard this connection.
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
