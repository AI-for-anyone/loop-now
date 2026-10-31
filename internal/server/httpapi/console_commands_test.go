package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
)

func consoleCommand(t *testing.T, e *env, path, text string) apitypes.ConsoleCommandResult {
	t.Helper()
	return decode[apitypes.ConsoleCommandResult](t, e.expect(200, "POST", path+"/messages", map[string]any{"text": text}))
}

func TestPlatformConsoleCommandsPrecedePlugins(t *testing.T) {
	e := newEnv(t)
	hooks := &fakeHooks{outcomes: map[string]pluginhost.Outcome{}}
	e.api.Hooks = hooks
	_, path := e.newAgent("命令助手")
	hooks.set("console.command", pluginhost.Outcome{Handled: true, Plugin: "bot", Value: json.RawMessage(`{"handled":true,"reply":"plugin"}`)})

	session := consoleCommand(t, e, path, "/session")
	if !session.Handled || session.Source != "platform" || session.Command != "session" || session.Session == nil || session.Session.Model.ModelID != "claude-x" || session.Session.ThinkingLevel == "" {
		t.Fatalf("session 结果不符：%+v", session)
	}
	copyResult := consoleCommand(t, e, path, "/copy")
	if copyResult.Source != "platform" || copyResult.CopyText != nil {
		t.Fatalf("没有回复时 copyText 应为 null：%+v", copyResult)
	}
	if n := hooks.count("console.command"); n != 0 {
		t.Fatalf("内置命令不应交给插件：%d", n)
	}
	plugin := consoleCommand(t, e, path, "/unknown value")
	if plugin.Source != "plugin" || plugin.Plugin != "bot" || plugin.Reply == nil || *plugin.Reply != "plugin" {
		t.Fatalf("未知命令应保留插件行为：%+v", plugin)
	}
	e.expectError(422, "validation_failed", "POST", path+"/messages", map[string]any{"text": "/session extra"})
	e.expectError(422, "validation_failed", "POST", path+"/messages", map[string]any{"text": "/export xml"})
}

func TestPlatformConsoleCompactCopyExportAndEndEventContract(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("会话助手")
	e.expectError(409, "session_none", "GET", path+"/session/export?format=html", nil)
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/tmp/managed-session.jsonl"); err != nil {
		t.Fatal(err)
	}

	events, closeEvents := readSSE(t, e, path, "")
	defer closeEvents()
	_ = next(t, events) // snapshot
	compact := consoleCommand(t, e, path, "/compact 保留关键决定")
	if compact.TokensBefore == nil || *compact.TokensBefore != 4200 || compact.EstimatedTokensAfter == nil || *compact.EstimatedTokensAfter != 350 {
		t.Fatalf("compact Token 结果不符：%+v", compact)
	}
	foundTerminal := false
	for range 20 {
		event := next(t, events)
		switch event.event {
		case "run.ended":
			var ended apitypes.SseRunEnded
			if err := json.Unmarshal([]byte(event.data), &ended); err != nil {
				t.Fatal(err)
			}
			t.Fatalf("compact 不得发送 run.ended 或空消息：%+v", ended)
		case "run.updated":
			var updated apitypes.SseRunUpdated
			if err := json.Unmarshal([]byte(event.data), &updated); err != nil {
				t.Fatal(err)
			}
			if updated.Run.Status == "completed" {
				foundTerminal = true
			}
		}
		if foundTerminal {
			break
		}
	}
	if !foundTerminal {
		t.Fatal("没有收到 compact 的终态 run.updated")
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("compact 不应创建用户或助手消息：%+v", page.Messages)
	}

	e.expect(202, "POST", path+"/messages", map[string]any{"text": "successful"})
	e.waitAgentIdle(path)
	foundNormalEnd := false
	for range 20 {
		event := next(t, events)
		if event.event != "run.ended" {
			continue
		}
		var ended apitypes.SseRunEnded
		if err := json.Unmarshal([]byte(event.data), &ended); err != nil {
			t.Fatal(err)
		}
		if ended.Message == nil {
			t.Fatalf("普通 run.ended 必须带非空消息：%+v", ended)
		}
		if ended.Run.Status == "completed" {
			foundNormalEnd = true
			break
		}
	}
	if !foundNormalEnd {
		t.Fatal("普通完成仍应发送带消息的 run.ended")
	}
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "fail"})
	e.waitAgentIdle(path)
	copied := consoleCommand(t, e, path, "/copy")
	if copied.CopyText == nil || *copied.CopyText != "你好" {
		t.Fatalf("copy 应取最近成功回复而不是失败回复：%+v", copied)
	}
	if got := consoleCommand(t, e, path, "/export"); got.ExportFormat == nil || *got.ExportFormat != "html" {
		t.Fatalf("默认导出格式应为 html：%+v", got)
	}
	if got := consoleCommand(t, e, path, "/export jsonl"); got.ExportFormat == nil || *got.ExportFormat != "jsonl" {
		t.Fatalf("显式导出格式不符：%+v", got)
	}
	_, body, header := e.do("GET", path+"/session/export?format=jsonl", nil)
	if string(body) != "{}\n" || !strings.Contains(header.Get("Content-Disposition"), "session.jsonl") {
		t.Fatalf("JSONL 下载不符：%q %v", body, header)
	}
	_, body, header = e.do("GET", path+"/session/export?format=html", nil)
	if string(body) != "<html>exported</html>" || !strings.Contains(header.Get("Content-Type"), "text/html") {
		t.Fatalf("HTML 下载不符：%q %v", body, header)
	}
	if err := e.st.MarkSessionMissing(ctx, agent.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.expectError(409, "session_missing", "GET", path+"/session/export?format=html", nil)
}

