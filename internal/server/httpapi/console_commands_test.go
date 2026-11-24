package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
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
	_, path := e.newAgent("Command assistant")
	hooks.set("console.command", pluginhost.Outcome{Handled: true, Plugin: "bot", Value: json.RawMessage(`{"handled":true,"reply":"plugin"}`)})

	session := consoleCommand(t, e, path, "/session")
	if !session.Handled || session.Source != "platform" || session.Command != "session" || session.Session == nil || session.Session.Model.ModelID != "claude-x" || session.Session.ThinkingLevel == "" {
		t.Fatalf("unexpected session result: %+v", session)
	}
	copyResult := consoleCommand(t, e, path, "/copy")
	if copyResult.Source != "platform" || copyResult.CopyText != nil {
		t.Fatalf("with no reply copyText should be null: %+v", copyResult)
	}
	if n := hooks.count("console.command"); n != 0 {
		t.Fatalf("built-in commands should not go to plugins: %d", n)
	}
	plugin := consoleCommand(t, e, path, "/unknown value")
	if plugin.Source != "plugin" || plugin.Plugin != "bot" || plugin.Reply == nil || *plugin.Reply != "plugin" {
		t.Fatalf("unknown commands should keep the plugin behavior: %+v", plugin)
	}
	e.expectError(422, "validation_failed", "POST", path+"/messages", map[string]any{"text": "/session extra"})
	e.expectError(422, "validation_failed", "POST", path+"/messages", map[string]any{"text": "/export xml"})
}

func TestPlatformConsoleCompactCopyExportAndEndEventContract(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Session assistant")
	e.expectError(409, "session_none", "GET", path+"/session/export?format=html", nil)
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/tmp/managed-session.jsonl"); err != nil {
		t.Fatal(err)
	}

	events, closeEvents := readSSE(t, e, path, "")
	defer closeEvents()
	_ = next(t, events) // snapshot
	compact := consoleCommand(t, e, path, "/compact keep the key decisions")
	if compact.TokensBefore == nil || *compact.TokensBefore != 4200 || compact.EstimatedTokensAfter == nil || *compact.EstimatedTokensAfter != 350 {
		t.Fatalf("unexpected compact token result: %+v", compact)
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
			t.Fatalf("compact must not send run.ended or an empty message: %+v", ended)
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
		t.Fatal("no terminal run.updated received for compact")
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("compact should not create user or assistant messages: %+v", page.Messages)
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
		if ended.Message.ID == uuid.Nil || ended.Message.Run.ID != ended.Run.ID {
			t.Fatalf("a regular run.ended must carry a non-empty message: %+v", ended)
		}
		if ended.Run.Status == "completed" {
			foundNormalEnd = true
			break
		}
	}
	if !foundNormalEnd {
		t.Fatal("a regular completion should still send run.ended with a message")
	}
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "fail"})
	e.waitAgentIdle(path)
	copied := consoleCommand(t, e, path, "/copy")
	if copied.CopyText == nil || *copied.CopyText != "Hello" {
		t.Fatalf("copy should take the latest successful reply, not the failed one: %+v", copied)
	}
	if got := consoleCommand(t, e, path, "/export"); got.ExportFormat == nil || *got.ExportFormat != "html" {
		t.Fatalf("the default export format should be html: %+v", got)
	}
	if got := consoleCommand(t, e, path, "/export jsonl"); got.ExportFormat == nil || *got.ExportFormat != "jsonl" {
		t.Fatalf("unexpected explicit export format: %+v", got)
	}
	_, body, header := e.do("GET", path+"/session/export?format=jsonl", nil)
	if string(body) != "{}\n" || !strings.Contains(header.Get("Content-Disposition"), "session.jsonl") {
		t.Fatalf("unexpected JSONL download: %q %v", body, header)
	}
	_, body, header = e.do("GET", path+"/session/export?format=html", nil)
	if string(body) != "<html>exported</html>" || !strings.Contains(header.Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected HTML download: %q %v", body, header)
	}
	if err := e.st.MarkSessionMissing(ctx, agent.ID, nil); err != nil {
		t.Fatal(err)
	}
	// A lost session starts over: there is nothing to export until the next message.
	e.expectError(409, "session_none", "GET", path+"/session/export?format=html", nil)
}

