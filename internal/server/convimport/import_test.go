package convimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// importEnv is a service over a store, a model catalog that lists Claude Code's aliases and two Codex models, and a
// work root for the Agents' folders.
func importEnv(t *testing.T, f *fixture) (*Service, *store.Store) {
	t.Helper()
	s, st := newService(t, f)
	s.Models = modelcatalog.New("1", nil, []modelcatalog.Provider{modelcatalog.ClaudeCodeProvider(),
		modelcatalog.CodexProvider([]modelcatalog.CodexModel{{ID: "gpt-6-luna", Name: "Luna"}, {ID: "gpt-6", Name: "GPT-6"}})})
	s.WorkRoot = t.TempDir()
	s.OrgID = f.org
	return s, st
}

func runImport(t *testing.T, s *Service, scanID string, picks ...apitypes.ConversationImportFolder) apitypes.ConversationImport {
	t.Helper()
	started, err := s.StartImport(context.Background(), apitypes.ConversationImportInput{ScanID: scanID, Folders: picks})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.Import(started.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.State != "running" {
			return op
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the import did not end")
	return apitypes.ConversationImport{}
}

func doneScan(t *testing.T, s *Service) apitypes.ConversationScan {
	t.Helper()
	started, err := s.StartScan(context.Background(), apitypes.ConversationScanInput{})
	if err != nil {
		t.Fatal(err)
	}
	return waitScan(t, s, started.OperationID)
}

func agentOf(t *testing.T, op apitypes.ConversationImport, sessionID string) uuid.UUID {
	t.Helper()
	for _, a := range op.Agents {
		if a.SessionID == sessionID {
			return a.AgentID
		}
	}
	t.Fatalf("no Agent for %s in %+v", sessionID, op)
	return uuid.Nil
}

// codexRollout writes a Codex rollout of the given prompts, each answered, in cwd; its turns are t1, t2, ...
func (f *fixture) codexRollout(id, cwd, model string, prompts ...string) string {
	f.t.Helper()
	at := f.at(time.Hour)
	recs := []map[string]any{{"type": "session_meta", "timestamp": at, "payload": map[string]any{"id": id, "cwd": cwd}}}
	for i, p := range prompts {
		turn := fmt.Sprintf("t%d", i+1)
		recs = append(recs,
			map[string]any{"type": "turn_context", "timestamp": at, "payload": map[string]any{"cwd": cwd, "model": model, "turn_id": turn}},
			map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "user_message", "message": p}},
			map[string]any{"type": "response_item", "timestamp": at, "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "did " + p}}}},
			map[string]any{"type": "event_msg", "timestamp": at, "payload": map[string]any{"type": "task_complete", "turn_id": turn}})
	}
	return f.write(".codex/sessions/2026/11/25/rollout-2026-11-25T08-00-00-"+id+".jsonl", lines(recs...))
}

// A folder nobody has yet becomes a project named after it, with the folder as its local directory; each conversation
// becomes an Agent on its CLI's runtime, working in the conversation's own folder with the person's own configuration,
// the default permission mode and its last model; the Agent remembers where it came from.
func TestImportCreatesProjectBindsFolderCreatesAgents(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("src/shop", "git@example.com:acme/shop.git")
	wt := f.worktree(repo, "src/shop-fix")
	claudeID := "l0000001-1111-4111-8111-111111111111"
	codexID := "l0000002-2222-4222-8222-222222222222"
	f.claude(claudeID, wt, time.Hour, "Fix checkout")
	f.codexRollout(codexID, repo, "gpt-6", "Add tests")
	s, st := importEnv(t, f)
	s.DefaultMode = func(context.Context) (string, error) { return "supervised", nil }
	scan := doneScan(t, s)
	op := runImport(t, s, scan.OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{claudeID, codexID}})
	if op.State != "done" || op.Error != nil || len(op.Errors) != 0 || op.Done != 2 || op.Total != 2 || len(op.Projects) != 1 || op.Projects[0].Path != repo {
		t.Fatalf("import: %+v", op)
	}
	ctx := context.Background()
	p, err := st.GetProject(ctx, op.Projects[0].ProjectID)
	if err != nil || p.Name != "shop" {
		t.Fatalf("project: %+v %v", p, err)
	}
	dirs, err := st.ProjectLocalDirectories(ctx, p.ID)
	if err != nil || len(dirs.Directories) != 1 || dirs.Directories[0].Path != repo {
		t.Fatalf("local directories: %+v %v", dirs, err)
	}
	claude, err := st.GetAgent(ctx, agentOf(t, op, claudeID))
	if err != nil {
		t.Fatal(err)
	}
	if claude.ProjectID == nil || *claude.ProjectID != p.ID || claude.Name != "Fix checkout" || claude.ModelProvider != agentruntime.ClaudeCodeProvider || claude.ModelID != "opus" {
		t.Fatalf("Claude Agent: %+v", claude)
	}
	if claude.WorkingDirectory == nil || *claude.WorkingDirectory != wt || !claude.UseLocalConfig || claude.PermissionMode != "supervised" {
		t.Fatalf("Claude Agent settings: %+v", claude)
	}
	if claude.ImportedFrom == nil || claude.ImportedFrom.CLI != "claude" || claude.ImportedFrom.SessionID != claudeID || !strings.HasSuffix(claude.ImportedFrom.Path, claudeID+".jsonl") ||
		!claude.ImportedFrom.ImportedAt.Equal(f.now) {
		t.Fatalf("Claude source: %+v", claude.ImportedFrom)
	}
	codex, err := st.GetAgent(ctx, agentOf(t, op, codexID))
	if err != nil {
		t.Fatal(err)
	}
	if codex.ModelProvider != agentruntime.CodexProvider || codex.ModelID != "gpt-6" || codex.WorkingDirectory == nil || *codex.WorkingDirectory != repo ||
		codex.ImportedFrom == nil || codex.ImportedFrom.CLI != "codex" {
		t.Fatalf("Codex Agent: %+v", codex)
	}
	// A plain new Agent keeps today's settings.
	plain, err := st.CreateAgent(ctx, store.ProjectScope(p.ID), "plain", agentruntime.ClaudeCodeProvider, "opus", "")
	if err != nil || plain.WorkingDirectory != nil || plain.UseLocalConfig || plain.PermissionMode != "full" || plain.ImportedFrom != nil {
		t.Fatalf("plain Agent: %+v %v", plain, err)
	}
}