func TestPlatformConsoleNewReloadAndDatabaseIdleGate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("维护助手")
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/tmp/managed-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	reloaded := consoleCommand(t, e, path, "/reload")
	if reloaded.ProcessStopped == nil || !*reloaded.ProcessStopped {
		t.Fatalf("reload 应返回进程停止结果：%+v", reloaded)
	}
	reset := consoleCommand(t, e, path, "/new")
	if reset.ResetSession == nil || reset.ResetSession.State != "none" || reset.ResetSession.Epoch != 2 {
		t.Fatalf("new 应保留历史并重开会话：%+v", reset)
	}
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/tmp/managed-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: agent.ID, RunID: uuid.Must(uuid.NewV7()), Text: "active", Start: true, InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/new", "/reload", "/compact", "/resume", "/resume target-full-session-id"} {
		e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": command})
	}
	e.expectError(409, "agent_not_idle", "GET", path+"/session/export?format=html", nil)
	if got := consoleCommand(t, e, path, "/session"); got.Session == nil || !got.Session.Status.Busy {
		t.Fatalf("session 只读查询在忙碌时仍应可用：%+v", got)
	}
}

func TestPlatformConsoleResumeLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("恢复助手")
	oldFile, targetFile := "/managed/old.jsonl", "/managed/target.jsonl"
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "old history"})
	e.waitAgentIdle(path)
	if err := e.st.RecordSessionFile(ctx, agent.ID, oldFile); err != nil {
		t.Fatal(err)
	}
	e.ex.sessions = []executor.ManagedSession{
		{ID: "old-full-session-id", Path: oldFile, UpdatedAt: time.Now().Add(-time.Hour)},
		{ID: "target-full-session-id", Path: targetFile, UpdatedAt: time.Now()},
	}
	events, closeEvents := readSSE(t, e, path, "")
	defer closeEvents()
	_ = next(t, events)
	listed := consoleCommand(t, e, path, "/resume")
	if listed.ResumeSessions == nil || len(listed.ResumeSessions) != 2 || listed.ResumeSessions[0].ID != "old-full-session-id" || !listed.ResumeSessions[0].Current || listed.ResumedSession != nil {
		t.Fatalf("resume 列表不符：%+v", listed)
	}
	for _, command := range []string{"/resume unknown", "/resume /managed/target.jsonl", "/resume ../target.jsonl"} {
		e.expectError(422, "validation_failed", "POST", path+"/messages", map[string]any{"text": command})
	}
	e.expectError(409, "console_command_failed", "POST", path+"/messages", map[string]any{"text": "/resume old-full-session-id"})
	before, err := e.st.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	resumed := consoleCommand(t, e, path, "/resume target-full-session-id")
	if resumed.ResumedSession == nil || resumed.ResumedSession.State != "active" || resumed.ResumedSession.Epoch != 2 {
		t.Fatalf("恢复结果不符：%+v", resumed)
	}
	sess, err := e.st.GetAgentSession(ctx, agent.ID)
	if err != nil || sess.SessionFile != targetFile || sess.State != "active" || sess.Epoch != 2 {
		t.Fatalf("恢复后的数据库会话不符：%+v %v", sess, err)
	}
	after, err := e.st.GetAgent(ctx, agent.ID)
	if err != nil || after.ConfigVersion != before.ConfigVersion || after.PromptVersion != before.PromptVersion || after.ModelProvider != before.ModelProvider || after.ModelID != before.ModelID || after.Prompt != before.Prompt {
		t.Fatalf("恢复不得回滚模型配置或 Prompt：before=%+v after=%+v %v", before, after, err)
	}
	found := false
	for range 20 {
		event := next(t, events)
		if event.event != "session.updated" {
			continue
		}
		var updated apitypes.SseSessionUpdated
		if err := json.Unmarshal([]byte(event.data), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.State == "active" && updated.Epoch == 2 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("没有收到恢复后的 session.updated")
	}
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "new epoch"})
	e.waitAgentIdle(path)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil))
	if len(page.Messages) != 4 || page.Messages[0].SessionEpoch != 1 || page.Messages[2].SessionEpoch != 2 {
		t.Fatalf("历史应保留且新消息使用新 epoch：%+v", page.Messages)
	}
}

