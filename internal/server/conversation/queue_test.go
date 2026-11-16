package conversation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// sender returns a second agent of the project that sends Inbox messages.
func (e *env) sender() uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT id FROM agents WHERE name='sender'`).Scan(&id); err == nil {
		return id
	}
	a, err := e.st.GetAgent(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := e.st.CreateAgent(context.Background(), store.ProjectScope(*a.ProjectID), "sender", "anthropic", "m", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return s.ID
}

// deliver sends an Inbox message from the sender agent, as the Inbox service does.
func (e *env) deliver(text, whenBusy string) uuid.UUID {
	e.t.Helper()
	id := uuid.Must(uuid.NewV7())
	msg := store.NewInboxMessage{ID: id, From: store.AgentParty(e.sender()), To: store.AgentParty(e.agent), Text: text, WhenBusy: whenBusy}
	if err := e.svc.DeliverInbox(context.Background(), msg); err != nil {
		e.t.Fatalf("delivering %q failed: %v", text, err)
	}
	return id
}

// queueNames describes the queue: console runs by text, Inbox messages as "inbox:<text>".
func queueNames(items []apitypes.QueueItem) []string {
	out := []string{}
	for _, it := range items {
		name := it.Text
		if it.Kind == "inbox" {
			name = "inbox:" + it.Text
		}
		if it.WhenBusy == "interrupt" {
			name += "!"
		}
		out = append(out, name)
	}
	return out
}

func (e *env) queueNow() []string {
	e.t.Helper()
	q, err := e.svc.Queue(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	return queueNames(q.Items)
}

// queueViews returns GET, snapshot and the newest queue.updated items.
func (e *env) queueViews() (get, snap, event []string) {
	e.t.Helper()
	ctx := context.Background()
	get = e.queueNow()
	s, _, err := e.svc.Snapshot(ctx, e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	snap = queueNames(s.QueueItems)
	for _, m := range e.newestEvents() {
		if m.Type != apitypes.EventQueueUpdated {
			continue
		}
		var ev apitypes.SseQueueUpdated
		if err := json.Unmarshal(m.Data, &ev); err != nil {
			e.t.Fatal(err)
		}
		return get, snap, queueNames(ev.Items)
	}
	return get, snap, nil
}

func (e *env) expectQueue(want ...string) {
	e.t.Helper()
	if want == nil {
		want = []string{}
	}
	get, snap, event := e.queueViews()
	if !slices.Equal(get, want) || !slices.Equal(snap, want) || !slices.Equal(event, want) {
		e.t.Fatalf("queue list should be %v: API %v snapshot %v event %v", want, get, snap, event)
	}
}

type queueAct func(ctx context.Context, agentID, subject, actionID uuid.UUID) (json.RawMessage, error)

// act runs one queue action and decodes the receipt.
func (e *env) act(do queueAct, subject, action uuid.UUID) (apitypes.AgentQueueActionReceipt, json.RawMessage, error) {
	raw, err := do(context.Background(), e.agent, subject, action)
	var r apitypes.AgentQueueActionReceipt
	if err == nil {
		if err := json.Unmarshal(raw, &r); err != nil {
			e.t.Fatal(err)
		}
	}
	return r, raw, err
}

func (e *env) mustAct(do queueAct, subject uuid.UUID) apitypes.AgentQueueActionReceipt {
	e.t.Helper()
	r, _, err := e.act(do, subject, uuid.New())
	if err != nil {
		e.t.Fatalf("queue action failed: %v", err)
	}
	return r
}

func (e *env) runOf(id uuid.UUID) store.Run {
	e.t.Helper()
	r, err := e.st.GetRun(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) carrier(message uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT reminder_run_id FROM inbox_messages WHERE id=$1`, message).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) eventCount(kind string) int {
	e.t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_events WHERE agent_id=$1 AND type=$2`, e.agent, kind).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) executed(substr string) int {
	n := 0
	for _, m := range e.ex.messages() {
		if strings.Contains(m, substr) {
			n++
		}
	}
	return n
}

// queued reports whether the run is still in the database queue.
func (e *env) queued(id uuid.UUID) bool {
	e.t.Helper()
	queued, err := e.st.QueuedRuns(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	return slices.ContainsFunc(queued, func(r store.Run) bool { return r.ID == id })
}

func drainStarted(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func (e *env) read(message uuid.UUID) store.InboxReadEffect {
	e.t.Helper()
	_, eff, err := e.st.MarkInboxRead(context.Background(), message, store.AgentParty(e.agent))
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc.InboxRead(context.Background(), eff)
	return eff
}

// Listed item by item: one item per console message, one item per Inbox message carried by a reminder, in the same
// order as the actual processing order; when the reminder starts it only names the messages it carries, and says the
// other unread messages will be reminded about separately later.
func TestQueueListsEachMessageInProcessingOrder(t *testing.T) {
	e := newEnv(t)
	e.mustSend("hold current", "")
	e.waitStarted("hold current")
	e.mustSend("normal 1", "queue")
	a := e.deliver("mail A", "queue")
	b := e.deliver("mail B", "queue")
	c := e.deliver("mail C", "interrupt")
	e.expectQueue("inbox:mail C!", "normal 1", "inbox:mail A", "inbox:mail B")
	if e.carrier(a) != e.carrier(b) || e.carrier(a) == e.carrier(c) {
		t.Fatal("messages of the same priority should share one reminder, and an interrupt message should get its own")
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	got := e.ex.messages()
	if len(got) != 4 || !strings.Contains(got[1], c.String()) || strings.Contains(got[1], a.String()) || !strings.Contains(got[1], "2 more unread messages") ||
		got[2] != "normal 1" || !strings.Contains(got[3], a.String()) || !strings.Contains(got[3], b.String()) || !strings.Contains(got[3], "sender") {
		t.Fatalf("wrong execution order or reminder naming: %q", got)
	}
	e.expectQueue()
}

// Promoting item by item: the message moves into the interrupt reminder (created if there is none), the original
// reminder is voided if it becomes empty; replaying the same actionId returns the first receipt and has no side
// effects.
func TestPromoteInboxMessageMovesItAndVoidsEmptiedReminder(t *testing.T) {
	e := newEnv(t)
	cur := e.mustSend("hold current", "")
	e.waitStarted("hold current")
	a := e.deliver("mail A", "queue")
	b := e.deliver("mail B", "queue")
	normal := e.carrier(a)
	action := uuid.New()
	r, raw, err := e.act(e.svc.PromoteInboxMessage, b, action)
	if err != nil || r.Status != "accepted" || r.Item == nil || *r.Item.MessageID != b || r.Item.WhenBusy != "queue" || r.Run == nil || r.Operation != apitypes.QueuePromoteInboxMessage {
		t.Fatalf("promote receipt: %+v %v", r, err)
	}
	interrupt := r.Run.ID
	if interrupt == normal || e.carrier(b) != interrupt || e.runOf(normal).Status != "queued" {
		t.Fatal("a new interrupt reminder should carry B, and the original reminder should still carry A")
	}
	e.expectQueue("inbox:mail B!", "inbox:mail A")
	var by string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->>'byRunId' FROM lifecycle_events WHERE run_id=$1 AND type=$2`, cur.Run.ID, store.EventRunAbortRequested).Scan(&by); err != nil || by != interrupt.String() {
		t.Fatalf("the current reply should be interrupted: %q %v", by, err)
	}
	aborts, promoted := e.eventCount(store.EventRunAbortRequested), e.eventCount(store.EventRunPromoted)
	if _, again, err := e.act(e.svc.PromoteInboxMessage, b, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("replaying the same actionId should return the first receipt: %v", err)
	}
	if _, _, err := e.act(e.svc.PromoteInboxMessage, a, action); !apperr.Is(err, apperr.CodeActionConflict) {
		t.Fatalf("the same actionId on another subject should be action_conflict: %v", err)
	}
	if r := e.mustAct(e.svc.PromoteInboxMessage, b); r.Status != "already_interrupt" {
		t.Fatalf("already an interrupt: %+v", r)
	}
	if e.eventCount(store.EventRunAbortRequested) != aborts || e.eventCount(store.EventRunPromoted) != promoted {
		t.Fatal("a replay or already_interrupt should not interrupt again or record a promotion")
	}
	if r := e.mustAct(e.svc.PromoteInboxMessage, a); r.Run.ID != interrupt {
		t.Fatalf("A should join the existing interrupt reminder: %+v", r)
	}
	if v := e.runOf(normal); v.Status != "interrupted" || v.EndReason != "merged_into_interrupt" || e.queued(normal) {
		t.Fatalf("the emptied reminder should be voided and removed from Redis: %s %s", v.Status, v.EndReason)
	}
	e.expectQueue("inbox:mail A!", "inbox:mail B!")
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 2 || !strings.Contains(got[1], a.String()) || !strings.Contains(got[1], b.String()) {
		t.Fatalf("the interrupt reminder should name A and B and run only once: %q", got)
	}
	var promotedMsg string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->>'messageId' FROM lifecycle_events WHERE run_id=$1 AND type=$2 ORDER BY at LIMIT 1`, interrupt, store.EventRunPromoted).Scan(&promotedMsg); err != nil || promotedMsg != b.String() {
		t.Fatalf("the promoted event should carry messageId: %q %v", promotedMsg, err)
	}
	_, _, err = e.act(e.svc.PromoteInboxMessage, a, uuid.New())
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued || ae.Details.(store.RunNotQueuedDetails).MessageState == "" {
		t.Fatalf("an already handled message should be run_not_queued: %v", err)
	}
}

// Withdrawing a console message and cancelling an Inbox message: the Agent will not receive them; the receipt carries
// the original text; a carrying reminder that becomes empty is voided; cancelled messages do not count as unread.
func TestCancelQueuedRunAndInboxMessage(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.mustSend("hold current", "")
	e.waitStarted("hold current")
	mine := e.mustSend("withdraw me", "queue")
	action := uuid.New()
	r, raw, err := e.act(e.svc.CancelRun, mine.Run.ID, action)
	if err != nil || r.Status != "accepted" || r.Item == nil || r.Item.Text != "withdraw me" || r.Item.Kind != "run" || r.Run.Status != "interrupted" || r.Run.EndReason == nil || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("withdraw receipt: %+v %v", r, err)
	}
	if e.queued(mine.Run.ID) {
		t.Fatal("the withdrawn run should be removed from the Redis queue")
	}
	if _, again, err := e.act(e.svc.CancelRun, mine.Run.ID, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("a replay should return the first receipt: %v", err)
	}
	if r := e.mustAct(e.svc.CancelRun, mine.Run.ID); r.Status != "already_cancelled" {
		t.Fatalf("already withdrawn: %+v", r)
	}
	a := e.deliver("mail A", "queue")
	b := e.deliver("mail B", "queue")
	rem := e.carrier(a)
	if r := e.mustAct(e.svc.CancelInboxMessage, a); r.Status != "accepted" || r.Item.Text != "mail A" || r.Run.Status != "queued" {
		t.Fatalf("cancel A: %+v", r)
	}
	if n, _ := e.st.CountInboxUnread(ctx, store.AgentParty(e.agent)); n != 1 {
		t.Fatalf("cancelled messages should not count as unread: %d", n)
	}
	e.expectQueue("inbox:mail B")
	if r := e.mustAct(e.svc.CancelInboxMessage, b); r.Run.Status != "interrupted" || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("the reminder should be voided after cancelling the last message: %+v", r.Run)
	}
	if r := e.mustAct(e.svc.CancelInboxMessage, b); r.Status != "already_cancelled" {
		t.Fatalf("already cancelled: %+v", r)
	}
	e.expectQueue()
	if e.queued(rem) || e.eventCount(store.EventMessageCancelled) != 2 {
		t.Fatal("the voided reminder should leave Redis, and each cancel should record one event")
	}
	m, err := e.st.GetInboxMessage(ctx, a, 200)
	if err != nil || m.CancelledAt == nil || m.ReadAt != nil {
		t.Fatalf("the message should be recorded as cancelled: %+v %v", m, err)
	}
	store.RedactCancelledFor(store.AgentParty(e.agent), &m)
	if m.Text != store.CancelledNotice || m.Preview != store.CancelledNotice {
		t.Fatalf("the recipient Agent should only see the cancellation notice: %q", m.Text)
	}
	full, _ := e.st.GetInboxMessage(ctx, a, 200)
	store.RedactCancelledFor(store.AgentParty(e.sender()), &full)
	if full.Text != "mail A" {
		t.Fatal("the sender should see the full text")
	}
	if changed, _, err := e.st.MarkInboxRead(ctx, a, store.AgentParty(e.agent)); err != nil || changed {
		t.Fatalf("a cancelled message should not become read: %v %v", changed, err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 1 {
		t.Fatalf("withdrawn and cancelled messages should not run: %q", got)
	}
}

// Leaving the queue on read: messages read by the recipient Agent leave the list immediately; a reminder with no
// pending messages left is voided right then, instead of being skipped later when it starts.
func TestReadLeavesQueueAndVoidsReminder(t *testing.T) {
	e := newEnv(t)
	e.mustSend("hold current", "")
	e.waitStarted("hold current")
	a := e.deliver("mail A", "queue")
	b := e.deliver("mail B", "queue")
	rem := e.carrier(a)
	if eff := e.read(a); !eff.QueueChanged || len(eff.Voided) != 0 {
		t.Fatalf("read A: %+v", eff)
	}
	e.expectQueue("inbox:mail B")
	if eff := e.read(b); !eff.QueueChanged || !slices.Equal(eff.Voided, []uuid.UUID{rem}) {
		t.Fatalf("reading the last message should void the reminder: %+v", eff)
	}
	if r := e.runOf(rem); r.Status != "interrupted" || r.EndReason != "no_pending_messages" || e.queued(rem) {
		t.Fatalf("voided reminder: %s %s", r.Status, r.EndReason)
	}
	e.expectQueue()
	// Fallback: marked as read directly, bypassing the read path; when the reminder starts it finds no pending messages
	// and is voided without starting Pi.
	c := e.deliver("mail C", "queue")
	late := e.carrier(c)
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE inbox_messages SET read_at=now() WHERE id=$1`, c); err != nil {
		t.Fatal(err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if r := e.runOf(late); r.Status != "interrupted" || r.EndReason != "no_pending_messages" || len(e.ex.messages()) != 1 {
		t.Fatalf("a reminder with no pending messages when it starts should be voided: %s %s %q", r.Status, r.EndReason, e.ex.messages())
	}
}

// Stop only affects this one reply: it ends with user_stopped and queued messages after it are processed as usual; when
// the run has already ended it returns run_not_active.
func TestStopOnlyStopsThisReplyAndQueueContinues(t *testing.T) {
	e := newEnv(t)
	cur := e.mustSend("block current", "")
	e.waitStarted("block current")
	next := e.mustSend("normal 1", "queue")
	if _, _, err := e.act(e.svc.StopRun, next.Run.ID, uuid.New()); !apperr.Is(err, apperr.CodeRunNotActive) {
		t.Fatalf("a queued run cannot be stopped: %v", err)
	}
	action := uuid.New()
	r, raw, err := e.act(e.svc.StopRun, cur.Run.ID, action)
	if err != nil || r.Status != "accepted" || r.Item != nil || r.Run.ID != cur.Run.ID {
		t.Fatalf("stop receipt: %+v %v", r, err)
	}
	e.waitIdleOrDump(names{})
	if v := e.runOf(cur.Run.ID); v.Status != "interrupted" || v.EndReason != "user_stopped" {
		t.Fatalf("stopped reply: %s %s", v.Status, v.EndReason)
	}
	if v := e.runOf(next.Run.ID); v.Status != "completed" || e.executed("normal 1") != 1 {
		t.Fatalf("the queue should continue after a stop: %s", v.Status)
	}
	if _, again, err := e.act(e.svc.StopRun, cur.Run.ID, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("a replay should return the first receipt: %v", err)
	}
	_, _, err = e.act(e.svc.StopRun, cur.Run.ID, uuid.New())
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotActive || ae.Details.(store.RunNotActiveDetails).Status != "interrupted" {
		t.Fatalf("an ended reply should be run_not_active: %v", err)
	}
	var stop string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->'stop' FROM lifecycle_events WHERE run_id=$1 AND type=$2`, cur.Run.ID, store.EventRunAbortRequested).Scan(&stop); err != nil || stop != "true" {
		t.Fatalf("abort_requested should carry stop: %q %v", stop, err)
	}
}

// Withdrawing racing with dequeue: the same message is either withdrawn and never run, or already started (withdrawing
// gets run_not_queued) and run exactly once.
func TestCancelRacesDequeueExactlyOnce(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block current" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		q := e.mustSend("queued"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var cancelErr error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, cancelErr = e.act(e.svc.CancelRun, q.Run.ID, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		r := e.runOf(q.Run.ID)
		ran := e.executed("queued" + string(rune('a'+i)))
		switch {
		case cancelErr == nil && r.EndReason == "user_cancelled" && ran == 0:
		case apperr.Is(cancelErr, apperr.CodeRunNotQueued) && r.Status == "completed" && ran == 1:
		default:
			t.Fatalf("round %d: withdraw %v, status %s/%s, executed %d times", i, cancelErr, r.Status, r.EndReason, ran)
		}
	}
}

// Promoting an item racing with the reminder starting: the message is handled exactly once (named by the original
// reminder or by the interrupt reminder), never lost or duplicated.
func TestPromoteMessageRacesReminderStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block current" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		m := e.deliver("mail"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var err error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, err = e.act(e.svc.PromoteInboxMessage, m, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		if err != nil && !apperr.Is(err, apperr.CodeRunNotQueued) {
			t.Fatalf("round %d: %v", i, err)
		}
		if n := e.executed(m.String()); n != 1 {
			t.Fatalf("round %d: the message was named in %d runs (promote %v)", i, n, err)
		}
		var open int
		if e2 := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND status='queued'`, e.agent).Scan(&open); e2 != nil || open != 0 {
			t.Fatalf("round %d: queued runs remain: %d", i, open)
		}
		// Handled in this round; mark it read so the next round's reminder does not count it.
		e.read(m)
	}
}