// A folder a project already has goes to that project; a project picked by hand gets the folder added to its local
// directories.
func TestImportReusesProjectBindingFolder(t *testing.T) {
	f := newFixture(t)
	a := f.repo("a", "")
	b := f.repo("b", "")
	f.claude("m0000001-1111-4111-8111-111111111111", a, time.Hour, "in a")
	f.claude("m0000002-1111-4111-8111-111111111111", b, time.Hour, "in b")
	s, st := importEnv(t, f)
	ctx := context.Background()
	bound, err := st.CreateProject(ctx, f.org, "Already")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutProjectLocalDirectories(ctx, bound.ID, 0, []store.LocalDirectoryEntry{{Path: a}}); err != nil {
		t.Fatal(err)
	}
	picked, err := st.CreateProject(ctx, f.org, "Picked")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutProjectLocalDirectories(ctx, picked.ID, 0, []store.LocalDirectoryEntry{{Path: "/elsewhere", Note: "kept"}}); err != nil {
		t.Fatal(err)
	}
	scan := doneScan(t, s)
	op := runImport(t, s, scan.OperationID,
		apitypes.ConversationImportFolder{Path: a, SessionIDs: []string{"m0000001-1111-4111-8111-111111111111"}},
		apitypes.ConversationImportFolder{Path: b, ProjectID: &picked.ID, SessionIDs: []string{"m0000002-1111-4111-8111-111111111111"}})
	if op.State != "done" || len(op.Errors) != 0 || len(op.Projects) != 2 || op.Projects[0].ProjectID != bound.ID || op.Projects[1].ProjectID != picked.ID {
		t.Fatalf("import: %+v", op)
	}
	dirs, err := st.ProjectLocalDirectories(ctx, picked.ID)
	if err != nil || len(dirs.Directories) != 2 || dirs.Directories[0].Path != "/elsewhere" || dirs.Directories[1].Path != b {
		t.Fatalf("picked project's directories: %+v %v", dirs, err)
	}
	if dirs, _ := st.ProjectLocalDirectories(ctx, bound.ID); len(dirs.Directories) != 1 {
		t.Fatalf("a folder the project has is not added again: %+v", dirs)
	}
}

