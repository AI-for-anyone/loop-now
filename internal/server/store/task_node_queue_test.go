package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// deliverFrom sends an ordinary Inbox message from another project Agent to
// the node's task instance (agent#task), as the Inbox service does.
func (f *promoteFixture) deliverFrom(text, whenBusy string) uuid.UUID {
	f.t.Helper()
	ctx := context.Background()
	var other uuid.UUID
	if e := f.s.Pool.QueryRow(ctx, `SELECT id FROM agents WHERE name='outside sender'`).Scan(&other); e != nil {
		var project uuid.UUID
		if e = f.s.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, f.task).Scan(&project); e != nil {
			f.t.Fatal(e)
		}
		a, e := f.s.CreateAgent(ctx, store.ProjectScope(project), "outside sender", "anthropic", "m", "")
		if e != nil {
			f.t.Fatal(e)
		}
		other = a.ID
	}
	id := uuid.Must(uuid.NewV7())
	if e := f.svc.DeliverInbox(ctx, store.NewInboxMessage{ID: id, From: store.AgentParty(other), To: store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}, Text: text, WhenBusy: whenBusy}); e != nil {
		f.t.Fatal(e)
	}
	return id
}

func (f *promoteFixture) input(action uuid.UUID) taskmodel.TaskNodeQueueActionInput {
	return taskmodel.TaskNodeQueueActionInput{ActionID: action, ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime}
}

func (f *promoteFixture) cancel(message, action uuid.UUID) ([]byte, taskmodel.TaskNodeQueueActionReceipt, error) {
	raw, e := f.svc.CancelNodeQueueMessage(context.Background(), auth.Console(), f.task, f.node, message, f.input(action))
	var r taskmodel.TaskNodeQueueActionReceipt
	if e == nil {
		if e2 := json.Unmarshal(raw, &r); e2 != nil {
			f.t.Fatal(e2)
		}
	}
	return raw, r, e
}

func (f *promoteFixture) mustCancel(message uuid.UUID) taskmodel.TaskNodeQueueActionReceipt {
	f.t.Helper()
	_, r, e := f.cancel(message, uuid.New())
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}

func itemMessages(q taskmodel.TaskNodeQueue) []uuid.UUID {
	out := []uuid.UUID{}
	for _, it := range q.Items {
		out = append(out, *it.MessageID)
	}
	return out
}

func conflictReason(t *testing.T, e error) string {
	t.Helper()
	ae, ok := apperr.As(e)
	if !ok || ae.Code != apperr.CodeTaskConflict {
		t.Fatalf("should be task_conflict: %v", e)
	}
	return ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason
}

// Messages other Agents send to a task instance are also listed one by one in the node queue and can be turned into
// interrupts one by one; when claimed, the reminder only names the messages this turn carries.
func TestTaskNodeQueueListsAgentMessagesAndPromotesThem(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	d1 := f.deliverFrom("external one", "queue")
	s1 := f.send("console one", "queue")
	d2 := f.deliverFrom("external two", "queue")
	q := f.queue()
	if !slices.Equal(itemMessages(q), []uuid.UUID{d1, d2, s1}) || q.Items[0].RunID != q.Items[1].RunID || q.Items[0].Text != "external one" || q.Items[2].Kind != "inbox" {
		t.Fatalf("queue list: %+v", q.Items)
	}
	var from map[string]any
	if e := json.Unmarshal(q.Items[0].From, &from); e != nil || from["kind"] != "agent" || from["name"] != "outside sender" {
		t.Fatalf("sender: %s", q.Items[0].From)
	}
	unscoped := q.Items[0].RunID
	r2 := f.mustPromote(d2)
	if r2.Status != "accepted" || r2.Operation != "promote_inbox_message" || r2.Item == nil || r2.Item.Text != "external two" || r2.RunID == unscoped || len(f.calls.calls) != 1 {
		t.Fatalf("promote external message: %+v %v", r2, f.calls.calls)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d2, d1, s1}) {
		t.Fatalf("after promoting: %v", got)
	}
	r1 := f.mustPromote(d1)
	if r1.RunID != r2.RunID {
		t.Fatalf("should be merged into the existing interrupt reminder: %+v", r1)
	}
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "merged_into_interrupt" {
		t.Fatalf("a turn left empty should be voided: %+v", v)
	}
	q = f.queue()
	if !slices.Equal(itemMessages(q), []uuid.UUID{d1, d2, s1}) || q.Items[0].WhenBusy != "interrupt" || q.Items[2].WhenBusy != "queue" {
		t.Fatalf("after merging: %+v", q.Items)
	}
	if r := f.mustPromote(d1); r.Status != "already_interrupt" {
		t.Fatalf("already an interrupt: %+v", r)
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	turn, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
	if e != nil || !ok || turn.ID != r2.RunID {
		t.Fatalf("the interrupt reminder should be claimed first: %v %v", turn.ID, e)
	}
	if !strings.Contains(turn.Text, d1.String()) || !strings.Contains(turn.Text, d2.String()) || strings.Contains(turn.Text, s1.String()) || !strings.Contains(turn.Text, "1 more unread messages") {
		t.Fatalf("the reminder should name only the messages this turn carries: %s", turn.Text)
	}
}

