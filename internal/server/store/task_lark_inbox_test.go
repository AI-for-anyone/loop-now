package store_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkDeferredInboxReconcilesOriginalMessageOnce(t *testing.T) {
	s, in := larkFenceFixture(t)
	ctx := context.Background()
	if _, err := reserveLarkFence(ctx, s, in); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	member := in.Members[0]
	message := uuid.New()
	if err := svc.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: member.AgentID, RuntimeID: member.RuntimeID}, Text: "original deferred message", WhenBusy: "queue"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileTaskLarkInbox(ctx, in.TaskID); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	if err := s.Pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM task_lark_deferred_inbox WHERE message_id=$1`, message).Scan(&delivered); err != nil || delivered {
		t.Fatal("pending handoff consumed deferred intent", err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return nil, s.FailTaskLarkFenceTx(ctx, m, in.ActorKey, in.ActionID, "safe_handoff_failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fail after the production enqueue but before the delivery marker. Both
	// must roll back; a later reconciliation may notify the same real message.
	if _, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_lark_delivery() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic delivery commit failure'; END $$; CREATE TRIGGER reject_lark_delivery BEFORE UPDATE ON task_lark_deferred_inbox FOR EACH ROW EXECUTE FUNCTION reject_lark_delivery()`); err != nil {
		t.Fatal(err)
	}
	if err = svc.ReconcileTaskLarkInbox(ctx, in.TaskID); err == nil {
		t.Fatal("delivery failure hidden")
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE trigger_inbox_message_id=$1`, message).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed delivery committed partial turn", count, err)
	}
	if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_lark_delivery ON task_lark_deferred_inbox; DROP FUNCTION reject_lark_delivery()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- svc.ReconcileTaskLarkInbox(ctx, in.TaskID) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var runtime uuid.UUID
	if err = s.Pool.QueryRow(ctx, `SELECT count(*),min(runtime_id::text)::uuid FROM task_turns WHERE trigger_inbox_message_id=$1`, message).Scan(&count, &runtime); err != nil || count != 1 || runtime != member.RuntimeID {
		t.Fatal("duplicate or retargeted reminder", count, err)
	}
	msg, err := s.GetInboxMessage(ctx, message, 160)
	if err != nil || msg.ReadAt != nil || msg.Text != "original deferred message" {
		t.Fatal("reminder pretended Agent read or replaced original", err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT delivered_at IS NOT NULL FROM task_lark_deferred_inbox WHERE message_id=$1`, message).Scan(&delivered); err != nil || !delivered {
		t.Fatal("delivery not durable", err)
	}
}

func TestTaskLarkDeferredInboxAfterPublishKeepsRuntimeAndQueue(t *testing.T) {
	s, in, next, turn := larkPublishFixture(t)
	ctx := context.Background()
	svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
	defer svc.Stop(ctx)
	member := in.Members[0]
	message := uuid.New()
	if err := svc.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: member.AgentID, RuntimeID: member.RuntimeID}, Text: "queued during handoff", WhenBusy: "queue"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, turn, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = svc.ReconcileTaskLarkInbox(ctx, in.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	var runtime, node uuid.UUID
	var origin, state string
	if err = s.Pool.QueryRow(ctx, `SELECT runtime_id,node_run_id,origin FROM task_turns WHERE trigger_inbox_message_id=$1`, message).Scan(&runtime, &node, &origin); err != nil || runtime != member.RuntimeID || node != next.ID || origin != "inbox_reminder" {
		t.Fatal("deferred ordinary Inbox lost original runtime", err)
	}
	if err = s.Pool.QueryRow(ctx, `SELECT status FROM task_turns WHERE id=$1`, turn).Scan(&state); err != nil || state != "queued" {
		t.Fatal("queue message interrupted unique continuation", err)
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, in.TaskID).Scan(&count); err != nil || count != 3 {
		t.Fatal("duplicate continuation or reminder", count, err)
	}
	msg, err := s.GetInboxMessage(ctx, message, 160)
	if err != nil || msg.ReadAt != nil || msg.To.RuntimeID != member.RuntimeID {
		t.Fatal("notification became read or fallback", err)
	}
}