// A conversation imported before is not imported again: the import and the next scan give its Agent. Once that Agent
// is deleted, the conversation can be imported again.
func TestImportTwiceLinksExistingAgent(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("twice", "")
	id := "n0000001-1111-4111-8111-111111111111"
	f.claude(id, repo, time.Hour, "once")
	s, st := importEnv(t, f)
	first := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{id}})
	agentID := agentOf(t, first, id)
	scan := doneScan(t, s)
	if c := scan.Folders[0].Conversations[0]; c.AgentID == nil || *c.AgentID != agentID {
		t.Fatalf("the scan marks it imported: %+v", c)
	}
	second := runImport(t, s, scan.OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{id}})
	if second.State != "done" || len(second.Errors) != 0 || agentOf(t, second, id) != agentID {
		t.Fatalf("second import: %+v", second)
	}
	agents, err := st.ListAgents(context.Background(), store.ProjectScope(first.Projects[0].ProjectID))
	if err != nil || len(agents) != 1 {
		t.Fatalf("one Agent: %d %v", len(agents), err)
	}
	if err := st.SoftDeleteAgent(context.Background(), agentID); err != nil {
		t.Fatal(err)
	}
	third := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{id}})
	if again := agentOf(t, third, id); again == agentID {
		t.Fatal("a deleted Agent is not given back")
	}
}