// Cancelling racing with the recipient Agent reading: exactly one side takes effect, and a message is never both read
// and cancelled.
func TestCancelRacesRead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.mustSend("hold current", "")
	e.waitStarted("hold current")
	for i := range 20 {
		m := e.deliver("mail"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var cancelErr, readErr error
		var readChanged bool
		wg.Go(func() { _, _, cancelErr = e.act(e.svc.CancelInboxMessage, m, uuid.New()) })
		wg.Go(func() {
			var eff store.InboxReadEffect
			readChanged, eff, readErr = e.st.MarkInboxRead(ctx, m, store.AgentParty(e.agent))
			e.svc.InboxRead(ctx, eff)
		})
		wg.Wait()
		if readErr != nil {
			t.Fatal(readErr)
		}
		v, err := e.st.GetInboxMessage(ctx, m, 200)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case cancelErr == nil && !readChanged && v.CancelledAt != nil && v.ReadAt == nil:
		case apperr.Is(cancelErr, apperr.CodeRunNotQueued) && readChanged && v.ReadAt != nil && v.CancelledAt == nil:
		default:
			t.Fatalf("round %d: cancel %v read %v, message read=%v cancelled=%v", i, cancelErr, readChanged, v.ReadAt, v.CancelledAt)
		}
	}
	e.expectQueue()
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 1 {
		t.Fatalf("all messages are read or cancelled, so the reminder should not run: %q", got)
	}
}

