package store_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

type promoteFixture struct {
	t       *testing.T
	s       *store.Store
	svc     *tasks.Service
	calls   *taskInboxInterruptRecorder
	task    uuid.UUID
	node    uuid.UUID
	agent   uuid.UUID
	runtime uuid.UUID
	active  store.TaskTurn
}

func newPromoteFixture(t *testing.T, imageLimit int) *promoteFixture {
	t.Helper()
	ctx := context.Background()
	s := taskConsoleStore(t)
	s.ImageMaxCount = imageLimit
	task, node, agent, initial := taskConsoleRecord(t, s)
	f := &promoteFixture{t: t, s: s, calls: &taskInboxInterruptRecorder{}, task: task, node: node, agent: agent, runtime: initial.RuntimeID}
	f.svc = tasks.New(s, nil, f.calls, tasks.Options{}, quietLogger())
	active, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal("claim", e)
	}
	if e = s.MarkTaskRunning(ctx, active, 1); e != nil {
		t.Fatal(e)
	}
	f.active = active
	return f
}

func (f *promoteFixture) image() uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	data := []byte("synthetic-image-" + id.String())
	if _, e := f.s.SaveMessageImage(context.Background(), id, store.ImageOwner{AgentID: f.agent, TaskID: f.task, NodeRunID: f.node, RuntimeID: f.runtime}, messageimage.Ref{ID: id, MimeType: "image/png", Size: int64(len(data)), Width: 1, Height: 1}, data); e != nil {
		f.t.Fatal(e)
	}
	return id
}

func (f *promoteFixture) send(text, whenBusy string, images ...uuid.UUID) uuid.UUID {
	f.t.Helper()
	raw, e := f.svc.SendNodeInboxMessage(context.Background(), auth.Console(), f.task, f.node, taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime, Text: text, WhenBusy: whenBusy, ImageIDs: images}, 65536)
	if e != nil {
		f.t.Fatal(e)
	}
	return receiptMessage(f.t, inboxReceipt(f.t, raw))
}

func (f *promoteFixture) promote(message, action uuid.UUID) ([]byte, taskmodel.TaskNodeQueueActionReceipt, error) {
	raw, e := f.svc.PromoteNodeInboxMessage(context.Background(), auth.Console(), f.task, f.node, message, taskmodel.TaskNodeQueueActionInput{ActionID: action, ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime})
	var r taskmodel.TaskNodeQueueActionReceipt
	if e == nil {
		if e2 := json.Unmarshal(raw, &r); e2 != nil {
			f.t.Fatal(e2)
		}
	}
	return raw, r, e
}

func (f *promoteFixture) mustPromote(message uuid.UUID) taskmodel.TaskNodeQueueActionReceipt {
	f.t.Helper()
	_, r, e := f.promote(message, uuid.New())
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}

type queueShape struct {
	whenBusy string
	messages []uuid.UUID
	images   int
}

// shape groups the per-message queue items back into the turns that carry them.
func shape(q taskmodel.TaskNodeQueue) []queueShape {
	out := []queueShape{}
	var last uuid.UUID
	for _, it := range q.Items {
		if len(out) == 0 || it.RunID != last {
			out = append(out, queueShape{whenBusy: it.WhenBusy, messages: []uuid.UUID{}})
			last = it.RunID
		}
		g := &out[len(out)-1]
		g.messages = append(g.messages, *it.MessageID)
		g.images += len(it.Images)
	}
	return out
}

func sameShape(a, b []queueShape) bool {
	return slices.EqualFunc(a, b, func(x, y queueShape) bool {
		return x.whenBusy == y.whenBusy && slices.Equal(x.messages, y.messages) && x.images == y.images
	})
}

// queue returns the GET read model and checks that the latest queue event equals it.
func (f *promoteFixture) queue() taskmodel.TaskNodeQueue {
	f.t.Helper()
	q, e := f.svc.NodeQueue(context.Background(), auth.Console(), f.task, f.node)
	if e != nil {
		f.t.Fatal(e)
	}
	var raw []byte
	if e = f.s.Pool.QueryRow(context.Background(), `SELECT data FROM task_events WHERE task_id=$1 AND type='task.node.queue.updated' ORDER BY id DESC LIMIT 1`, f.task).Scan(&raw); e != nil {
		f.t.Fatal("queue event", e)
	}
	var ev taskmodel.TaskNodeQueue
	if e = json.Unmarshal(raw, &ev); e != nil {
		f.t.Fatal(e)
	}
	if !sameShape(shape(q), shape(ev)) || q.RuntimeID != f.runtime || ev.NodeRunID != f.node {
		f.t.Fatalf("the latest task.node.queue.updated does not match the read result: %+v / %+v", ev, q)
	}
	return q
}

