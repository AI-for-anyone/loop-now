package modelauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
)

// platformAgent creates a platform Agent whose model provider is provider and, when granted is true, grants it the
// plugin grokish.
func (e *pluginEnv) platformAgent(t *testing.T, name, provider string, granted bool) store.Agent {
	t.Helper()
	ctx := context.Background()
	a, err := e.st.CreateAgent(ctx, store.PlatformScope(), name, provider, "fake-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if granted {
		if err := e.st.SaveAgentPluginGrant(ctx, a.ID, "grokish", true, nil); err != nil {
			t.Fatal(err)
		}
		if a, err = e.st.GetAgent(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func (e *pluginEnv) waitAgent(t *testing.T, agent, id uuid.UUID, ok func(Operation) bool) Operation {
	t.Helper()
	var last Operation
	waitAuth(t, func() bool {
		o, err := e.svc.Get(context.Background(), agent, id)
		last = o
		return err == nil && ok(o)
	})
	return last
}

// loginAgent runs one plugin provider login of a platform Agent to the end (answering the multiple-choice question) and
// returns the credential after success.
func (e *pluginEnv) loginAgent(t *testing.T, a store.Agent) Metadata {
	t.Helper()
	ctx := context.Background()
	cur, err := e.st.GetAgent(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	op, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: cur.ConfigVersion})
	if err != nil {
		t.Fatal(err)
	}
	e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return o.Prompt != nil })
	if _, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "1", Value: "device"}); err != nil {
		t.Fatal(err)
	}
	if done := e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return terminal(o.Status) }); done.Status != "succeeded" {
		t.Fatalf("login: %+v", done)
	}
	st, err := e.svc.State(ctx, a.ID)
	if err != nil || st.Credential == nil {
		t.Fatalf("credential after login: %+v %v", st, err)
	}
	return *st.Credential
}

