package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/permissions"
	"loop-now/internal/server/store"
)

// permissionEnv is an Agent with a reply in progress and a permissions service asking the console through the Inbox.
type permissionEnv struct {
	*env
	svc   *permissions.Service
	agent apitypes.Agent
	run   uuid.UUID
}

func newPermissionEnv(t *testing.T) *permissionEnv {
	t.Helper()
	e := newEnv(t)
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "asker", "model": map[string]any{"provider": "anthropic", "modelId": "claude-x"}}))
	_, run, err := e.st.InsertMessageAndRun(context.Background(), store.NewRun{AgentID: agent.ID, RunID: uuid.Must(uuid.NewV7()), Text: "deploy", Start: true, InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	svc := &permissions.Service{Store: e.st, Inbox: e.api.Inbox, Poll: 20 * time.Millisecond}
	e.api.Permissions = svc
	return &permissionEnv{env: e, svc: svc, agent: agent, run: run.ID}
}

// ask asks in the background and returns the pending request and where the answer will come.
func (p *permissionEnv) ask(ctx context.Context, title string) (apitypes.PermissionRequest, chan executor.PermissionAnswer) {
	p.t.Helper()
	before := len(decode[apitypes.PermissionRequestList](p.t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/permission-requests", nil)).Requests)
	out := make(chan executor.PermissionAnswer, 1)
	go func() {
		out <- p.svc.AskPermission(ctx, executor.PermissionAsk{AgentID: p.agent.ID, RunID: p.run, Runtime: "claude-code", Kind: "command", Title: title, Detail: "make deploy"})
	}()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		list := decode[apitypes.PermissionRequestList](p.t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/permission-requests?state=pending", nil)).Requests
		if len(list) > before {
			return list[len(list)-1], out
		}
	}
	p.t.Fatal("the request did not appear")
	return apitypes.PermissionRequest{}, nil
}

// question is the Inbox question asking about a request.
func (p *permissionEnv) question(id uuid.UUID) apitypes.InboxMessageSummary {
	p.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		page := decode[struct {
			Messages []apitypes.InboxMessageSummary `json:"messages"`
		}](p.t, p.expect(200, "GET", "/inbox/messages", nil))
		for _, m := range page.Messages {
			if m.Question != nil && m.Question.PermissionRequestID != nil && *m.Question.PermissionRequestID == id {
				return m
			}
		}
	}
	p.t.Fatalf("no Inbox question about %s", id)
	return apitypes.InboxMessageSummary{}
}

func wait(t *testing.T, ch chan executor.PermissionAnswer) executor.PermissionAnswer {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(10 * time.Second):
		t.Fatal("the runtime got no answer")
		return executor.PermissionAnswer{}
	}
}

// A request shows in the Agent's status and as an Inbox question of kind permission. Answered from the chat and from
// the Inbox at once, it is answered once: the other answer gets a conflict carrying the stored outcome, and the runtime
// gets that outcome. A typed Inbox reply rejects it with that reason, and does not remind the Agent.
func TestAnswerOnceFromChatOrInbox(t *testing.T) {
	p := newPermissionEnv(t)
	ctx := context.Background()
	req, answer := p.ask(ctx, "Run make deploy")
	if req.Kind != "command" || req.Title != "Run make deploy" || req.Detail == nil || *req.Detail != "make deploy" || req.RunID != p.run || req.State != "pending" || req.TaskID != nil {
		t.Fatalf("request: %+v", req)
	}
	status := decode[apitypes.AgentStatus](t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/status", nil))
	if len(status.PermissionRequests) != 1 || status.PermissionRequests[0].ID != req.ID {
		t.Fatalf("status: %+v", status.PermissionRequests)
	}
	q := p.question(req.ID)
	if q.Question.Kind != "permission" || len(q.Question.Options) != 2 || q.Question.State != "open" {
		t.Fatalf("question: %+v", q.Question)
	}
	var wg sync.WaitGroup
	codes := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0], _, _ = p.do("POST", "/permission-requests/"+req.ID.String()+"/answer", map[string]any{"decision": "approve"})
	}()
	go func() {
		defer wg.Done()
		codes[1], _, _ = p.do("POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "2", "text": "Reject", "whenBusy": "queue"})
	}()
	wg.Wait()
	if (codes[0] == http.StatusOK) == (codes[1] == http.StatusCreated) {
		t.Fatalf("answered once: chat %d, Inbox %d", codes[0], codes[1])
	}
	got := wait(t, answer)
	if got.Approved != (codes[0] == http.StatusOK) {
		t.Fatalf("the runtime got the winning answer: %+v (chat %d)", got, codes[0])
	}
	code, body, _ := p.do("POST", "/permission-requests/"+req.ID.String()+"/answer", map[string]any{"decision": "approve"})
	var conflict struct {
		Error struct {
			Code    string                                    `json:"code"`
			Details apitypes.PermissionRequestAnsweredDetails `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &conflict); err != nil || code != 409 || conflict.Error.Code != "permission_request_answered" || conflict.Error.Details.Request.ID != req.ID ||
		conflict.Error.Details.Request.State == "pending" || conflict.Error.Details.Request.AnsweredAt == nil {
		t.Fatalf("answering again: %d %s", code, body)
	}

	req, answer = p.ask(ctx, "Run make deploy again")
	q = p.question(req.ID)
	p.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "use the staging target", "whenBusy": "queue"})
	if got := wait(t, answer); got.Approved || got.Reason != "use the staging target" {
		t.Fatalf("typed reply: %+v", got)
	}
	queued, err := p.st.ActiveRun(ctx, p.agent.ID)
	if err != nil || queued == nil || queued.ID != p.run {
		t.Fatalf("the reply goes on: %+v %v", queued, err)
	}
	if page := decode[apitypes.MessagePage](t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/messages", nil)); len(page.Messages) != 1 {
		t.Fatalf("the Inbox answer does not remind the Agent: %d messages", len(page.Messages))
	}
	if list := decode[apitypes.PermissionRequestList](t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/permission-requests", nil)); len(list.Requests) != 0 {
		t.Fatalf("nothing pending: %+v", list.Requests)
	}
}

// Stopping the reply cancels a pending request (reply_stopped): its Inbox question closes, and answering it is a
// conflict.
func TestStopCancelsPendingRequest(t *testing.T) {
	p := newPermissionEnv(t)
	ctx, cancel := context.WithCancelCause(context.Background())
	req, answer := p.ask(ctx, "Run rm -rf build")
	q := p.question(req.ID)
	cancel(executor.CancelReplyStopped)
	if got := wait(t, answer); got.Approved {
		t.Fatalf("a cancelled request is not approved: %+v", got)
	}
	code, body, _ := p.do("POST", "/permission-requests/"+req.ID.String()+"/answer", map[string]any{"decision": "approve"})
	if code != 409 {
		t.Fatalf("answering a cancelled request: %d %s", code, body)
	}
	var conflict struct {
		Error struct {
			Details apitypes.PermissionRequestAnsweredDetails `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &conflict)
	if r := conflict.Error.Details.Request; r.State != "cancelled" || r.Reason == nil || *r.Reason != "reply_stopped" {
		t.Fatalf("stored: %+v", r)
	}
	if q2 := p.question(req.ID); q2.Question.State != "expired" || q.ID != q2.ID {
		t.Fatalf("the question closes: %+v", q2.Question)
	}
	p.expect(404, "POST", "/permission-requests/"+uuid.NewString()+"/answer", map[string]any{"decision": "approve"})
}

// The default mode for new Agents is Full access until someone changes it.
func TestPermissionSettings(t *testing.T) {
	e := newEnv(t)
	if s := decode[apitypes.PermissionSettings](t, e.expect(200, "GET", "/permission-settings", nil)); s.DefaultMode != "full" {
		t.Fatalf("initial: %+v", s)
	}
	if s := decode[apitypes.PermissionSettings](t, e.expect(200, "PUT", "/permission-settings", map[string]any{"defaultMode": "supervised"})); s.DefaultMode != "supervised" {
		t.Fatalf("changed: %+v", s)
	}
}

// The run's timeline lists its permission requests, with how each ended.
func TestRunTimelineShowsPermissionRequests(t *testing.T) {
	p := newPermissionEnv(t)
	req, answer := p.ask(context.Background(), "Run make deploy")
	p.expect(200, "POST", "/permission-requests/"+req.ID.String()+"/answer", map[string]any{"decision": "reject", "reason": "not today"})
	wait(t, answer)
	timeline := decode[struct {
		Entries []map[string]any `json:"entries"`
	}](t, p.expect(200, "GET", "/agents/"+p.agent.ID.String()+"/runs/"+p.run.String()+"/timeline", nil))
	for _, e := range timeline.Entries {
		if e["type"] == "permission_request" {
			if e["requestId"] != req.ID.String() || e["state"] != "rejected" || e["reason"] != "not today" || e["answeredAt"] == nil || e["kind"] != "command" {
				t.Fatalf("entry: %v", e)
			}
			return
		}
	}
	t.Fatalf("no permission entry: %v", timeline.Entries)
}

// Requests a stopped server left pending are cancelled when it starts again (process_exited): no runtime waits for them.
func TestLeftOverRequestsCancelledAtStart(t *testing.T) {
	p := newPermissionEnv(t)
	ctx := context.Background()
	run := p.run
	left, err := p.st.CreatePermissionRequest(ctx, store.PermissionRequest{ID: uuid.Must(uuid.NewV7()), AgentID: p.agent.ID, RunID: &run, Runtime: "codex", Kind: "command", Title: "Run x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.svc.CancelLeftOver(ctx); err != nil {
		t.Fatal(err)
	}
	r, err := p.st.GetPermissionRequest(ctx, left.ID)
	if err != nil || r.State != "cancelled" || r.Reason == nil || *r.Reason != "process_exited" {
		t.Fatalf("left over: %+v %v", r, err)
	}
}
