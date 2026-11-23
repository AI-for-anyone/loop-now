package agentconfig_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"loop-now/internal/server/agentconfig"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// localRuntimeEnv is newEnv with the Claude Code provider in the catalog, so an Agent can switch to a runtime that runs
// on its own CLI's local login (capability accounts = local).
func localRuntimeEnv(t *testing.T, scope func(*env) store.Scope) *env {
	t.Helper()
	e := newEnv(t)
	e.svc.Models = modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", AuthMethods: []string{"api_key"}, APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X"}}},
		modelcatalog.ClaudeCodeProvider(),
	})
	if scope != nil {
		e.scope = scope(e)
	}
	return e
}

func switchModel(t *testing.T, e *env, id store.Agent, provider, model string) {
	t.Helper()
	if _, err := e.svc.UpdateAgent(context.Background(), id.ID, agentconfig.AgentPatch{Provider: &provider, ModelID: &model}); err != nil {
		t.Fatalf("switch to %s: %v", provider, err)
	}
}

// noModelCredential fails unless the snapshot carries no model credential of any kind.
func noModelCredential(t *testing.T, snap agentconfig.Snapshot) {
	t.Helper()
	if snap.ModelBinding != nil || snap.ModelCredential.Kind() != "" || len(snap.ModelEnv) != 0 {
		t.Fatalf("a local-login runtime got a model credential: binding %v, kind %q, env %v", snap.ModelBinding, snap.ModelCredential.Kind(), snap.ModelEnv)
	}
}

// A platform Agent with a model key (the console's managed key) that switches to Claude Code runs without reading or
// checking that key; CLI credentials are still injected. Switching back to the provider of the key uses it again.
func TestLocalRuntimeIgnoresPlatformModelKey(t *testing.T) {
	e := localRuntimeEnv(t, func(*env) store.Scope { return store.PlatformScope() })
	ctx := context.Background()
	a := e.agent(t)
	coordinator := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coordinator, e.svc.Models, modelauth.ServiceOptions{MaxCredential: 4096}, nil)
	defer auth.Close(ctx)
	e.svc.ModelAuth = auth
	meta, err := auth.PutKey(ctx, a.ID, modelauth.KeyInput{Provider: "anthropic", Key: "synthetic-platform-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutCLICredential(ctx, a.ID, "gh", map[string]string{"token": "synthetic-gh"}); err != nil {
		t.Fatal(err)
	}
	switchModel(t, e, a, "claude-code", "sonnet")
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil {
		t.Fatalf("Claude Code with a stored model key: %v", err)
	}
	noModelCredential(t, snap)
	if snap.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-gh" {
		t.Fatalf("CLI credentials must still be injected: %v", snap.CLIPayloads)
	}
	switchModel(t, e, a, "anthropic", "claude-x")
	snap, err = e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.ModelBinding == nil || snap.ModelBinding.VersionID != *meta.VersionID {
		t.Fatalf("back on anthropic the stored key must be used again: %+v %v", snap.ModelBinding, err)
	}
	if encoded, err := snap.ModelCredential.AuthFile("anthropic"); err != nil || !strings.Contains(string(encoded), "synthetic-platform-key") {
		t.Fatalf("back on anthropic: %v", err)
	}
}

// The same for a platform Agent whose model key is a single key from before managed keys (a model row with environment
// variables): Claude Code ignores it, and the old provider reads it as before.
func TestLocalRuntimeIgnoresLegacyModelKey(t *testing.T) {
	e := localRuntimeEnv(t, func(*env) store.Scope { return store.PlatformScope() })
	ctx := context.Background()
	a := e.agent(t)
	if _, err := e.svc.PutModelCredential(ctx, a.ID, map[string]string{"ANTHROPIC_API_KEY": "synthetic-legacy-key"}); err != nil {
		t.Fatal(err)
	}
	switchModel(t, e, a, "claude-code", "sonnet")
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil {
		t.Fatalf("Claude Code with a stored single key: %v", err)
	}
	noModelCredential(t, snap)
	switchModel(t, e, a, "anthropic", "claude-x")
	snap, err = e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.ModelEnv["ANTHROPIC_API_KEY"] != "synthetic-legacy-key" || snap.ModelCredential.Kind() != "api_key" {
		t.Fatalf("back on anthropic the single key must be used again: %v %q %v", snap.ModelEnv, snap.ModelCredential.Kind(), err)
	}
}

// A project Agent's model accounts are chosen per provider, and no project account can exist for the Claude Code
// provider (it accepts neither keys nor logins), so its model slot is never ambiguous: an Agent whose anthropic slot
// needs a choice (two accounts) runs on Claude Code without a model credential. Back on anthropic the slot is checked
// again, and it resolves once one account is left.
func TestLocalRuntimeProjectAgentModelAccounts(t *testing.T) {
	e := localRuntimeEnv(t, nil)
	ctx := context.Background()
	a := e.agent(t)
	coordinator := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coordinator, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	project := modelauth.NewProjectService(auth, e.svc.Templates, 4096)
	e.svc.ModelAuth = auth
	provider := "anthropic"
	first, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "model", Provider: &provider, Name: "A", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-A"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "model", Provider: &provider, Name: "B", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-B"}})
	if err != nil {
		t.Fatal(err)
	}
	claude := "claude-code"
	if _, err := project.Create(ctx, *a.ProjectID, cm.Create{Purpose: "model", Provider: &claude, Name: "C", Credential: cm.CredentialInput{Type: "api_key", Key: "synthetic-C"}}); err == nil {
		t.Fatal("a project account for the Claude Code provider must be rejected")
	}
	switchModel(t, e, a, "claude-code", "sonnet")
	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil {
		t.Fatalf("Claude Code with an ambiguous anthropic slot: %v", err)
	}
	noModelCredential(t, snap)
	switchModel(t, e, a, "anthropic", "claude-x")
	if _, err = e.svc.ExecutionSnapshot(ctx, a.ID); !errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		t.Fatalf("back on anthropic the ambiguous slot must be checked again: %v", err)
	}
	if _, err = project.Delete(ctx, *a.ProjectID, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}
	snap, err = e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || snap.ModelBinding == nil || snap.ModelBinding.AccountID == nil || *snap.ModelBinding.AccountID != first.ID {
		t.Fatalf("back on anthropic with one account: %+v %v", snap.ModelBinding, err)
	}
}