// The Agent's chat shows the conversation as T3 does: the first turn and the newest turns, at most 200 messages, text
// only (no tool activity), each turn a completed reply of origin imported that counts as a turn.
func TestDisplayHistoryFirstPromptPlusNewest200NoTools(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("long", "")
	id := "o0000001-1111-4111-8111-111111111111"
	var recs []map[string]any
	for i := range 150 {
		at := f.at(time.Duration(150-i) * time.Minute)
		recs = append(recs,
			map[string]any{"type": "user", "sessionId": id, "cwd": repo, "timestamp": at, "message": map[string]any{"role": "user", "content": fmt.Sprintf("prompt %d", i)}},
			map[string]any{"type": "assistant", "sessionId": id, "cwd": repo, "timestamp": at, "message": map[string]any{"role": "assistant", "model": "claude-sonnet-5-5",
				"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("looking %d", i)}, map[string]any{"type": "tool_use", "id": "t", "name": "Bash", "input": map[string]any{}}}}},
			map[string]any{"type": "user", "sessionId": id, "cwd": repo, "timestamp": at, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t", "content": "out"}}}},
			map[string]any{"type": "assistant", "sessionId": id, "cwd": repo, "timestamp": at, "message": map[string]any{"role": "assistant", "model": "claude-sonnet-5-5",
				"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("answer %d", i)}}}})
	}
	f.write(".claude/projects/-long/"+id+".jsonl", lines(recs...))
	s, st := importEnv(t, f)
	op := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{id}})
	page, err := st.ListMessages(context.Background(), agentOf(t, op, id), store.MessageQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 200 || page.TurnsTotal != 100 {
		t.Fatalf("messages %d, turns %d", len(page.Items), page.TurnsTotal)
	}
	first, second := page.Items[0], page.Items[1]
	if first.Role != "user" || first.Text != "prompt 0" || second.Role != "assistant" || second.Text != "looking 0\n\nanswer 0" {
		t.Fatalf("first turn: %+v / %+v", first.Message, second.Message)
	}
	if next := page.Items[2]; next.Text != "prompt 51" {
		t.Fatalf("then the newest turns: %q", next.Text)
	}
	if last := page.Items[199]; last.Role != "assistant" || last.Text != "looking 149\n\nanswer 149" {
		t.Fatalf("last: %+v", last.Message)
	}
	for _, m := range page.Items {
		if m.Run.Origin != "imported" || m.Run.Status != "completed" {
			t.Fatalf("message %+v run %+v", m.Message, m.Run)
		}
		if strings.Contains(m.Text, "out") {
			t.Fatalf("tool output shown: %q", m.Text)
		}
	}
	if first.RunID != second.RunID || page.Items[2].RunID == first.RunID {
		t.Fatal("one run per turn")
	}
	if !first.CreatedAt.Equal(f.now.Add(-150 * time.Minute)) {
		t.Fatalf("messages keep their times: %v", first.CreatedAt)
	}
}

// Importing copies Claude Code's session file into the Agent's folder (up to its last complete record) and records
// Codex's thread and last finished turn; the originals are never changed.
func TestCaptureLeavesOriginalUntouched(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("cap", "")
	claudeID := "p0000001-1111-4111-8111-111111111111"
	codexID := "p0000002-2222-4222-8222-222222222222"
	claudePath := f.claude(claudeID, repo, time.Hour, "first", "second")
	fh, err := os.OpenFile(claudePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	complete, _ := os.ReadFile(claudePath)
	_, _ = fh.WriteString(`{"type":"user","sessionId":"` + claudeID + `","message":{"role":"user","content":"thi`)
	fh.Close()
	codexPath := f.codexRollout(codexID, repo, "gpt-6", "one", "two")
	fh, err = os.OpenFile(codexPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fh.WriteString(lines(map[string]any{"type": "turn_context", "timestamp": f.at(time.Hour), "payload": map[string]any{"cwd": repo, "model": "gpt-6", "turn_id": "t3"}},
		map[string]any{"type": "event_msg", "timestamp": f.at(time.Hour), "payload": map[string]any{"type": "user_message", "message": "three, still running"}}))
	fh.Close()
	hash := func(p string) [32]byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	stat := func(p string) os.FileInfo {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	before := map[string][32]byte{claudePath: hash(claudePath), codexPath: hash(codexPath)}
	beforeInfo := map[string]os.FileInfo{claudePath: stat(claudePath), codexPath: stat(codexPath)}
	s, st := importEnv(t, f)
	op := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{claudeID, codexID}})
	if op.State != "done" || len(op.Errors) != 0 {
		t.Fatalf("import: %+v", op)
	}
	for p, h := range before {
		if hash(p) != h || !stat(p).ModTime().Equal(beforeInfo[p].ModTime()) || stat(p).Mode() != beforeInfo[p].Mode() {
			t.Fatalf("%s changed", p)
		}
	}
	ctx := context.Background()
	claude, _ := st.GetAgent(ctx, agentOf(t, op, claudeID))
	capture := claude.ImportedFrom.Capture
	if filepath.Dir(capture) != folders.AgentRoot(s.WorkRoot, claude.ID) {
		t.Fatalf("the copy is in the Agent's folder: %s", capture)
	}
	if got, _ := os.ReadFile(capture); string(got) != string(complete) {
		t.Fatalf("the copy ends at the last complete record:\n%s", got)
	}
	codex, _ := st.GetAgent(ctx, agentOf(t, op, codexID))
	var base struct {
		ThreadID   string `json:"threadId"`
		LastTurnID string `json:"lastTurnId"`
	}
	b, err := os.ReadFile(codex.ImportedFrom.Capture)
	if err != nil || json.Unmarshal(b, &base) != nil || base.ThreadID != codexID || base.LastTurnID != "t2" {
		t.Fatalf("Codex capture: %s %v", b, err)
	}
	page, _ := st.ListMessages(ctx, codex.ID, store.MessageQuery{Limit: 50})
	if len(page.Items) != 4 || page.Items[3].Text != "did two" {
		t.Fatalf("the history stops where the copy does: %d", len(page.Items))
	}
}

// A model the runtime no longer lists gives way to the runtime's default (the first model it lists).
func TestModelFallsBackToRuntimeDefault(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("models", "")
	claudeID := "q0000001-1111-4111-8111-111111111111"
	codexID := "q0000002-2222-4222-8222-222222222222"
	p := f.claude(claudeID, repo, time.Hour, "x")
	b, _ := os.ReadFile(p)
	f.write(strings.TrimPrefix(p, f.home+"/"), strings.ReplaceAll(string(b), "claude-opus-5-5", "claude-3-legacy"))
	f.codexRollout(codexID, repo, "gpt-4-retired", "y")
	s, st := importEnv(t, f)
	op := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{claudeID, codexID}})
	ctx := context.Background()
	claude, _ := st.GetAgent(ctx, agentOf(t, op, claudeID))
	codex, _ := st.GetAgent(ctx, agentOf(t, op, codexID))
	if claude.ModelID != "fable" || codex.ModelID != "gpt-6-luna" {
		t.Fatalf("defaults: %s / %s", claude.ModelID, codex.ModelID)
	}
}

// What cannot be imported is refused before anything is done: an unknown scan, more than 100 conversations or 4 GiB
// in a folder, a folder the scan did not find. A conversation the scan listed as not importable is an error of its
// own while the rest are imported.
func TestImportRefusals(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("r", "")
	f.claude("r0000001-1111-4111-8111-111111111111", repo, time.Hour, "ok")
	empty := "r0000002-1111-4111-8111-111111111111"
	f.write(".claude/projects/-r/"+empty+".jsonl", lines(map[string]any{"type": "assistant", "sessionId": empty, "cwd": repo, "message": map[string]any{"role": "assistant", "content": "x"}}))
	s, _ := importEnv(t, f)
	ctx := context.Background()
	scan := doneScan(t, s)
	if _, err := s.StartImport(ctx, apitypes.ConversationImportInput{ScanID: uuid.NewString(), Folders: []apitypes.ConversationImportFolder{{Path: repo, SessionIDs: []string{"x"}}}}); err == nil {
		t.Fatal("unknown scan")
	}
	if _, err := s.StartImport(ctx, apitypes.ConversationImportInput{ScanID: scan.OperationID, Folders: []apitypes.ConversationImportFolder{{Path: "/nowhere", SessionIDs: []string{"x"}}}}); err == nil {
		t.Fatal("unknown folder")
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprint(i)
	}
	if _, err := s.StartImport(ctx, apitypes.ConversationImportInput{ScanID: scan.OperationID, Folders: []apitypes.ConversationImportFolder{{Path: repo, SessionIDs: many}}}); err == nil {
		t.Fatal("over 100")
	}
	s.MaxFolderBytes = 10
	if _, err := s.StartImport(ctx, apitypes.ConversationImportInput{ScanID: scan.OperationID, Folders: []apitypes.ConversationImportFolder{{Path: repo, SessionIDs: []string{"r0000001-1111-4111-8111-111111111111"}}}}); err == nil {
		t.Fatal("over the size limit")
	}
	s.MaxFolderBytes = 0
	op := runImport(t, s, scan.OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{"r0000001-1111-4111-8111-111111111111", empty, "missing"}})
	if op.State != "done" || len(op.Agents) != 1 || len(op.Errors) != 2 || op.Total != 3 || op.Done != 3 {
		t.Fatalf("partial import: %+v", op)
	}
}

// A Claude Code conversation imported while it is mid-reply is copied up to its last finished turn (the reply ended,
// and the records about that end): the fork never resumes inside a turn, and the chat shows what the copy holds. One
// with no finished turn is not copied: the Agent starts a new session, and the chat still shows the conversation.
func TestClaudeCaptureEndsAtLastFinishedTurn(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("midturn", "")
	id := "u0000001-1111-4111-8111-111111111111"
	at := f.at(time.Hour)
	rec := func(m map[string]any) map[string]any {
		m["sessionId"], m["cwd"], m["timestamp"] = id, repo, at
		return m
	}
	user := func(text string) map[string]any {
		return rec(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
	}
	finished := lines(
		user("first question"),
		rec(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "model": "claude-opus-5-5", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "first answer"}}}}),
		rec(map[string]any{"type": "system", "subtype": "turn_duration", "durationMs": 1200}),
	)
	unfinished := lines(
		user("second question"),
		rec(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "model": "claude-opus-5-5", "stop_reason": "tool_use",
			"content": []any{map[string]any{"type": "text", "text": "looking"}, map[string]any{"type": "tool_use", "id": "t1", "name": "SendMessage", "input": map[string]any{}}}}}),
		rec(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": "sent"}}}}),
		rec(map[string]any{"type": "api-request", "requestId": "r1"}),
	)
	f.write(".claude/projects/-midturn/"+id+".jsonl", finished+unfinished)
	none := "u0000002-1111-4111-8111-111111111111"
	f.write(".claude/projects/-midturn/"+none+".jsonl", strings.ReplaceAll(unfinished, id, none))
	s, st := importEnv(t, f)
	op := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{id, none}})
	if op.State != "done" || len(op.Errors) != 0 {
		t.Fatalf("import: %+v", op)
	}
	ctx := context.Background()
	a, _ := st.GetAgent(ctx, agentOf(t, op, id))
	if got, _ := os.ReadFile(a.ImportedFrom.Capture); string(got) != finished {
		t.Fatalf("the copy ends after the last finished turn:\n%s", got)
	}
	page, _ := st.ListMessages(ctx, a.ID, store.MessageQuery{Limit: 50})
	if len(page.Items) != 2 || page.Items[1].Text != "first answer" {
		t.Fatalf("the chat shows what the copy holds: %d", len(page.Items))
	}
	b, _ := st.GetAgent(ctx, agentOf(t, op, none))
	if b.ImportedFrom.Capture != "" {
		t.Fatalf("nothing finished, nothing copied: %q", b.ImportedFrom.Capture)
	}
	if _, err := os.Stat(filepath.Join(folders.AgentRoot(s.WorkRoot, b.ID), claudeCaptureFile)); !os.IsNotExist(err) {
		t.Fatalf("no copy left behind: %v", err)
	}
	if page, _ := st.ListMessages(ctx, b.ID, store.MessageQuery{Limit: 50}); len(page.Items) != 2 || page.Items[0].Text != "second question" {
		t.Fatalf("the chat still shows the conversation: %d", len(page.Items))
	}
}

