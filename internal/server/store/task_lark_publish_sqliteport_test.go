//go:build sqliteport

// SQLITE-PORT(G5): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 task_lark_publish_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskLarkMigrationRejectsIncompletePublishedGroup

package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestTaskLarkMigrationRejectsIncompletePublishedGroup(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	record, err := s.GetTask(ctx, in.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,execution_mode,text,lark_binding_revision,lark_switch_action_id) VALUES($1,$2,$3,$4,$5,$6,'queued','task_node','task_node','synthetic final C',1,$7)`, turn, in.TaskID, next.RuntimeID, next.ID, uuid.New(), uuid.New(), in.ActionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE task_lark_switch_actions SET status='succeeded',receipt='{}',finished_at=clock_timestamp() WHERE actor_key=$1 AND action_id=$2`, in.ActorKey, in.ActionID); err != nil {
		t.Fatal(err)
	}
	target := in.Capture.Target
	if _, err = tx.Exec(ctx, `INSERT INTO task_lark_credential_bindings(task_id,revision,project_id,account_id,version_id,app_id,user_id,brand,runtime_variant_version,actor_key,action_id,continuation_node_run_id,continuation_turn_id) VALUES($1,1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, in.TaskID, record.View.Task.ProjectID, target.AccountID, target.VersionID, target.Identity.AppID, target.Identity.OpenID, target.Identity.Brand, target.RuntimeVariantVersion, in.ActorKey, in.ActionID, next.ID, turn); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err == nil {
		t.Fatal("deferred migration constraint allowed zero-member publication")
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$1`, in.TaskID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed publication left binding audit", count, err)
	}
}
