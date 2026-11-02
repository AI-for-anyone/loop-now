//go:build sqliteport

// SQLITE-PORT(G5): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 task_lark_target_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskLarkTargetLockWaitCannotCrossGrantExpiry

package store_test

import (
	"context"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

func TestTaskLarkTargetLockWaitCannotCrossGrantExpiry(t *testing.T) {
	s, reservation := larkFenceFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, _ := s.GetTask(ctx, reservation.TaskID)
	expires := time.Now().Add(time.Second)
	account := larkSwitchTarget(t, s, r.View.Task.ProjectID, expires)
	holder, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM project_cli_versions WHERE id=$1 FOR UPDATE`, *account.View.CurrentVersionID); err != nil {
		t.Fatal(err)
	}
	pid := make(chan uint32, 1)
	done := make(chan error, 1)
	go func() {
		_, err := s.MutateTask(ctx, reservation.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			pid <- m.Tx.Conn().PgConn().PID()
			_, err := s.CaptureTaskLarkTargetTx(ctx, m, account.View.ID, *account.View.CurrentVersionID, account.View.Revision)
			return nil, err
		})
		done <- err
	}()
	var backend uint32
	select {
	case backend = <-pid:
	case <-ctx.Done():
		t.Fatal("task lock not acquired")
	}
	for {
		var waiting bool
		if err = s.Pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, backend).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("did not observe actual target row lock wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if until := time.Until(expires.Add(20 * time.Millisecond)); until > 0 {
		time.Sleep(until)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("transaction-start time allowed an expired grant after lock wait")
		}
	case <-ctx.Done():
		t.Fatal("target validation did not complete")
	}
}