func TestPlatformConsoleNewThenResume(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("重开后恢复")
	oldFile := "/managed/old.jsonl"
	if err := e.st.RecordSessionFile(ctx, agent.ID, oldFile); err != nil {
		t.Fatal(err)
	}
	e.ex.sessions = []executor.ManagedSession{{ID: "old-full-session-id", Path: oldFile, UpdatedAt: time.Now()}}
	reset := consoleCommand(t, e, path, "/new")
	if reset.ResetSession == nil || reset.ResetSession.State != "none" || reset.ResetSession.Epoch != 2 {
		t.Fatalf("重开结果不符：%+v", reset)
	}
	resumed := consoleCommand(t, e, path, "/resume old-full-session-id")
	if resumed.ResumedSession == nil || resumed.ResumedSession.State != "active" || resumed.ResumedSession.Epoch != 3 {
		t.Fatalf("new 后应能恢复旧会话：%+v", resumed)
	}
	sess, err := e.st.GetAgentSession(ctx, agent.ID)
	if err != nil || sess.SessionFile != oldFile || sess.State != "active" || sess.Epoch != 3 {
		t.Fatalf("new 后恢复的数据库状态不符：%+v %v", sess, err)
	}
}

func TestPlatformConsoleQueuedWorkAlsoBlocksMaintenance(t *testing.T) {
	e := newEnv(t)
	agent, path := e.newAgent("排队门禁")
	_, _, err := e.st.InsertMessageAndRun(context.Background(), store.NewRun{AgentID: agent.ID, RunID: uuid.Must(uuid.NewV7()), Text: "queued", Start: false, InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/new", "/resume", "/resume target-full-session-id"} {
		e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": command})
	}
	e.expectError(409, "agent_not_idle", "GET", path+"/session/export?format=jsonl", nil)
}
