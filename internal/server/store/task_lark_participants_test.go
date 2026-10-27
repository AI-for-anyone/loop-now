package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkParticipantsFrozenReadOnly(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	before, e := s.GetTask(ctx, in.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	_, list, e := s.TaskLarkParticipants(ctx, in.TaskID)
	if e != nil || len(list) != 2 {
		t.Fatal("all original instances", e)
	}
	for _, p := range list {
		if p.LarkAllowed == nil || !*p.LarkAllowed || p.Available || p.UnavailableReason == nil || *p.UnavailableReason != "handoff_in_progress" || p.BindingRevision != 0 {
			t.Fatal("fenced local facts", p)
		}
	}
	_, e = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, e := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "QA", json.RawMessage(`{"status":"succeeded"}`))
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	// These later mutable facts are deliberately irrelevant to the projection.
	if _, e = s.Pool.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=NULL,revision=revision+1 WHERE id=$1`, in.Capture.Target.AccountID); e != nil {
		t.Fatal(e)
	}
	detail, list, e := s.TaskLarkParticipants(ctx, in.TaskID)
	if e != nil || len(list) != 2 {
		t.Fatal(e)
	}
	if detail.Task.Version <= before.View.Task.Version {
		t.Fatal("stale task version")
	}
	for _, p := range list {
		if p.BindingRevision != 1 || !p.Available || p.LarkAllowed == nil || !*p.LarkAllowed || p.UnavailableReason != nil {
			t.Fatal("fixed overlay unavailable", p)
		}
		if p.RuntimeID == in.Members[1].RuntimeID && p.Prepared {
			t.Fatal("read prepared future followup")
		}
	}
	beforeRead, e := s.GetTask(ctx, in.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	var rawBefore []byte
	if e = s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, in.Members[1].RuntimeID).Scan(&rawBefore); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.TaskLarkParticipants(ctx, in.TaskID); e != nil {
		t.Fatal(e)
	}
	afterRead, e := s.GetTask(ctx, in.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(beforeRead)
	b, _ := json.Marshal(afterRead)
	if !bytes.Equal(a, b) {
		t.Fatal("console read mutated task")
	}
	var rawAfter []byte
	s.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, in.Members[1].RuntimeID).Scan(&rawAfter)
	if !bytes.Equal(rawBefore, rawAfter) {
		t.Fatal("read rewrote frozen snapshot")
	}
	if _, e = s.Pool.Exec(ctx, `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, in.Members[1].RuntimeID, []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
	_, list, e = s.TaskLarkParticipants(ctx, in.TaskID)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range list {
		if p.RuntimeID == in.Members[1].RuntimeID && (p.LarkAllowed != nil || p.Available || p.UnavailableReason == nil || *p.UnavailableReason != "snapshot_unavailable") {
			t.Fatal("bad original snapshot inferred permission", p)
		}
	}
}

func TestTaskLarkParticipantsConsoleOnlyBeforeStore(t *testing.T) {
	s := &tasks.Service{}
	for _, kind := range []auth.Kind{auth.KindAgent, auth.KindExternal} {
		if _, _, e := s.GetLarkSwitchParticipants(context.Background(), auth.Principal{Kind: kind, ID: uuid.New()}, uuid.New()); e == nil {
			t.Fatal("management exposed")
		}
	}
}