// Voiding on read racing with the reminder starting: the reminder is either voided on read, or voided after starting
// because there are no pending messages, or runs once as usual; it never gets stuck in the queue.
func TestReadVoidRacesReminderStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block current" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		m := e.deliver("mail"+string(rune('a'+i)), "queue")
		rem := e.carrier(m)
		var wg sync.WaitGroup
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { e.read(m) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		r := e.runOf(rem)
		ran := e.executed(m.String())
		switch {
		case r.Status == "interrupted" && r.EndReason == "no_pending_messages" && ran == 0:
		case r.Status == "completed" && ran == 1:
		default:
			t.Fatalf("round %d: reminder %s/%s, executed %d times", i, r.Status, r.EndReason, ran)
		}
	}
}

// Stopping racing with the reply ending and the next one starting: the stop only affects the run in the path, and the
// next one is never stopped.
func TestStopRacesFinishAndNextStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := e.mustSend("block current"+string(rune('a'+i)), "queue")
		e.waitStarted("block current" + string(rune('a'+i)))
		next := e.mustSend("next"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var err error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, err = e.act(e.svc.StopRun, cur.Run.ID, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		c, n := e.runOf(cur.Run.ID), e.runOf(next.Run.ID)
		if n.Status != "completed" || n.AbortRequestedAt != nil {
			t.Fatalf("round %d: the next run should not be stopped: %s", i, n.Status)
		}
		switch {
		case err == nil && (c.EndReason == "user_stopped" || c.Status == "completed"):
		case apperr.Is(err, apperr.CodeRunNotActive) && c.Status == "completed":
		default:
			t.Fatalf("round %d: stop %v, current reply %s/%s", i, err, c.Status, c.EndReason)
		}
	}
}

// The recipient Agent reading the Inbox during a reply, racing with runs ending and starting: the read path locks the
// Agent row first, in the same lock order as FinishRun, so it cannot deadlock.
func TestReadsConcurrentWithFinishRun(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	for round := range 5 {
		cur := "block current" + string(rune('a'+round))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		msgs := []uuid.UUID{}
		for i := range 8 {
			msgs = append(msgs, e.deliver("mail"+string(rune('a'+round))+string(rune('0'+i)), "queue"))
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(msgs)+2)
		for _, m := range msgs {
			wg.Go(func() {
				_, eff, err := e.st.MarkInboxRead(ctx, m, store.AgentParty(e.agent))
				e.svc.InboxRead(ctx, eff)
				errs <- err
			})
		}
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() {
			_, err := e.send("concurrent"+string(rune('a'+round)), "queue")
			errs <- err
		})
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		var unread int
		if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE to_agent_id=$1 AND read_at IS NULL`, e.agent).Scan(&unread); err != nil || unread != 0 {
			t.Fatalf("round %d: every read should take effect: %d %v", round, unread, err)
		}
	}
}