func (f *promoteFixture) turn(id uuid.UUID) store.TaskTurn {
	f.t.Helper()
	var status string
	var void *string
	if e := f.s.Pool.QueryRow(context.Background(), `SELECT status,void_reason FROM task_turns WHERE id=$1`, id).Scan(&status, &void); e != nil {
		f.t.Fatal(e)
	}
	return store.TaskTurn{ID: id, Status: status, VoidReason: void}
}

func TestTaskNodeInboxPromoteSplitMergeVoidAndClaimOrder(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	m1, m2, m3 := f.send("one", "queue"), f.send("two", "queue"), f.send("three", "queue")
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"queue", []uuid.UUID{m1, m2, m3}, 0}}) {
		t.Fatalf("three normal messages should be merged into one group: %+v", q)
	}
	normal := q.Items[0].RunID

	// Split one message out of a group: a new interrupt group is created at the front, and it interrupts the current
	// reply.
	r2 := f.mustPromote(m2)
	if r2.Status != "accepted" || r2.RunID == normal || len(f.calls.calls) != 1 || f.calls.calls[0] != [2]uuid.UUID{f.runtime, f.active.ID} {
		t.Fatalf("split out: %+v interrupts %v", r2, f.calls.calls)
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m2}, 0}, {"queue", []uuid.UUID{m1, m3}, 0}}) {
		t.Fatalf("queue after splitting out: %+v", got)
	}
	// Merged into the existing interrupt group; the current reply is already being interrupted, so it is not
	// interrupted again.
	r1 := f.mustPromote(m1)
	if r1.RunID != r2.RunID || len(f.calls.calls) != 1 {
		t.Fatalf("should be merged into the existing interrupt group: %+v %v", r1, f.calls.calls)
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m1, m2}, 0}, {"queue", []uuid.UUID{m3}, 0}}) {
		t.Fatalf("queue after merging: %+v", got)
	}
	// Splitting leaves the group empty: the original group is voided and marked merged_into_interrupt, which the web
	// uses to hide it.
	action := uuid.New()
	raw3, r3, e := f.promote(m3, action)
	if e != nil || r3.RunID != r2.RunID {
		t.Fatal("split that empties the group", e)
	}
	if v := f.turn(normal); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "merged_into_interrupt" {
		t.Fatalf("a group left empty should be voided: %+v", v)
	}
	page, e := f.s.TaskNodeMessages(ctx, f.task, f.node, "", 100)
	if e != nil {
		t.Fatal(e)
	}
	var hidden bool
	for _, m := range page.Messages {
		if m.RunID == normal && m.VoidReason != nil && *m.VoidReason == "merged_into_interrupt" {
			hidden = true
		}
		if m.RunID != normal && m.VoidReason != nil {
			t.Fatalf("other turns should not carry voidReason: %+v", m)
		}
	}
	if !hidden {
		t.Fatal("the TaskMessage of the voided group lacks voidReason")
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m1, m2, m3}, 0}}) {
		t.Fatalf("queue after emptying the group: %+v", got)
	}
	// Replaying the same actionId returns the original response; a message that is already an interrupt returns
	// already_interrupt for a new actionId; neither interrupts again.
	again, _, e := f.promote(m3, action)
	if e != nil || string(again) != string(raw3) {
		t.Fatal("replaying the same action should return the original response", e)
	}
	if _, r, e := f.promote(m3, uuid.New()); e != nil || r.Status != "already_interrupt" || r.RunID != r2.RunID || len(f.calls.calls) != 1 {
		t.Fatalf("an existing interrupt should be idempotent: %+v %v", r, e)
	}
	var whenBusy []string
	rows, e := f.s.Pool.Query(ctx, `SELECT when_busy FROM inbox_messages WHERE id IN (SELECT value FROM json_each($1)) ORDER BY created_at`, []uuid.UUID{m1, m2, m3})
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var w string
		_ = rows.Scan(&w)
		whenBusy = append(whenBusy, w)
	}
	rows.Close()
	var promoted int
	if e = f.s.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.promoted'`, r2.RunID).Scan(&promoted); e != nil {
		t.Fatal(e)
	}
	if !slices.Equal(whenBusy, []string{"interrupt", "interrupt", "interrupt"}) || promoted != 3 {
		t.Fatalf("message whenBusy and run.promoted records: %v %d", whenBusy, promoted)
	}

	// The actual claim order matches the queue list; after the claim the list is emptied and an empty list is pushed.
	if _, e = f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	claimed, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
	if e != nil || !ok || claimed.ID != r2.RunID {
		t.Fatalf("the interrupt group should be claimed first: %v %v", claimed.ID, e)
	}
	if got := f.queue(); len(got.Items) != 0 {
		t.Fatalf("the queue should be empty after the claim: %+v", got)
	}
	// Already claimed: conflict, carrying the turn it is in and its status.
	_, _, e = f.promote(m1, uuid.New())
	ae, isApp := apperr.As(e)
	if !isApp || ae.Code != apperr.CodeTaskConflict {
		t.Fatalf("an already claimed message should conflict: %v", e)
	}
	d := ae.Details.(taskmodel.TaskNodeMessageConflictDetails)
	if d.Reason != "message_dispatched" || d.MessageID == nil || *d.MessageID != m1 || d.RunID == nil || *d.RunID != r2.RunID || d.RunStatus == nil || *d.RunStatus != "starting" {
		t.Fatalf("conflict details: %+v", d)
	}
}

