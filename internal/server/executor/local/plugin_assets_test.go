package local

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/pluginhost"
)

// 已授权插件交给 Pi：每个扩展一条 -e、每个 Skill 一条 --skill，有 MCP 服务器时写 mcp.json 并加 -e builtin:mcp；
// CLI 目录在模板 CLI 之后、执行基础 PATH 之前；插件变量不覆盖平台的目录变量。没有插件时删除旧的 mcp.json。
func TestPluginAssetsReachPi(t *testing.T) {
	e := &Executor{o: Options{ExecBasePath: "/usr/bin", Username: "u", WorkRoot: t.TempDir()}}
	d := e.dirs([16]byte{1})
	if err := d.ensure(); err != nil {
		t.Fatal(err)
	}
	p := agentconfig.PluginAssets{
		Extensions: []string{"/p/a/agent.ts", "/p/b/agent.ts"}, Skills: []string{"/p/a/skills"},
		MCPServers: map[string]json.RawMessage{"a-browser": json.RawMessage(`{"command":"node","args":["/p/a/mcp.js"]}`)},
		Bin:        []string{"/p/a/bin"}, Env: map[string]string{"LOOP_NOW_PLUGIN_A_CONFIG": `{"x":1}`, "HOME": "/evil"},
	}
	args := pluginArgs(p)
	if strings.Join(args, " ") != "-e /p/a/agent.ts -e /p/b/agent.ts --skill /p/a/skills -e builtin:mcp" {
		t.Fatalf("参数：%v", args)
	}
	if err := writePluginMCP(d, p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.pi, "mcp.json"))
	var file struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err != nil || json.Unmarshal(b, &file) != nil || file.MCPServers["a-browser"]["command"] != "node" {
		t.Fatalf("mcp.json：%s %v", b, err)
	}
	env := e.buildEnv(d, agentconfig.Snapshot{Plugins: p}, []string{"/cli/gh"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !slices.Contains(env, "PATH=/cli/gh:/p/a/bin:/usr/bin") || !slices.Contains(env, `LOOP_NOW_PLUGIN_A_CONFIG={"x":1}`) || !slices.Contains(env, "HOME="+d.home) {
		t.Fatalf("环境：%v", env)
	}
	if err := writePluginMCP(d, agentconfig.PluginAssets{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.pi, "mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("没有 MCP 服务器时应删除 mcp.json：%v", err)
	}
	if len(pluginArgs(agentconfig.PluginAssets{})) != 0 {
		t.Fatal("没有插件时不加参数")
	}
}

type blockingHooks struct {
	events []map[string]any
	block  bool
}

func (b *blockingHooks) Hook(_ context.Context, kind pluginhost.Kind, event map[string]any, _ *uuid.UUID) (pluginhost.Outcome, error) {
	b.events = append(b.events, event)
	return pluginhost.Outcome{Blocked: b.block, Plugin: "guard", Reason: "维护中"}, nil
}

// run.before_start 被阻止时这一轮以 plugin_blocked 失败，不启动 Pi；放行时照常执行。事件带来源与触发消息。
func TestRunBeforeStartHook(t *testing.T) {
	hooks := &blockingHooks{block: true}
	h := newHarness(t, func(o *Options) { o.Hooks = hooks }, "")
	id := h.agent(nil)
	msg := uuid.New()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Message: "hi", Origin: "console", MessageID: msg}, newSink())
	if err != nil || res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrPluginBlocked || !strings.Contains(res.ErrorMessage, "guard") || !strings.Contains(res.ErrorMessage, "维护中") {
		t.Fatalf("被阻止：%+v %v", res, err)
	}
	if h.pid(id) != 0 {
		t.Fatal("被阻止时不应启动 Pi")
	}
	if ev := hooks.events[0]; ev["type"] != "run.before_start" || ev["origin"] != "console" || ev["messageId"] != msg {
		t.Fatalf("事件：%v", ev)
	}
	hooks.block = false
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("放行后照常执行：%+v", res)
	}
}

// 扩展对话经 Options.Dialogs 回答：带执行的 Agent 与这一轮的 ID，回答交还给 Pi；这一轮被打断时 Dialogs 的 ctx 结束，Pi 收到取消。
func TestExtensionDialogsReachPi(t *testing.T) {
	type call struct {
		agent, run uuid.UUID
		req        pirpc.DialogRequest
	}
	calls := make(chan call, 4)
	block := make(chan struct{})
	h := newHarness(t, func(o *Options) {
		o.Dialogs = func(ctx context.Context, agent, run uuid.UUID, req pirpc.DialogRequest) pirpc.DialogResponse {
			calls <- call{agent, run, req}
			if req.Method == "confirm" {
				select {
				case <-ctx.Done():
					return pirpc.DialogResponse{Cancelled: true, Outcome: "expired"}
				case <-block:
				}
			}
			return pirpc.DialogResponse{Value: "production", Outcome: "answered"}
		}
	}, "")
	id := h.agent(nil)
	run, res := h.execRun(id, "!ask=select")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "answer value=production" {
		t.Fatalf("Pi 收到回答：%+v", res)
	}
	if c := <-calls; c.agent != id || c.run != run || c.req.Method != "select" || len(c.req.Options) != 2 {
		t.Fatalf("交给 Dialogs 的对话：%+v", c)
	}
	done := make(chan executor.Result, 1)
	runID := uuid.Must(uuid.NewV7())
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: "!ask=confirm"}, newSink())
		done <- res
	}()
	if c := <-calls; c.req.Method != "confirm" || c.run != runID {
		t.Fatalf("第二轮的对话：%+v", c)
	}
	_ = h.e.Interrupt(context.Background(), id, runID)
	select {
	case res := <-done:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Fatalf("打断后：%+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("打断后这一轮应结束")
	}
	close(block)
}
