package agentconfig_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// localAgentsEnv has Pi's anthropic provider, Claude Code and Codex in the catalog.
func localAgentsEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.svc.Models = modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", AuthMethods: []string{"api_key"}, APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X"}}},
		modelcatalog.ClaudeCodeProvider(),
		modelcatalog.CodexProvider([]modelcatalog.CodexModel{{ID: "gpt-6", Name: "GPT-6"}}),
	})
	return e
}

func strPtr(s string) *string { return &s }

func boolPtr(b bool) *bool { return &b }

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) == 0 {
		t.Fatalf("want a validation error, got %v", err)
	}
	return ae.Fields[0].Field
}

// Pi cannot work in a folder the person picks and runs with Full access only: an Agent cannot be created on Pi with a
// working folder or another mode, and an Agent with either cannot move to Pi. Other runtimes take a working folder.
func TestPiRefusedForLocalAgents(t *testing.T) {
	e := localAgentsEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	if _, err := e.svc.CreateAgentWithSettings(ctx, e.scope, "pi-folder", "anthropic", "claude-x", "", nil, nil,
		agentconfig.AgentSettings{WorkingDirectory: &dir}); fieldOf(t, err) != "workingDirectory" {
		t.Fatalf("Pi with a working folder: %v", err)
	}
	if _, err := e.svc.CreateAgentWithSettings(ctx, e.scope, "pi-mode", "anthropic", "claude-x", "", nil, nil,
		agentconfig.AgentSettings{PermissionMode: strPtr("supervised")}); fieldOf(t, err) != "permissionMode" {
		t.Fatalf("Pi with Supervised: %v", err)
	}
	a, err := e.svc.CreateAgentWithSettings(ctx, e.scope, "claude-folder", "claude-code", "opus", "", nil, nil,
		agentconfig.AgentSettings{WorkingDirectory: &dir, UseLocalConfig: boolPtr(true)})
	if err != nil || a.WorkingDirectory == nil || *a.WorkingDirectory != dir || !a.UseLocalConfig || a.PermissionMode != "full" {
		t.Fatalf("Claude Code with a working folder: %+v %v", a, err)
	}
	if _, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Provider: strPtr("anthropic"), ModelID: strPtr("claude-x")}); fieldOf(t, err) != "workingDirectory" {
		t.Fatalf("moving to Pi with a working folder: %v", err)
	}
	cleared, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{WorkingDirectorySet: true, Provider: strPtr("anthropic"), ModelID: strPtr("claude-x")})
	if err != nil || cleared.WorkingDirectory != nil || cleared.ModelProvider != "anthropic" {
		t.Fatalf("clearing the folder while moving to Pi: %+v %v", cleared, err)
	}
	if _, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{WorkingDirectory: &dir, WorkingDirectorySet: true}); fieldOf(t, err) != "workingDirectory" {
		t.Fatalf("a working folder on Pi: %v", err)
	}
	plain := e.agent(t)
	if plain.WorkingDirectory != nil || plain.UseLocalConfig || plain.PermissionMode != "full" {
		t.Fatalf("defaults change nothing: %+v", plain)
	}
}

// A working folder must be an existing folder, given as an absolute path; changing it changes the Agent's config
// version (its CLI restarts there with the next message).
func TestWorkingDirectoryChecks(t *testing.T) {
	e := localAgentsEnv(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative/dir", "/does/not/exist", file} {
		if _, err := e.svc.CreateAgentWithSettings(ctx, e.scope, "bad-"+uuid.NewString()[:6], "claude-code", "opus", "", nil, nil,
			agentconfig.AgentSettings{WorkingDirectory: &bad}); fieldOf(t, err) != "workingDirectory" {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	a, err := e.svc.CreateAgent(ctx, e.scope, "c", "claude-code", "opus", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	b, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{WorkingDirectory: &dir, WorkingDirectorySet: true, UseLocalConfig: boolPtr(true)})
	if err != nil || b.ConfigVersion != a.ConfigVersion+1 || *b.WorkingDirectory != dir || !b.UseLocalConfig {
		t.Fatalf("update: %+v %v", b, err)
	}
	if c, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{WorkingDirectory: &dir, WorkingDirectorySet: true}); err != nil || c.ConfigVersion != b.ConfigVersion {
		t.Fatalf("the same folder again changes nothing: %+v %v", c, err)
	}
}

// An imported Agent's first turn continues the copy taken at import, on the runtime of the conversation; once it has a
// session of its own, or on another runtime, it does not. The snapshot carries its folder and own configuration.
func TestImportedFirstTurnSnapshot(t *testing.T) {
	e := localAgentsEnv(t)
	ctx := context.Background()
	dir := t.TempDir()
	capture := filepath.Join(t.TempDir(), "imported-session.jsonl")
	a, err := e.st.CreateImportedAgent(ctx, store.NewImportedAgent{ID: uuid.Must(uuid.NewV7()), ProjectID: e.scope.ProjectID, Name: "imported",
		Provider: "claude-code", ModelID: "opus", WorkingDirectory: dir,
		From: store.ImportedFrom{CLI: "claude", SessionID: "s1", Path: "/orig.jsonl", ImportedAt: time.Now(), Capture: capture}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.ForkSessionFile != capture || snap.WorkingDirectory != dir || !snap.UseLocalConfig {
		t.Fatalf("first turn: %+v %v", snap, err)
	}
	switchModel(t, e, a, "codex-cli", "gpt-6")
	if snap, _ := e.svc.ExecutionSnapshot(ctx, a.ID); snap.ForkSessionFile != "" {
		t.Fatalf("a Claude Code copy is not forked by Codex: %q", snap.ForkSessionFile)
	}
	switchModel(t, e, a, "claude-code", "opus")
	if err := e.st.RecordSessionFile(ctx, a.ID, filepath.Join(t.TempDir(), "own.jsonl")); err != nil {
		t.Fatal(err)
	}
	if snap, _ := e.svc.ExecutionSnapshot(ctx, a.ID); snap.ForkSessionFile != "" {
		t.Fatalf("with a session of its own the copy is not forked again: %q", snap.ForkSessionFile)
	}
}

// With the person's own configuration an Agent uses this computer's logins and PATH, whatever the local-mode switches
// say, and none of the accounts Loop Now keeps for it.
func TestLocalConfigUsesOwnLogins(t *testing.T) {
	e := localAgentsEnv(t)
	ctx := context.Background()
	a, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "c", "claude-code", "opus", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutCLICredential(ctx, a.ID, "gh", map[string]string{"token": "synthetic-gh"}); err != nil {
		t.Fatal(err)
	}
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-gh" || snap.Local.Logins || snap.Local.UserPath {
		t.Fatalf("today's Agent: %+v %v", snap.Local, err)
	}
	if _, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{UseLocalConfig: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}
	snap, err = e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || len(snap.CLIs) != 0 || len(snap.CLIPayloads) != 0 || !snap.Local.Logins || !snap.Local.UserPath {
		t.Fatalf("own configuration: CLIs %v payloads %v local %+v %v", snap.CLIs, snap.CLIPayloads, snap.Local, err)
	}
}