func TestTaskNodeInboxPromoteQueueOrderMatchesClaims(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	a := f.send("a", "queue")
	b := f.send("b", "interrupt") // creates a new interrupt group and interrupts the current reply
	c := f.send("c", "queue")     // merged into the normal group that a is in
	f.mustPromote(a)              // merged into the interrupt group that b is in
	d := f.send("d", "queue")     // merged into the normal group that c is in
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"interrupt", []uuid.UUID{a, b}, 0}, {"queue", []uuid.UUID{c, d}, 0}}) {
		t.Fatalf("queue list: %+v", shape(q))
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	var order []uuid.UUID
	for range shape(q) {
		turn, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
		if e != nil || !ok {
			t.Fatal("claim", e)
		}
		order = append(order, turn.ID)
		if _, e = f.s.UpdateTaskTurn(ctx, f.task, turn.ID, "completed", "", nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	if !slices.Equal(order, []uuid.UUID{q.Items[0].RunID, q.Items[2].RunID}) {
		t.Fatalf("the actual claim order does not match the queue list: %v / %+v", order, q.Items)
	}
}

func TestTaskNodeInboxPromoteImageLimit(t *testing.T) {
	f := newPromoteFixture(t, 2)
	i0 := f.send("interrupt with one image", "interrupt", f.image())
	ma := f.send("normal with two images", "queue", f.image(), f.image())
	mb := f.send("normal with one image", "queue", f.image()) // the normal group already has two images, so another group is opened
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"interrupt", []uuid.UUID{i0}, 1}, {"queue", []uuid.UUID{ma}, 2}, {"queue", []uuid.UUID{mb}, 1}}) {
		t.Fatalf("initial queue: %+v", shape(q))
	}
	// 1+2 exceeds the limit: it cannot be merged into the existing interrupt group, so a new interrupt group is created
	// after it; the original normal group is emptied and voided.
	ra := f.mustPromote(ma)
	if ra.RunID == q.Items[0].RunID {
		t.Fatal("merged into the existing interrupt group despite exceeding the image limit")
	}
	if v := f.turn(q.Items[1].RunID); v.VoidReason == nil {
		t.Fatal("a normal group left empty should be voided")
	}
	// 1+1 does not exceed the limit: merged into the earliest interrupt group.
	rb := f.mustPromote(mb)
	if rb.RunID != q.Items[0].RunID {
		t.Fatal("should be merged into the earliest interrupt group with room left")
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{i0, mb}, 2}, {"interrupt", []uuid.UUID{ma}, 2}}) {
		t.Fatalf("queue under the image limit: %+v", got)
	}
}

func TestTaskNodeInboxPromoteRejects(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	m := f.send("normal", "queue")
	if _, _, e := f.promote(uuid.New(), uuid.New()); !apperr.Is(e, apperr.CodeNotFound) {
		t.Fatalf("a missing message should be 404: %v", e)
	}
	_, e := f.svc.PromoteNodeInboxMessage(ctx, auth.Console(), f.task, f.node, m, taskmodel.TaskNodeQueueActionInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: uuid.New()})
	if ae, ok := apperr.As(e); !ok || ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason != "target_mismatch" {
		t.Fatalf("a target mismatch should conflict: %v", e)
	}
	if _, e = f.s.Pool.Exec(ctx, `UPDATE task_node_inbox_messages SET not_executed_reason='node_changed',not_executed_at=now() WHERE message_id=$1`, m); e != nil {
		t.Fatal(e)
	}
	_, _, e = f.promote(m, uuid.New())
	if ae, ok := apperr.As(e); !ok || ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason != "message_not_executed" {
		t.Fatalf("a message that is no longer valid should conflict: %v", e)
	}
	if len(f.calls.calls) != 0 {
		t.Fatal("a rejected request should not interrupt")
	}
	if _, e = f.svc.PromoteNodeInboxMessage(ctx, auth.Principal{Kind: auth.KindAgent, ID: f.agent}, f.task, f.node, m, taskmodel.TaskNodeQueueActionInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime}); !apperr.Is(e, apperr.CodeForbidden) {
		t.Fatalf("a caller other than the console should get 403: %v", e)
	}
}
