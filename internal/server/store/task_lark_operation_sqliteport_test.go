//go:build sqliteport

// SQLITE-PORT(G5): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 task_lark_operation_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskLarkOperationUnknownCConfirmsOriginalActionOnly

package store_test

import (
	"context"
	"errors"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/taskmodel"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskLarkOperationUnknownCConfirmsOriginalActionOnly(t *testing.T) {
	requireRuntimeRecovery(t)
	s, in := larkSwitchReadyFixture(t, nil)
	ctx := context.Background()
	var saturated []*pgxpool.Conn
	defer func() {
		for _, c := range saturated {
			c.Release()
		}
	}()
	n := &larkOperationNative{}
	n.seal = func() error {
		// Production commit relay runs after C commits and returns its pool
		// connection. Withhold every real pool connection before confirmation.
		s.TaskCommitted = func() {
			for i := 0; i < int(s.Pool.Config().MaxConns); i++ {
				c, err := s.Pool.Acquire(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				saturated = append(saturated, c)
			}
		}
		return nil
	}
	svc, ex := larkOperationService(t, s, n)
	_, err := svc.SwitchLarkAccount(ctx, auth.Console(), in.TaskID, larkOperationInput(in))
	if err == nil || n.releases.Load() != 0 {
		t.Fatal("unconfirmed C released gates", err)
	}
	s.TaskCommitted = nil
	for _, c := range saturated {
		c.Release()
	}
	saturated = nil
	var fences, groups int
	if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences),(SELECT count(*) FROM task_lark_credential_bindings)`).Scan(&fences, &groups); err != nil || fences != 0 || groups != 1 {
		t.Fatal("test did not commit C before confirmation failed", err)
	}
	for _, member := range in.Members {
		if lease, err := ex.AcquireRuntimeHandoff(ctx, []uuid.UUID{member.RuntimeID}); !errors.Is(err, local.ErrRuntimeBusy) || lease != nil {
			t.Fatal("C unknown lost memory gate", err)
		}
	}
	committed, err := svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
	if err != nil || committed.Continuation == nil {
		t.Fatal(err)
	}
	capability, err := svc.ConsoleMessageCapability(ctx, auth.Console(), in.TaskID, committed.Continuation.NodeRunID)
	if err != nil || capability.CanSend {
		t.Fatal("unknown C exposed strict send as available", err)
	}
	if _, err = svc.SendNodeInboxMessage(ctx, auth.Console(), in.TaskID, committed.Continuation.NodeRunID, taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: committed.SourceAgentID, ExpectedRuntimeID: committed.SourceRuntimeID, Text: "must retain draft", WhenBusy: "queue"}, 1000); !apperr.Is(err, "task_conflict") {
		t.Fatal("unknown C accepted new strict message", err)
	}
	var messages int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&messages); err != nil || messages != 0 {
		t.Fatal("unknown C left accepted message", err)
	}
	if err = svc.ReconcileLarkSwitchOutcomes(ctx); err != nil {
		t.Fatal(err)
	}
	receipt, err := svc.LarkSwitchAction(ctx, auth.Console(), in.TaskID, in.ActionID)
	if err != nil || receipt.Status != "succeeded" || receipt.Continuation == nil || n.releases.Load() != 1 || n.acquires.Load() != 1 {
		t.Fatal("original-action confirmation repeated operation", err)
	}
	var turns int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, in.TaskID).Scan(&turns); err != nil || turns != 2 {
		t.Fatal("unknown C retried continuation", turns, err)
	}
}
