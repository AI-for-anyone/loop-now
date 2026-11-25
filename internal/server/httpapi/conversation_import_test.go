package httpapi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/convimport"
	"loop-now/internal/server/modelcatalog"
)

// writeClaudeSession leaves a Claude Code session in cwd under the projects folder.
func writeClaudeSession(t *testing.T, projects, id, cwd, prompt string) {
	t.Helper()
	dir := filepath.Join(projects, "-x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	user, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": prompt}})
	reply, _ := json.Marshal(map[string]any{"type": "assistant", "sessionId": id, "cwd": cwd, "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": "assistant", "model": "claude-opus-5-5", "content": []any{map[string]any{"type": "text", "text": "ok"}}}})
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), append(append(user, '\n'), append(reply, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A scan of this computer's conversations starts at once (202) and is followed with GET until it is done; the answers
// match the contract.
func TestConversationScanEndpoints(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	cwd := filepath.Join(home, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(home, ".claude", "projects")
	writeClaudeSession(t, projects, "k0000001-1111-4111-8111-111111111111", cwd, "hello there")
	e.api.ConvImport = &convimport.Service{Store: e.st, ClaudeProjects: func(context.Context) string { return projects },
		CodexHome: func() string { return filepath.Join(home, ".codex") }}
	started := decode[apitypes.ConversationScan](t, e.expect(202, "POST", "/conversation-scans", map[string]any{}))
	var scan apitypes.ConversationScan
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		scan = decode[apitypes.ConversationScan](t, e.expect(200, "GET", "/conversation-scans/"+started.OperationID, nil))
		if scan.State != "running" {
			break
		}
	}
	if scan.State != "done" || len(scan.Folders) != 1 || scan.Folders[0].Path != cwd || len(scan.Folders[0].Conversations) != 1 || scan.Folders[0].Conversations[0].Title != "hello there" {
		t.Fatalf("scan: %+v", scan)
	}
	e.expect(404, "GET", "/conversation-scans/00000000-0000-4000-8000-000000000000", nil)
	e.expect(422, "POST", "/conversation-scans", map[string]any{"path": "relative"})
}

// Importing what a scan found starts at once (202) and is followed with GET; the Agent it made reads with its source,
// working folder and own configuration, and its history as messages of origin imported. All answers match the contract.
func TestConversationImportEndpoints(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	cwd := filepath.Join(home, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(home, ".claude", "projects")
	id := "s0000001-1111-4111-8111-111111111111"
	writeClaudeSession(t, projects, id, cwd, "hello there")
	e.api.ConvImport = &convimport.Service{Store: e.st, ClaudeProjects: func(context.Context) string { return projects },
		CodexHome: func() string { return filepath.Join(home, ".codex") }, WorkRoot: t.TempDir(), OrgID: e.dc.Organization.ID,
		Models: modelcatalog.New("1", nil, []modelcatalog.Provider{modelcatalog.ClaudeCodeProvider()})}
	started := decode[apitypes.ConversationScan](t, e.expect(202, "POST", "/conversation-scans", map[string]any{}))
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if decode[apitypes.ConversationScan](t, e.expect(200, "GET", "/conversation-scans/"+started.OperationID, nil)).State != "running" {
			break
		}
	}
	e.expect(422, "POST", "/conversation-imports", map[string]any{"scanId": started.OperationID, "folders": []any{map[string]any{"path": "/elsewhere", "projectId": nil, "sessionIds": []string{id}}}})
	e.expect(404, "POST", "/conversation-imports", map[string]any{"scanId": "00000000-0000-4000-8000-000000000000", "folders": []any{map[string]any{"path": cwd, "projectId": nil, "sessionIds": []string{id}}}})
	op := decode[apitypes.ConversationImport](t, e.expect(202, "POST", "/conversation-imports", map[string]any{"scanId": started.OperationID,
		"folders": []any{map[string]any{"path": cwd, "projectId": e.dc.Project.ID, "sessionIds": []string{id}}}}))
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && op.State == "running"; time.Sleep(20 * time.Millisecond) {
		op = decode[apitypes.ConversationImport](t, e.expect(200, "GET", "/conversation-imports/"+op.OperationID, nil))
	}
	if op.State != "done" || len(op.Agents) != 1 || len(op.Projects) != 1 || op.Projects[0].ProjectID != e.dc.Project.ID {
		t.Fatalf("import: %+v", op)
	}
	agent := decode[apitypes.Agent](t, e.expect(200, "GET", "/agents/"+op.Agents[0].AgentID.String(), nil))
	if agent.ImportedFrom == nil || agent.ImportedFrom.SessionID != id || agent.WorkingDirectory == nil || *agent.WorkingDirectory != cwd ||
		!agent.UseLocalConfig || agent.PermissionMode != "full" || agent.Runtime != "claude-code" || agent.Model.ModelID != "opus" {
		t.Fatalf("Agent: %+v", agent)
	}
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", "/agents/"+agent.ID.String()+"/messages", nil))
	if len(page.Messages) != 2 || page.Messages[0].Origin != "imported" || page.Messages[0].Text != "hello there" || page.Messages[1].Text != "ok" || page.TurnsTotal != 1 {
		t.Fatalf("messages: %+v", page)
	}
	e.expect(404, "GET", "/conversation-imports/00000000-0000-4000-8000-000000000000", nil)
}