// While a platform command (/new, /reload, /resume, export) holds the busy lock there is no run to report: the
// currentRunId of agent_busy is null, not an all-zero ID. Messages queued per whenBusy start once the lock is released.
func TestAgentBusyWithoutRunReportsNullCurrentRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Busy assistant")
	owner := coord.LockValue(uuid.NewString(), uuid.NewString()) // holds the lock in place of a platform command that creates no run
	if ok, err := e.co.Acquire(ctx, agent.ID.String(), owner); err != nil || !ok {
		t.Fatalf("acquire lock: %v %v", ok, err)
	}
	var busy struct {
		Error struct {
			Code    string                     `json:"code"`
			Details map[string]json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(e.expect(409, "POST", path+"/messages", map[string]any{"text": "first message"}), &busy); err != nil {
		t.Fatal(err)
	}
	if busy.Error.Code != "agent_busy" || string(busy.Error.Details["currentRunId"]) != "null" || string(busy.Error.Details["queued"]) != `{"interrupt":0,"normal":0}` {
		t.Fatalf("with no run currentRunId should be null: %+v", busy.Error)
	}
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "queued", "whenBusy": "queue"})
	if ok, err := e.co.Release(ctx, agent.ID.String(), owner); err != nil || !ok {
		t.Fatalf("release lock: %v %v", ok, err)
	}
	e.waitAgentIdle(path)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil))
	if len(page.Messages) != 2 || page.Messages[0].Text != "queued" || page.Messages[1].Run.Status != "completed" {
		t.Fatalf("the queued message should get a reply after the lock is released: %+v", page.Messages)
	}
}

func TestPlatformConsoleNewReloadAndDatabaseIdleGate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Maintenance assistant")
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/tmp/managed-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	reloaded := consoleCommand(t, e, path, "/reload")
	if reloaded.ProcessStopped == nil || !*reloaded.ProcessStopped {
		t.Fatalf("reload should return the process stop result: %+v", reloaded)
	}
	reset := consoleCommand(t, e, path, "/new")
	if reset.ResetSession == nil || reset.ResetSession.State != "none" || reset.ResetSession.Epoch != 2 {
		t.Fatalf("new should keep the history and reopen the session: %+v", reset)
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
		t.Fatalf("the read-only session query should still work while busy: %+v", got)
	}
}

func TestPlatformConsoleResumeLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Resume assistant")
	oldFile, targetFile := "/managed/old.jsonl", "/managed/target.jsonl"
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "old history"})
	e.waitAgentIdle(path)
	if err := e.st.RecordSessionFile(ctx, agent.ID, oldFile); err != nil {
		t.Fatal(err)
	}
	e.ex.sessions = []executor.ManagedSession{
		{ID: "old-full-session-id", Path: oldFile, Title: "Old session title", Preview: "Old session latest message", UpdatedAt: time.Now().Add(-time.Hour)},
		{ID: "target-full-session-id", Path: targetFile, Title: "Target session title", Preview: "Target session latest message", UpdatedAt: time.Now()},
	}
	events, closeEvents := readSSE(t, e, path, "")
	defer closeEvents()
	_ = next(t, events)
	listed := consoleCommand(t, e, path, "/resume")
	if listed.ResumeSessions == nil || len(listed.ResumeSessions) != 2 || listed.ResumeSessions[0].ID != "old-full-session-id" || listed.ResumeSessions[0].Title != "Old session title" || listed.ResumeSessions[0].Preview != "Old session latest message" || !listed.ResumeSessions[0].Current || listed.ResumedSession != nil {
		t.Fatalf("unexpected resume list: %+v", listed)
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
		t.Fatalf("unexpected resume result: %+v", resumed)
	}
	sess, err := e.st.GetAgentSession(ctx, agent.ID)
	if err != nil || sess.SessionFile != targetFile || sess.State != "active" || sess.Epoch != 2 {
		t.Fatalf("unexpected database session after resume: %+v %v", sess, err)
	}
	after, err := e.st.GetAgent(ctx, agent.ID)
	if err != nil || after.ConfigVersion != before.ConfigVersion || after.PromptVersion != before.PromptVersion || after.ModelProvider != before.ModelProvider || after.ModelID != before.ModelID || after.Prompt != before.Prompt {
		t.Fatalf("resume must not roll back the model config or Prompt: before=%+v after=%+v %v", before, after, err)
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
		t.Fatal("no session.updated received after resume")
	}
	e.expect(202, "POST", path+"/messages", map[string]any{"text": "new epoch"})
	e.waitAgentIdle(path)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil))
	if len(page.Messages) != 4 || page.Messages[0].SessionEpoch != 1 || page.Messages[2].SessionEpoch != 2 {
		t.Fatalf("the history should be kept and new messages should use the new epoch: %+v", page.Messages)
	}
}

