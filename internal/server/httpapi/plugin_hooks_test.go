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

// fakeHooks 记录同步 Hook 的调用，按事件类型返回预设的结果。
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

// last 返回某类事件最近一次调用。
func (f *fakeHooks) last(t *testing.T, typ string) hookCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].event["type"] == typ {
			return f.calls[i]
		}
	}
	t.Fatalf("没有调用 %s", typ)
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

// 控制台消息：/ 开头的先给认领类 console.command，被认领时不发给 Agent、返回插件的回复；其余经 message.before_enqueue 改写。
// Inbox：inbox.before_send 可以阻止；Agent 发出的经 inbox.before_deliver 改写。sleep.before_schedule 可以改时长。
// 每个事件都带固定字段与 Agent 所在的项目。
func TestPluginHookCallSites(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hooks := &fakeHooks{outcomes: map[string]pluginhost.Outcome{}}
	e.api.Hooks = hooks
	project := e.dc.Project.ID
	agent, path := e.newAgent("助手")

	hooks.set("console.command", pluginhost.Outcome{Handled: true, Plugin: "bot", Value: json.RawMessage(`{"handled":true,"reply":"pong"}`)})
	res := decode[map[string]any](t, e.expect(200, "POST", path+"/messages", map[string]any{"text": "/ping now"}))
	if res["handled"] != true || res["plugin"] != "bot" || res["reply"] != "pong" {
		t.Fatalf("认领：%v", res)
	}
	cmd := hooks.last(t, "console.command")
	if cmd.kind != pluginhost.Claim || cmd.event["command"] != "ping" || *cmd.project != project || cmd.event["agentId"].(*uuid.UUID).String() != agent.ID.String() || len(cmd.event["id"].(string)) != 26 {
		t.Fatalf("console.command 事件：%+v", cmd)
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("被认领的命令不应保存：%+v", page)
	}

	hooks.set("console.command", pluginhost.Outcome{})
	hooks.set("message.before_enqueue", pluginhost.Outcome{Patch: map[string]any{"text": "改写后的消息"}})
	sent := decode[apitypes.SendMessageResult](t, e.expect(202, "POST", path+"/messages", map[string]any{"text": "/unknown 原文"}))
	if sent.Message.Text != "改写后的消息" || hooks.last(t, "message.before_enqueue").event["text"] != "/unknown 原文" {
		t.Fatalf("改写：%+v", sent.Message)
	}
	e.waitAgentIdle(path)

	hooks.set("inbox.before_send", pluginhost.Outcome{Blocked: true, Plugin: "guard", Reason: "工作时间外"})
	e.expectError(409, "plugin_blocked", "POST", "/inbox/messages", map[string]any{"to": "默认项目/助手", "text": "在吗"})
	if c := hooks.last(t, "inbox.before_send"); c.project == nil || *c.project != project || c.event["to"] != "默认项目/助手" {
		t.Fatalf("控制台发给 Agent 时按收件 Agent 的项目：%+v", c)
	}
	hooks.set("inbox.before_send", pluginhost.Outcome{})
	hooks.set("inbox.before_deliver", pluginhost.Outcome{Patch: map[string]any{"text": "[已审阅] 你好"}})
	other, _ := e.newAgent("同事")
	msg := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/同事", "text": "你好"}))
	if msg.Text != "[已审阅] 你好" {
		t.Fatalf("Agent 发出的消息应被改写：%+v", msg)
	}
	_ = other

	platform := e.platformAgent("睡眠者")
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
		t.Fatalf("sleep 时长应被改写：%+v", got.Sleep)
	}
	if c := hooks.last(t, "sleep.before_schedule"); c.project != nil || c.event["durationSeconds"] != int64(120) {
		t.Fatalf("平台 Agent 的 sleep 事件：%+v", c)
	}
}

// 任务创建与节点提交前的拦截：被阻止时 409 plugin_blocked；同一动作 ID 的重放直接返回原结果，不再询问插件。
func TestPluginHookTaskCallSites(t *testing.T) {
	e, manager, d := taskEnv(t)
	hooks := &fakeHooks{outcomes: map[string]pluginhost.Outcome{}}
	e.api.Hooks = hooks
	body := func(action uuid.UUID) map[string]any {
		return map[string]any{"actionId": action, "title": "第二个任务", "request": map[string]any{}, "workflow": map[string]any{"startStepId": "work",
			"steps": []any{map[string]any{"id": "work", "title": "处理", "description": "执行", "agentId": d.Nodes[0].AgentID, "inputFields": []any{}, "outputFields": []any{}, "inputs": map[string]any{}}}, "edges": []any{}}}
	}
	action := uuid.New()
	manager.expect(201, "POST", "/tasks", body(action))
	if c := hooks.last(t, "task.before_start"); c.event["title"] != "第二个任务" || c.event["stepCount"] != 1 || *c.project != e.dc.Project.ID {
		t.Fatalf("task.before_start：%+v", c)
	}
	hooks.set("task.before_start", pluginhost.Outcome{Blocked: true, Plugin: "budget", Reason: "本月额度用完"})
	manager.expect(201, "POST", "/tasks", body(action))
	if n := hooks.count("task.before_start"); n != 1 {
		t.Fatalf("重放不应再询问插件：%d", n)
	}
	manager.expectError(409, "plugin_blocked", "POST", "/tasks", body(uuid.New()))

	hooks.set("node.before_submit", pluginhost.Outcome{Blocked: true, Plugin: "qa", Reason: "缺少测试"})
	node := d.Nodes[0].ID
	manager.expectError(409, "plugin_blocked", "POST", "/tasks/"+d.Task.ID.String()+"/nodes/"+node.String()+"/submit",
		map[string]any{"actionId": uuid.New(), "output": map[string]any{}, "edgeId": nil})
	if c := hooks.last(t, "node.before_submit"); c.event["nodeRunId"] != node || *c.event["taskId"].(*uuid.UUID) != d.Task.ID {
		t.Fatalf("node.before_submit：%+v", c)
	}
}