// A platform Agent logs in to a plugin provider in its own model credential: questions, the authorization URL and the
// device code are given through the operation view (the device code only in memory), and only the current question can
// be answered; on success one version is added to this Agent's model auth versions, the "credential" records only the
// plugin, and the state directory is named after the version ID; the sweeper keeps the current version, and old
// directories can be deleted after a re-login, clearing the credential or deleting the Agent.
func TestPlatformAgentPluginLogin(t *testing.T) {
	e := newPluginEnv(t, false)
	ctx := context.Background()
	var changed atomic.Int32
	e.svc.Changed = func(uuid.UUID) { changed.Add(1) }
	a := e.platformAgent(t, "Platform assistant", "fake-oauth", true)

	e.helper.gate = make(chan struct{})
	op, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: a.ConfigVersion})
	if err != nil {
		t.Fatal(err)
	}
	if op.Provider != "fake-oauth" || op.DeviceCode != nil {
		t.Fatalf("start: %+v", op)
	}
	asked := e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return o.Prompt != nil })
	if q := asked.Prompt; q.Kind != "select" || len(q.Options) != 2 {
		t.Fatalf("prompt: %+v", asked)
	}
	if st, err := e.svc.State(ctx, a.ID); err != nil || st.Operation == nil || st.Operation.Prompt == nil {
		t.Fatalf("the state should also carry the prompt: %+v %v", st, err)
	}
	if _, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "9", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("not the current prompt: %v", err)
	}
	if _, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "1", Value: "pigeon"}); fieldOf(err) != "value" {
		t.Fatalf("not an option: %v", err)
	}
	if _, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "1", Value: strings.Repeat("x", 4097)}); fieldOf(err) != "value" {
		t.Fatalf("answer too long: %v", err)
	}
	other := e.platformAgent(t, "Another assistant", "fake-oauth", true)
	if _, err := e.svc.Input(ctx, other.ID, op.ID, cm.LoginInput{PromptID: "1", Value: "device"}); !store.IsNotFound(err) {
		t.Fatalf("another Agent should not be able to answer: %v", err)
	}
	after, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "1", Value: "device"})
	if err != nil || after.Operation == nil || after.Operation.Prompt != nil {
		t.Fatalf("the prompt should clear after answering: %+v %v", after, err)
	}
	waiting := e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return o.DeviceCode != nil })
	if waiting.Status != "awaiting_authorization" || waiting.DeviceCode.UserCode != "ABCD-EFGH" || waiting.DeviceCode.VerificationURI != "https://accounts.example.test/device" ||
		waiting.DeviceCode.ExpiresAt == nil || waiting.AuthorizationURL == nil || *waiting.AuthorizationURL != "https://accounts.example.test/authorize?state=s" {
		t.Fatalf("in-progress view: %+v", waiting)
	}
	close(e.helper.gate) // once closed, later logins no longer pause
	done := e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return terminal(o.Status) })
	if done.Status != "succeeded" || done.DeviceCode != nil || done.Prompt != nil || done.AuthorizationURL != nil || done.ErrorCode != nil {
		t.Fatalf("login result: %+v", done)
	}
	// The notification comes only after the commit (not at the same moment the operation becomes terminal); wait for
	// it.
	waitAuth(t, func() bool { return changed.Load() == 1 })
	st, err := e.svc.State(ctx, a.ID)
	if err != nil || st.Credential == nil {
		t.Fatalf("credential: %+v %v", st, err)
	}
	meta := *st.Credential
	if meta.Status != "set" || meta.Provider == nil || *meta.Provider != "fake-oauth" || meta.AuthMethod == nil || *meta.AuthMethod != "oauth" || meta.VersionID == nil || meta.ProjectID != nil {
		t.Fatalf("credential metadata: %+v", meta)
	}
	first := *meta.VersionID
	version, err := e.st.GetModelAuthVersion(ctx, first)
	if err != nil || version.AgentID != a.ID || version.ProjectAccountID != nil {
		t.Fatalf("the version should belong to this Agent: %+v %v", version, err)
	}
	c, err := modelcredential.Decode(version.Secret)
	if err != nil || c.Kind() != modelcredential.PluginState || c.Plugin() != "grokish" {
		t.Fatalf("the version should record only the plugin: %v %v", c.Kind(), err)
	}
	if _, err := os.Stat(filepath.Join(e.states.Dir(first), providerstate.AuthFile)); err != nil {
		t.Fatalf("state directory and auth.json: %v", err)
	}
	if pending, _ := filepath.Glob(filepath.Join(e.states.Root, ".pending-*")); len(pending) != 0 {
		t.Fatalf("the staging directory should have been renamed: %v", pending)
	}

	// Re-login: new version, brand-new state directory; the sweeper keeps only the current version.
	second := *e.loginAgent(t, a).VersionID
	if second == first || e.helper.homes[0] == e.helper.homes[1] {
		t.Fatal("a re-login should use a new version and a brand-new state directory")
	}
	keep, err := e.st.ProviderStateVersionsInUse(ctx)
	if err != nil || !keep[second] || keep[first] {
		t.Fatalf("versions to keep: %v %v", keep, err)
	}
	removed, err := e.states.Sweep(func() (map[uuid.UUID]bool, error) { return keep, nil }, true)
	if err != nil || len(removed) != 1 || removed[0] != first {
		t.Fatalf("sweep old versions: %v %v", removed, err)
	}
	// Clear the credential: the current version is no longer kept either.
	if err := e.svc.Clear(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if keep, _ = e.st.ProviderStateVersionsInUse(ctx); keep[second] {
		t.Fatal("should no longer be kept after clearing the credential")
	}
	// Delete the Agent: its current version is no longer kept.
	third := *e.loginAgent(t, a).VersionID
	if keep, _ = e.st.ProviderStateVersionsInUse(ctx); !keep[third] {
		t.Fatal("the current version should be kept")
	}
	if err := e.st.SoftDeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if keep, _ = e.st.ProviderStateVersionsInUse(ctx); keep[third] {
		t.Fatal("should no longer be kept after deleting the Agent")
	}
}

