package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

// agentCall sends one request to the Agent API with a freshly issued token of agentID.
func (s *server) agentCall(agentID uuid.UUID, method, path string, body any) (int, []byte) {
	s.t.Helper()
	token, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		s.t.Fatal(err)
	}
	if err = s.a.Store.InsertAgentToken(context.Background(), token.ID, agentID, s.a.InstanceID, token.Hash); err != nil {
		s.t.Fatal(err)
	}
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimSuffix(s.url, "/api/v1")+"/agent/v1"+path, r)
	req.Header.Set("Authorization", "Bearer "+token.Plain)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func queueKinds(items []apitypes.QueueItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Kind+":"+it.Text+":"+it.WhenBusy)
	}
	return out
}

// The reworked queue for regular conversations, real App + fakePi: per-item list and snapshot, cancel (the receiving
// Agent sees only a cancellation note, the sender and the console see the full text), converting a single item to an
// interrupt, and the queue continuing after the current reply is stopped.
func TestQueueActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	path, receiver := s.createAgent("Receiver")
	_, sender := s.createAgent("Sender")
	send := func(text, whenBusy string) apitypes.Run {
		body := map[string]any{"text": text}
		if whenBusy != "" {
			body["whenBusy"] = whenBusy
		}
		return decode[apitypes.SendMessageResult](t, s.must(202, "POST", path+"/messages", body)).Run
	}
	first := send("!sleep=30s !noabort !text=first", "")
	waitFor(t, "first message running", func() bool {
		r, e := s.a.Store.GetRun(ctx, first.ID)
		return e == nil && r.Status == "running"
	})
	mine := send("!text=mine", "queue")
	inbox := func(text string) uuid.UUID {
		code, raw := s.agentCall(sender.ID, "POST", "/inbox/messages", map[string]any{"to": "Default project/Receiver", "text": text, "whenBusy": "queue"})
		if code != 201 {
			t.Fatalf("Sender sends: %d %s", code, raw)
		}
		return decode[apitypes.InboxMessage](t, raw).ID
	}
	m1, m2 := inbox("please check the build results"), inbox("second note")
	q := decode[apitypes.AgentQueue](t, s.must(200, "GET", path+"/queue", nil))
	want := []string{"run:!text=mine:queue", "inbox:please check the build results:queue", "inbox:second note:queue"}
	if !slices.Equal(queueKinds(q.Items), want) || q.Items[1].From.Kind != "agent" || q.Items[1].From.ID == nil || *q.Items[1].From.ID != sender.ID || q.Items[0].From.Kind != "console" {
		t.Fatalf("queue list: %+v", q.Items)
	}
	var snap apitypes.SseSnapshot
	firstSSESnapshot(t, s.url+path+"/events", &snap)
	if !slices.Equal(queueKinds(snap.QueueItems), want) {
		t.Fatalf("per-item list in the SSE snapshot: %+v", snap.QueueItems)
	}

	// Cancel a message sent by someone else: the receiving Agent sees only a cancellation note, the sender and the
	// console see the full text; the Inbox event stream pushes the cancellation.
	r := decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/inbox-messages/"+m2.String()+"/cancel", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Item == nil || r.Item.Text != "second note" || r.Operation != "cancel_inbox_message" {
		t.Fatalf("cancel receipt: %+v", r)
	}
	code, raw := s.agentCall(receiver.ID, "POST", "/inbox/messages/"+m2.String()+"/read", map[string]any{})
	got := decode[apitypes.InboxMessage](t, raw)
	if code != 200 || got.Text != store.CancelledNotice || got.CancelledAt == nil || got.ReadAt != nil || strings.Contains(string(raw), "second note") {
		t.Fatalf("the receiving Agent reads the cancelled message: %d %s", code, raw)
	}
	code, raw = s.agentCall(receiver.ID, "GET", "/inbox/messages?unread=true", nil)
	if code != 200 || strings.Contains(string(raw), m2.String()) || !strings.Contains(string(raw), m1.String()) {
		t.Fatalf("a cancelled message should not count as unread: %d %s", code, raw)
	}
	code, raw = s.agentCall(sender.ID, "GET", "/inbox/threads/"+m2.String(), nil)
	if code != 200 || !strings.Contains(string(raw), "second note") || !strings.Contains(string(raw), `"cancelledAt":"`) {
		t.Fatalf("the sender should see the full text and the cancel time: %d %s", code, raw)
	}
	if console := decode[apitypes.InboxMessage](t, s.must(200, "GET", path+"/inbox/"+m2.String(), nil)); console.Text != "second note" || console.CancelledAt == nil {
		t.Fatalf("the console should see the full text: %+v", console)
	}
	events, err := s.a.Coord.ReadInbox(ctx, "0", 1000, 0)
	if err != nil || !slices.ContainsFunc(events, func(e coord.Event) bool {
		return e.Type == apitypes.EventInboxCancelled && strings.Contains(string(e.Data), m2.String())
	}) {
		t.Fatalf("inbox.message.cancelled should be pushed: %v", err)
	}

	// Convert a single item to an interrupt: the message from someone else is handled before the Agent's own queued
	// message, and the current reply is interrupted.
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/inbox-messages/"+m1.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Run == nil || r.Run.WhenBusy == nil || *r.Run.WhenBusy != "interrupt" || !slices.Equal(queueKinds(r.Queue), []string{"inbox:please check the build results:interrupt", "run:!text=mine:queue"}) {
		t.Fatalf("promote receipt: %+v", r)
	}
	promoted := r.Run.ID
	waitFor(t, "all runs ended", func() bool {
		for _, id := range []uuid.UUID{first.ID, promoted, mine.ID} {
			if v, e := s.a.Store.GetRun(ctx, id); e != nil || v.EndedAt == nil {
				return false
			}
		}
		return true
	})
	a, p, m := runOf(t, s, first.ID), runOf(t, s, promoted), runOf(t, s, mine.ID)
	if a.EndReason != "user_interrupt" || p.Status != "completed" || m.Status != "completed" || !p.StartedAt.Before(*m.StartedAt) {
		t.Fatalf("the promoted message should run before the queued console message: first=%s promoted=%s@%v mine=%s@%v", a.EndReason, p.Status, p.StartedAt, m.Status, m.StartedAt)
	}
	var prompt string
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT text FROM conversation_messages WHERE id=$1`, p.UserMessageID).Scan(&prompt); err != nil || !strings.Contains(prompt, m1.String()) || strings.Contains(prompt, m2.String()) {
		t.Fatalf("the interrupt reminder should name only the promoted message: %q %v", prompt, err)
	}
	if q := decode[apitypes.AgentQueue](t, s.must(200, "GET", path+"/queue", nil)); len(q.Items) != 0 {
		t.Fatalf("the queue should be empty: %+v", q.Items)
	}

	// Stop the current reply: it ends as user_stopped and queued messages are handled as usual afterwards; a reply that
	// has ended cannot be stopped again.
	long := send("!sleep=30s !noabort !text=long", "")
	waitFor(t, "long reply running", func() bool {
		v, e := s.a.Store.GetRun(ctx, long.ID)
		return e == nil && v.Status == "running"
	})
	after := send("!text=after", "queue")
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/runs/"+long.ID.String()+"/stop", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Run.ID != long.ID {
		t.Fatalf("stop receipt: %+v", r)
	}
	waitFor(t, "queue continues after stop", func() bool {
		v, e := s.a.Store.GetRun(ctx, after.ID)
		return e == nil && v.Status == "completed"
	})
	if v := runOf(t, s, long.ID); v.Status != "interrupted" || v.EndReason != "user_stopped" {
		t.Fatalf("stopped reply: %s %s", v.Status, v.EndReason)
	}
	if code, body := s.call("POST", path+"/runs/"+long.ID.String()+"/stop", map[string]any{"actionId": uuid.New()}); code != 409 || !strings.Contains(string(body), "run_not_active") {
		t.Fatalf("an ended reply should get 409 run_not_active: %d %s", code, body)
	}
	tl := s.must(200, "GET", path+"/runs/"+long.ID.String()+"/timeline", nil)
	if !bytes.Contains(tl, []byte(`"stop":true`)) || !bytes.Contains(tl, []byte(`"user_stopped"`)) {
		t.Fatalf("the timeline should record the stop: %s", tl)
	}
	// Withdraw a message you queued yourself: the Agent never receives it and it is removed from the queue.
	busy := send("!sleep=30s !noabort !text=busy", "")
	waitFor(t, "running again", func() bool {
		v, e := s.a.Store.GetRun(ctx, busy.ID)
		return e == nil && v.Status == "running"
	})
	withdraw := send("!text=withdraw", "queue")
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/runs/"+withdraw.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Item.Text != "!text=withdraw" || r.Run.EndReason == nil || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("withdraw receipt: %+v", r)
	}
	s.must(200, "POST", path+"/runs/"+busy.ID.String()+"/stop", map[string]any{"actionId": uuid.New()})
	s.waitIdle(path)
	if v := runOf(t, s, withdraw.ID); v.StartedAt != nil || v.EndReason != "user_cancelled" {
		t.Fatalf("a withdrawn message should not run: %+v", v)
	}
}

func runOf(t *testing.T, s *server, id uuid.UUID) store.Run {
	t.Helper()
	r, err := s.a.Store.GetRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Node stop, real App + fakePi: with queued items the node goes on to the next one; without queued items the node
// becomes blocked (stopped by a person).
func TestQueueNodeStopActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	_, manager := s.createAgent("Manager")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	start := func(name string) (taskmodel.Detail, apitypes.Agent, uuid.UUID) {
		_, worker := s.createAgent(name)
		raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: name, Request: map[string]any{},
			Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=30s !noabort !text=working", AgentID: worker.ID,
				InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
		if err != nil {
			t.Fatal(err)
		}
		task := decode[taskmodel.Detail](t, raw)
		var active uuid.UUID
		waitFor(t, name+" node running", func() bool {
			return s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, task.Nodes[0].RuntimeID).Scan(&active) == nil
		})
		return task, worker, active
	}
	body := func(worker apitypes.Agent, task taskmodel.Detail) map[string]any {
		return map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": task.Nodes[0].RuntimeID}
	}
	turnState := func(id uuid.UUID) (string, string) {
		var status, code string
		if err := s.a.Store.Pool.QueryRow(ctx, `SELECT status,coalesce(error_code,'') FROM task_turns WHERE id=$1`, id).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		return status, code
	}

	// No queued items: after the stop the node becomes blocked.
	task, worker, active := start("Alone")
	base := "/tasks/" + task.Task.ID.String() + "/nodes/" + task.Nodes[0].ID.String()
	r := decode[taskmodel.TaskNodeQueueActionReceipt](t, s.must(200, "POST", base+"/runs/"+active.String()+"/stop", body(worker, task)))
	if r.Status != "accepted" || r.RunID != active {
		t.Fatalf("stop receipt: %+v", r)
	}
	waitFor(t, "node blocked", func() bool {
		d, e := s.a.Tasks.Get(ctx, auth.Console(), task.Task.ID)
		return e == nil && d.Nodes[0].Status == "blocked"
	})
	if st, code := turnState(active); st != "interrupted" || code != "user_stopped" {
		t.Fatalf("stopped turn: %s %s", st, code)
	}
	d, _ := s.a.Tasks.Get(ctx, auth.Console(), task.Task.ID)
	if d.Task.Reason == nil || !strings.Contains(*d.Task.Reason, "Stopped by a person.") {
		t.Fatalf("block reason: %+v", d.Task.Reason)
	}
	if code, raw := s.call("POST", base+"/runs/"+active.String()+"/stop", body(worker, task)); code != 409 {
		t.Fatalf("an ended turn cannot be stopped: %d %s", code, raw)
	}

	// Queued items: after the stop the node goes on to the next one.
	task, worker, active = start("Busy")
	base = "/tasks/" + task.Task.ID.String() + "/nodes/" + task.Nodes[0].ID.String()
	s.must(201, "POST", base+"/inbox-messages", map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": task.Nodes[0].RuntimeID, "text": "!text=next", "whenBusy": "queue"})
	q := decode[taskmodel.TaskNodeQueue](t, s.must(200, "GET", base+"/queue", nil))
	if len(q.Items) != 1 || q.Items[0].Text != "!text=next" {
		t.Fatalf("node queue: %+v", q.Items)
	}
	next := q.Items[0].RunID
	s.must(200, "POST", base+"/runs/"+active.String()+"/stop", body(worker, task))
	waitFor(t, "next item claimed after stop", func() bool {
		var started bool
		return s.a.Store.Pool.QueryRow(ctx, `SELECT started_at IS NOT NULL FROM task_turns WHERE id=$1`, next).Scan(&started) == nil && started
	})
	if st, code := turnState(active); st != "interrupted" || code != "user_stopped" {
		t.Fatalf("stopped turn: %s %s", st, code)
	}
}
