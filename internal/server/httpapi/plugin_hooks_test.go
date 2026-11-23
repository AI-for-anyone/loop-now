package httpapi_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

// fakeHooks records synchronous Hook calls and returns preset results by event type.
type fakeHooks struct {
	mu       sync.Mutex
	calls    []hookCall
	outcomes map[string]pluginhost.Outcome
}

type hookCall struct {
	kind    pluginhost.Kind
	event   map[string]any
	project *uuid.UUID
}

func (f *fakeHooks) Hook(_ context.Context, kind pluginhost.Kind, event map[string]any, project *uuid.UUID) (pluginhost.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, hookCall{kind, event, project})
	return f.outcomes[event["type"].(string)], nil
}

func (f *fakeHooks) set(typ string, o pluginhost.Outcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[typ] = o
}

// last returns the most recent call for an event type.
func (f *fakeHooks) last(t *testing.T, typ string) hookCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].event["type"] == typ {
			return f.calls[i]
		}
	}
	t.Fatalf("no call to %s", typ)
	return hookCall{}
}

func (f *fakeHooks) count(typ string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.event["type"] == typ {
			n++
		}
	}
	return n
}

// Console messages: those starting with / go to the claim Hook console.command first; when claimed they are not sent to
// the Agent and the plugin's reply is returned; the rest are rewritten through message.before_enqueue. Inbox:
// inbox.before_send can block; messages sent by Agents are rewritten through inbox.before_deliver.
// sleep.before_schedule can change the duration. Every event carries the fixed fields and the Agent's project.
func TestPluginHookCallSites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hooks := &fakeHooks{outcomes: map[string]pluginhost.Outcome{}}
	e.api.Hooks = hooks
	project := e.dc.Project.ID
	agent, path := e.newAgent("Assistant")

	hooks.set("console.command", pluginhost.Outcome{Handled: true, Plugin: "bot", Value: json.RawMessage(`{"handled":true,"reply":"pong"}`)})
	res := decode[map[string]any](t, e.expect(200, "POST", path+"/messages", map[string]any{"text": "/ping now"}))
	if res["handled"] != true || res["plugin"] != "bot" || res["reply"] != "pong" {
		t.Fatalf("claim: %v", res)
	}
	cmd := hooks.last(t, "console.command")
	if cmd.kind != pluginhost.Claim || cmd.event["command"] != "ping" || *cmd.project != project || cmd.event["agentId"].(*uuid.UUID).String() != agent.ID.String() || len(cmd.event["id"].(string)) != 26 {
		t.Fatalf("console.command event: %+v", cmd)
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("a claimed command should not be saved: %+v", page)
	}

	hooks.set("console.command", pluginhost.Outcome{})
	hooks.set("message.before_enqueue", pluginhost.Outcome{Patch: map[string]any{"text": "rewritten message"}})
	sent := decode[apitypes.SendMessageResult](t, e.expect(202, "POST", path+"/messages", map[string]any{"text": "/unknown original text"}))
	if sent.Message.Text != "rewritten message" || hooks.last(t, "message.before_enqueue").event["text"] != "/unknown original text" {
		t.Fatalf("rewrite: %+v", sent.Message)
	}
	e.waitAgentIdle(path)

	hooks.set("inbox.before_send", pluginhost.Outcome{Blocked: true, Plugin: "guard", Reason: "outside working hours"})
	e.expectError(409, "plugin_blocked", "POST", "/inbox/messages", map[string]any{"to": "Default project/Assistant", "text": "are you there"})
	if c := hooks.last(t, "inbox.before_send"); c.project == nil || *c.project != project || c.event["to"] != "Default project/Assistant" {
		t.Fatalf("the console sending to an Agent should use the recipient Agent's project: %+v", c)
	}
	hooks.set("inbox.before_send", pluginhost.Outcome{})
	hooks.set("inbox.before_deliver", pluginhost.Outcome{Patch: map[string]any{"text": "[reviewed] Hello"}})
	other, _ := e.newAgent("Colleague")
	msg := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages", map[string]any{"to": "Default project/Colleague", "text": "Hello"}))
	if msg.Text != "[reviewed] Hello" {
		t.Fatalf("messages sent by an Agent should be rewritten: %+v", msg)
	}
	_ = other

	platform := e.platformAgent("Sleeper")
	_, run, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: platform.ID, RunID: uuid.New(), Text: "source", Start: true, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.st.SetRunRunning(ctx, run.ID, 1); err != nil {
		t.Fatal(err)
	}
	hooks.set("sleep.before_schedule", pluginhost.Outcome{Patch: map[string]any{"durationSeconds": float64(900)}})
	got := decode[sleepmodel.Receipt](t, e.agentCaller(platform.ID).expect(202, "POST", "/sleep", map[string]any{"actionId": uuid.New(), "runId": run.ID, "seconds": 120, "reason": nil}))
	if got.Sleep.Seconds != 900 {
		t.Fatalf("the sleep duration should be rewritten: %+v", got.Sleep)
	}
	if c := hooks.last(t, "sleep.before_schedule"); c.project != nil || c.event["durationSeconds"] != int64(120) {
		t.Fatalf("sleep event of a platform Agent: %+v", c)
	}
}

// Intercepting before task creation and node submission: 409 plugin_blocked when blocked; a replay with the same action
// ID returns the original result directly without asking plugins again.
func TestPluginHookTaskCallSites(t *testing.T) {
	e, manager, d := taskEnv(t)
	hooks := &fakeHooks{outcomes: map[string]pluginhost.Outcome{}}
	e.api.Hooks = hooks
	body := func(action uuid.UUID) map[string]any {
		return map[string]any{"actionId": action, "title": "Second task", "request": map[string]any{}, "workflow": map[string]any{"startStepId": "work",
			"steps": []any{map[string]any{"id": "work", "title": "Handle", "description": "Do the work", "agentId": d.Nodes[0].AgentID, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}}, "edges": []any{}}}
	}
	action := uuid.New()
	manager.expect(201, "POST", "/tasks", body(action))
	if c := hooks.last(t, "task.before_start"); c.event["title"] != "Second task" || c.event["stepCount"] != 1 || *c.project != e.dc.Project.ID {
		t.Fatalf("task.before_start: %+v", c)
	}
	hooks.set("task.before_start", pluginhost.Outcome{Blocked: true, Plugin: "budget", Reason: "this month's budget is used up"})
	manager.expect(201, "POST", "/tasks", body(action))
	if n := hooks.count("task.before_start"); n != 1 {
		t.Fatalf("a replay should not ask plugins again: %d", n)
	}
	manager.expectError(409, "plugin_blocked", "POST", "/tasks", body(uuid.New()))

	hooks.set("node.before_submit", pluginhost.Outcome{Blocked: true, Plugin: "qa", Reason: "tests are missing"})
	node := d.Nodes[0].ID
	manager.expectError(409, "plugin_blocked", "POST", "/tasks/"+d.Task.ID.String()+"/nodes/"+node.String()+"/submit",
		map[string]any{"actionId": uuid.New(), "output": map[string]any{}, "edgeId": nil})
	if c := hooks.last(t, "node.before_submit"); c.event["nodeRunId"] != node || *c.event["taskId"].(*uuid.UUID) != d.Task.ID {
		t.Fatalf("node.before_submit: %+v", c)
	}
}