func TestPlatformConsoleNewThenResume(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Resume after new")
	oldFile := "/managed/old.jsonl"
	if err := e.st.RecordSessionFile(ctx, agent.ID, oldFile); err != nil {
		t.Fatal(err)
	}
	e.ex.sessions = []executor.ManagedSession{{ID: "old-full-session-id", Path: oldFile, UpdatedAt: time.Now()}}
	reset := consoleCommand(t, e, path, "/new")
	if reset.ResetSession == nil || reset.ResetSession.State != "none" || reset.ResetSession.Epoch != 2 {
		t.Fatalf("unexpected reopen result: %+v", reset)
	}
	resumed := consoleCommand(t, e, path, "/resume old-full-session-id")
	if resumed.ResumedSession == nil || resumed.ResumedSession.State != "active" || resumed.ResumedSession.Epoch != 3 {
		t.Fatalf("after new the old session should be resumable: %+v", resumed)
	}
	sess, err := e.st.GetAgentSession(ctx, agent.ID)
	if err != nil || sess.SessionFile != oldFile || sess.State != "active" || sess.Epoch != 3 {
		t.Fatalf("unexpected database state after resuming following new: %+v %v", sess, err)
	}
}

// When there are queued messages that can start, platform commands do no maintenance (agent_not_idle) and that message
// starts immediately instead of waiting for the next reconciliation; once it has started, the commands cannot get the
// busy lock, which is also agent_not_idle (platform commands do not use agent_busy).
func TestPlatformConsoleQueuedWorkAlsoBlocksMaintenance(t *testing.T) {
	e := newEnv(t)
	agent, path := e.newAgent("Queue gate")
	_, _, err := e.st.InsertMessageAndRun(context.Background(), store.NewRun{AgentID: agent.ID, RunID: uuid.Must(uuid.NewV7()), Text: "block queued", Start: false, InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": "/new"})
	if st := decode[apitypes.AgentStatus](t, e.expect(200, "GET", path+"/status", nil)); st.CurrentRun == nil || st.CurrentRun.Status == "queued" {
		t.Fatalf("the queued message should have started: %+v", st)
	}
	for _, command := range []string{"/new", "/reload", "/compact", "/resume", "/resume target-full-session-id"} {
		body := decode[apitypes.ErrorBody](t, e.expect(409, "POST", path+"/messages", map[string]any{"text": command}))
		if body.Error.Code != "agent_not_idle" || body.Error.Details != nil {
			t.Fatalf("%s while busy should be agent_not_idle without details: %+v", command, body.Error)
		}
	}
	e.expectError(409, "agent_not_idle", "GET", path+"/session/export?format=jsonl", nil)
	for _, command := range []string{"/session", "/copy"} {
		consoleCommand(t, e, path, command) // read-only commands keep working while busy
	}
}

func TestClaudeCannotResumePiSession(t *testing.T) {
	e := newEnv(t)
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	agent, path := e.newAgent("Session assistant")
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "sonnet"}})
	e.ex.sessions = []executor.ManagedSession{{ID: "old-pi", Path: "/tmp/old-pi.jsonl"}}
	for _, command := range []string{"/resume", "/resume old-pi"} {
		e.expectError(409, "console_command_failed", "POST", path+"/messages", map[string]any{"text": command})
	}
	sess, err := e.st.IdleSession(context.Background(), agent.ID)
	if err != nil || sess.State != "none" {
		t.Fatalf("must not switch to a Pi session: %+v %v", sess, err)
	}
}