// Cancelling messages in the node queue (sent by the console and by other Agents): they no longer run and do not count
// as unread; a turn left empty is voided.
func TestTaskNodeQueueCancelBothKinds(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	d1 := f.deliverFrom("external one", "queue")
	s1 := f.send("console one", "queue")
	s2 := f.send("console two", "queue")
	q := f.queue()
	unscoped, strict := q.Items[0].RunID, q.Items[1].RunID
	action := uuid.New()
	raw, r, e := f.cancel(s1, action)
	if e != nil || r.Status != "accepted" || r.Operation != "cancel_inbox_message" || r.Item == nil || r.Item.Text != "console one" || r.RunID != strict {
		t.Fatalf("cancel console message: %+v %v", r, e)
	}
	if again, _, e := f.cancel(s1, action); e != nil || !bytes.Equal(again, raw) {
		t.Fatalf("replay of the same actionId: %v", e)
	}
	if r := f.mustCancel(s1); r.Status != "already_cancelled" {
		t.Fatalf("already cancelled: %+v", r)
	}
	var reason *string
	if e := f.s.Pool.QueryRow(ctx, `SELECT not_executed_reason FROM task_node_inbox_messages WHERE message_id=$1`, s1).Scan(&reason); e != nil || reason == nil || *reason != "cancelled" {
		t.Fatalf("a strict message should record not_executed=cancelled: %v %v", reason, e)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d1, s2}) {
		t.Fatalf("after cancelling: %v", got)
	}
	f.mustCancel(s2)
	if v := f.turn(strict); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "user_cancelled" {
		t.Fatalf("a strict turn left empty should be voided: %+v", v)
	}
	if _, e := f.s.Pool.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[{"id":"1","label":"OK"}]',$2)`, d1, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	f.mustCancel(d1)
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "user_cancelled" {
		t.Fatalf("a reminder turn left empty should be voided: %+v", v)
	}
	var state string
	if e := f.s.Pool.QueryRow(ctx, `SELECT state FROM inbox_questions WHERE message_id=$1`, d1).Scan(&state); e != nil || state != "cancelled" {
		t.Fatalf("the question should become cancelled: %q %v", state, e)
	}
	if got := itemMessages(f.queue()); len(got) != 0 {
		t.Fatalf("after cancelling all: %v", got)
	}
	if n, _ := f.s.CountInboxUnread(ctx, store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}); n != 0 {
		t.Fatalf("cancelled messages should not count as unread: %d", n)
	}
	if len(f.calls.calls) != 0 {
		t.Fatal("cancelling should not interrupt the current reply")
	}
	if _, e := f.svc.PromoteNodeInboxMessage(ctx, auth.Console(), f.task, f.node, d1, f.input(uuid.New())); conflictReason(t, e) != "message_cancelled" {
		t.Fatal("a cancelled message must not become an interrupt")
	}
}

// The node Agent reads a queued message during its reply: the message leaves the queue, and its turn, left with no
// pending messages, is voided (no_pending_messages).
func TestTaskNodeQueueReadVoidsTurn(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	s1 := f.send("console one", "queue")
	d1 := f.deliverFrom("external one", "queue")
	q := f.queue()
	strict, unscoped := q.Items[0].RunID, q.Items[1].RunID
	me := store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}
	if _, handled, changed, e := f.s.ReadTaskNodeInbox(ctx, me, s1, f.active.ID); e != nil || !handled || !changed {
		t.Fatalf("read strict message: %v %v %v", handled, changed, e)
	}
	if v := f.turn(strict); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "no_pending_messages" {
		t.Fatalf("the strict turn should be voided after the read: %+v", v)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d1}) {
		t.Fatalf("after the read: %v", got)
	}
	changed, eff, e := f.s.MarkInboxRead(ctx, d1, me)
	if e != nil || !changed || !eff.QueueChanged {
		t.Fatalf("read external message: %v %+v %v", changed, eff, e)
	}
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "no_pending_messages" {
		t.Fatalf("the reminder turn should be voided after the read: %+v", v)
	}
	if got := itemMessages(f.queue()); len(got) != 0 {
		t.Fatalf("after reading all: %v", got)
	}
	if _, e := f.svc.CancelNodeQueueMessage(ctx, auth.Console(), f.task, f.node, d1, f.input(uuid.New())); conflictReason(t, e) != "message_read" {
		t.Fatal("a read message must not be cancellable")
	}
}

// Stopping the node's current turn: set to aborting and the executor is notified; stopping again gives
// already_stopping; an ended turn returns run_not_active.
func TestTaskNodeStopMarksOnlyThisTurn(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	action := uuid.New()
	raw, e := f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(action))
	var r taskmodel.TaskNodeQueueActionReceipt
	if e != nil || json.Unmarshal(raw, &r) != nil || r.Status != "accepted" || r.Operation != "stop_run" || r.RunID != f.active.ID || r.MessageID != nil || r.Item != nil {
		t.Fatalf("stop: %s %v", raw, e)
	}
	if v := f.turn(f.active.ID); v.Status != "aborting" || len(f.calls.calls) != 1 || f.calls.calls[0] != [2]uuid.UUID{f.runtime, f.active.ID} {
		t.Fatalf("should interrupt this turn: %+v %v", v, f.calls.calls)
	}
	if again, e := f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(action)); e != nil || !bytes.Equal(again, raw) {
		t.Fatalf("replay of the same actionId: %v", e)
	}
	raw, e = f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(uuid.New()))
	if e != nil || json.Unmarshal(raw, &r) != nil || r.Status != "already_stopping" || len(f.calls.calls) != 1 {
		t.Fatalf("already stopping: %s %v", raw, e)
	}
	var stopped bool
	if e := f.s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.abort_requested' AND data->'stop'='true')`, f.active.ID).Scan(&stopped); e != nil || !stopped {
		t.Fatalf("the stop should be recorded: %v %v", stopped, e)
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(uuid.New()))
	if conflictReason(t, e) != "run_not_active" {
		t.Fatal("an ended turn must not be stoppable")
	}
}