// A conversation Loop Now ran a turn in is that Agent's own: the fork an imported Agent continues in (Claude Code or
// Codex, in the same folder as the original) and an Agent's own session there are listed with that Agent and importing
// them gives it back; one whose Agent is gone is not listed at all.
func TestAgentsOwnConversationsAreTheirs(t *testing.T) {
	f := newFixture(t)
	repo := f.repo("forked", "")
	orig := "f0000001-1111-4111-8111-111111111111"
	f.claude(orig, repo, 3*time.Hour, "original work")
	s, st := importEnv(t, f)
	first := runImport(t, s, doneScan(t, s).OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{orig}})
	agentID := agentOf(t, first, orig)
	var run uuid.UUID
	if err := st.Pool.QueryRow(context.Background(), `SELECT id FROM runs WHERE agent_id = $1 LIMIT 1`, agentID).Scan(&run); err != nil {
		t.Fatal(err)
	}
	line := "Run ID for this turn: " + run.String() + ".\n\n"
	fork, own, codexFork, gone := "f0000002-1111-4111-8111-111111111111", "f0000003-1111-4111-8111-111111111111",
		"f0000004-1111-4111-8111-111111111111", "f0000005-1111-4111-8111-111111111111"
	f.claude(fork, repo, time.Hour, "original work", line+"go on")
	f.claude(own, repo, time.Hour, line+"hello")
	f.codexRollout(codexFork, repo, "gpt-6", "earlier", line+"go on")
	f.claude(gone, repo, time.Hour, "earlier", "Run ID for this turn: "+uuid.NewString()+".\n\ngo on")
	scan := doneScan(t, s)
	listed := map[string]*uuid.UUID{}
	for _, c := range scan.Folders[0].Conversations {
		listed[c.SessionID] = c.AgentID
	}
	for _, id := range []string{orig, fork, own, codexFork} {
		if a, ok := listed[id]; !ok || a == nil || *a != agentID {
			t.Errorf("%s is the Agent's: %v", id, a)
		}
	}
	if _, ok := listed[gone]; ok || len(scan.Folders) != 1 {
		t.Errorf("one whose Agent is gone is not listed: %+v", scan.Folders)
	}
	again := runImport(t, s, scan.OperationID, apitypes.ConversationImportFolder{Path: repo, SessionIDs: []string{fork, own, codexFork}})
	for _, id := range []string{fork, own, codexFork} {
		if a := agentOf(t, again, id); a != agentID {
			t.Errorf("importing %s gives the Agent back: %s", id, a)
		}
	}
	agents, err := st.ListAgents(context.Background(), store.ProjectScope(first.Projects[0].ProjectID))
	if err != nil || len(agents) != 1 {
		t.Fatalf("still one Agent: %d %v", len(agents), err)
	}
}