// Failures of executor maintenance operations that users can run into (runtime busy, no session, runtime unsupported)
// are 409, not 500 internal_error.
func TestPlatformConsoleMaintenanceErrorsAreConflicts(t *testing.T) {
	e := newEnv(t)
	agent, path := e.newAgent("Maintenance errors")
	if err := e.st.RecordSessionFile(context.Background(), agent.ID, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	busy := fmt.Errorf("%w: runtime_busy", executor.ErrMaintenanceBusy)
	e.ex.stopErr = busy
	for _, command := range []string{"/reload", "/new"} {
		e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": command})
	}
	e.ex.stopErr = nil
	e.ex.listErr = busy
	e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": "/resume"})
	e.expectError(409, "agent_not_idle", "POST", path+"/messages", map[string]any{"text": "/resume some-session"})
	e.ex.listErr = nil
	for err, code := range map[error]string{busy: "agent_not_idle", executor.ErrNoSession: "session_none",
		fmt.Errorf("%w: Claude Code", executor.ErrUnsupportedRuntime): "console_command_failed"} {
		e.ex.exportErr = err
		e.expectError(409, code, "GET", path+"/session/export?format=html", nil)
	}
}

// Claude Code Agents support /session, /new, /reload and /copy; /compact, /export and /resume only apply to Pi
// sessions, and are rejected directly with 409 console_command_failed, without creating a run.
func TestClaudeCodeConsoleCommandSupport(t *testing.T) {
	e := newEnv(t)
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	agent, path := e.newAgent("Claude commands")
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "sonnet"}})
	if err := e.st.RecordSessionFile(context.Background(), agent.ID, "/managed/claude-session"); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"/compact", "/compact keep the decisions", "/export", "/export jsonl", "/resume", "/resume some-id"} {
		body := decode[apitypes.ErrorBody](t, e.expect(409, "POST", path+"/messages", map[string]any{"text": command}))
		if body.Error.Code != "console_command_failed" || !strings.Contains(body.Error.Message, "Claude Code") {
			t.Fatalf("%s should be rejected with console_command_failed naming the runtime: %+v", command, body.Error)
		}
	}
	e.expectError(409, "console_command_failed", "GET", path+"/session/export?format=html", nil)
	var runs int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id = $1`, agent.ID).Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("unsupported commands should not create runs: %d %v", runs, err)
	}
	if got := consoleCommand(t, e, path, "/session"); got.Session == nil {
		t.Fatalf("/session: %+v", got)
	}
	if got := consoleCommand(t, e, path, "/copy"); got.Command != "copy" {
		t.Fatalf("/copy: %+v", got)
	}
	if got := consoleCommand(t, e, path, "/reload"); got.ProcessStopped == nil {
		t.Fatalf("/reload: %+v", got)
	}
	if got := consoleCommand(t, e, path, "/new"); got.ResetSession == nil {
		t.Fatalf("/new: %+v", got)
	}
}

// The result of /compact is recorded on the run: the command returns the run after it ended; it is 200 even when Pi did
// not compact, with the reason stated; /session can read the most recent compaction.
func TestPlatformConsoleCompactResultOnRun(t *testing.T) {
	e := newEnv(t)
	agent, path := e.newAgent("Compaction result")
	if err := e.st.RecordSessionFile(context.Background(), agent.ID, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	if got := consoleCommand(t, e, path, "/session"); got.Session == nil || got.Session.LastCompaction != nil {
		t.Fatalf("nothing compacted yet: %+v", got.Session)
	}
	done := consoleCommand(t, e, path, "/compact keep the decisions")
	c := done.CompactRun.Compaction
	if done.CompactRun == nil || done.CompactRun.Status != "completed" || c == nil || c.Instructions != "keep the decisions" || c.Outcome == nil || *c.Outcome != "compacted" ||
		c.TokensBefore == nil || *c.TokensBefore != 4200 || done.TokensBefore == nil || *done.TokensBefore != 4200 {
		t.Fatalf("compaction result: %+v %+v", done, c)
	}
	e.ex.compactSkip = "already_compacted"
	skipped := consoleCommand(t, e, path, "/compact")
	sc := skipped.CompactRun.Compaction
	if sc == nil || sc.Outcome == nil || *sc.Outcome != "skipped" || sc.SkipReason == nil || *sc.SkipReason != "already_compacted" || skipped.TokensBefore != nil {
		t.Fatalf("without compaction it should still complete normally and state the reason: %+v %+v", skipped, sc)
	}
	session := consoleCommand(t, e, path, "/session")
	if last := session.Session.LastCompaction; last == nil || last.ID != skipped.CompactRun.ID || last.Compaction == nil || *last.Compaction.Outcome != "skipped" {
		t.Fatalf("/session should give the latest compaction: %+v", session.Session.LastCompaction)
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages?aroundRun="+skipped.CompactRun.ID.String(), nil)); page.Messages == nil {
		t.Fatal("locating a compaction run should return a page (an empty list when there are no messages)")
	}
}

// In the anomaly list, a failed manual compaction carries compaction (the instructions; outcome is null on failure),
// and a regular run has null.
func TestAnomalyMarksFailedCompaction(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, _ := e.newAgent("Compaction anomaly")
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	compact, err := e.st.InsertConsoleCompactRun(ctx, agent.ID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "keep the decisions")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.st.FinishRun(ctx, compact.ID, store.RunEnd{Status: "failed", ErrorCode: "pi_error", ErrorMessage: "model error"}); err != nil {
		t.Fatal(err)
	}
	_, normal, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: agent.ID, RunID: uuid.Must(uuid.NewV7()), Text: "x", Start: true, InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.st.FinishRun(ctx, normal.ID, store.RunEnd{Status: "failed", ErrorCode: "pi_error"}); err != nil {
		t.Fatal(err)
	}
	page := decode[apitypes.AnomalyPage](t, e.expect(200, "GET", "/anomalies", nil))
	seen := 0
	for _, it := range page.Items {
		switch it.RunID {
		case compact.ID:
			seen++
			if it.Compaction == nil || it.Compaction.Instructions != "keep the decisions" || it.Compaction.Outcome != nil {
				t.Fatalf("a failed compaction should carry compaction: %+v", it.Compaction)
			}
		case normal.ID:
			seen++
			if it.Compaction != nil {
				t.Fatalf("compaction of a regular run should be null: %+v", it.Compaction)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("both anomalies should be listed: %+v", page.Items)
	}
}