// Refusals and failures when a platform Agent logs in to a plugin provider: not granted is 409, wrong provider is 422,
// components not assembled is unavailable; extension load failure and plugin unavailable end with an error code and
// delete the staging directory; after cancelling, no more answers are accepted. A project Agent still uses the project
// account as before.
func TestPlatformAgentPluginLoginRefusalsAndFailures(t *testing.T) {
	e := newPluginEnv(t, false)
	ctx := context.Background()

	ungranted := e.platformAgent(t, "Not granted", "fake-oauth", false)
	if _, err := e.svc.Start(ctx, ungranted.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: ungranted.ConfigVersion}); !apperr.Is(err, apperr.CodePluginNotEnabled) || !strings.Contains(err.Error(), "is not granted") {
		t.Fatalf("not granted: %v", err)
	}
	a := e.platformAgent(t, "Platform assistant", "fake-oauth", true)
	if _, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "nope", ExpectedConfigVersion: a.ConfigVersion}); fieldOf(err) != "provider" {
		t.Fatalf("not in the catalog: %v", err)
	}
	mismatch := e.platformAgent(t, "Wrong provider", "openai-codex", true)
	if _, err := e.svc.Start(ctx, mismatch.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: mismatch.ConfigVersion}); fieldOf(err) != "provider" {
		t.Fatalf("does not match the Agent's current provider: %v", err)
	}
	project, err := e.st.CreateAgent(ctx, store.ProjectScope(e.project), "Project assistant", "fake-oauth", "fake-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(ctx, project.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: project.ConfigVersion}); err == nil {
		t.Fatal("a project Agent uses project accounts and should not log in to its own credential")
	}
	saved := e.p.Plugins
	e.p.Plugins = nil
	if _, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: a.ConfigVersion}); !errors.Is(err, ErrHelperUnavailable) {
		t.Fatalf("login components not assembled: %v", err)
	}
	e.p.Plugins = saved

	fails := func(code string) {
		t.Helper()
		op, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: a.ConfigVersion})
		if err != nil {
			t.Fatal(err)
		}
		done := e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return terminal(o.Status) })
		if done.Status != "failed" || done.ErrorCode == nil || *done.ErrorCode != code || done.ErrorMessage == nil || *done.ErrorMessage != pluginErrorMessages[code] {
			t.Fatalf("%s: %+v", code, done)
		}
		if entries, _ := os.ReadDir(e.states.Root); len(entries) != 0 {
			t.Fatalf("a failure should delete the staging directory: %v", entries)
		}
	}
	e.helper.fail = ErrExtensionLoad
	fails("extension_load_failed")
	e.helper.fail = nil
	extensions := saved.Extensions
	saved.Extensions = func(context.Context, string) ([]string, error) { return nil, apperr.NotFound("plugin") }
	fails("plugin_unavailable")
	saved.Extensions = extensions

	op, err := e.svc.Start(ctx, a.ID, LoginInput{Provider: "fake-oauth", ExpectedConfigVersion: a.ConfigVersion})
	if err != nil {
		t.Fatal(err)
	}
	e.waitAgent(t, a.ID, op.ID, func(o Operation) bool { return o.Prompt != nil })
	end, err := e.svc.Cancel(ctx, a.ID, op.ID)
	if err != nil || end.Status != "cancelled" || end.Prompt != nil || end.ErrorCode != nil {
		t.Fatalf("cancel: %+v %v", end, err)
	}
	if _, err := e.svc.Input(ctx, a.ID, op.ID, cm.LoginInput{PromptID: "1", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("answering after cancel should fail: %v", err)
	}
	waitAuth(t, func() bool { entries, _ := os.ReadDir(e.states.Root); return len(entries) == 0 })
	if st, err := e.svc.State(ctx, a.ID); err != nil || st.Credential != nil {
		t.Fatalf("failure and cancel should leave no credential: %+v %v", st, err)
	}
}
