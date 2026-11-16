package modelauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// fakePluginHelper simulates a plugin extension's subscription login: it writes its own state under HOME, asks a
// multiple-choice question, gives an authorization URL and a device code, and returns a marker that contains no secret.
type fakePluginHelper struct {
	fail  error
	homes []string
	// gate, when non-nil, makes the helper wait after giving the device code until gate is closed (or the login is
	// cancelled) before returning; used to look at the in-progress view.
	gate chan struct{}
}

const fakeEntry = `{"type":"oauth","access":"vault-marker","refresh":"vault-marker","expires":9007199254740991}`

func (f *fakePluginHelper) Login(ctx context.Context, req PluginLoginRequest, ui PluginLoginUI, replies <-chan cm.LoginInput) ([]byte, error) {
	f.homes = append(f.homes, req.Home)
	if f.fail != nil {
		return nil, f.fail
	}
	if len(req.Extensions) != 1 || req.Extensions[0] != "/plugins/grokish/0.1.0/src/index.ts" || req.Provider != "fake-oauth" {
		return nil, errors.New("unexpected request")
	}
	dir := filepath.Join(req.Home, ".pi", "fake-oauth")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(`{"token":"real-token-in-state"}`), 0o644); err != nil {
		return nil, err
	}
	ui.Prompt(cm.LoginPrompt{ID: "1", Kind: "select", Message: "Pick", Options: []cm.LoginPromptOption{{ID: "browser", Label: "Browser"}, {ID: "device", Label: "Device"}}})
	select {
	case in := <-replies:
		if in.PromptID != "1" || in.Value != "device" {
			return nil, errors.New("unexpected answer")
		}
		ui.PromptClosed("1")
	case <-ctx.Done():
		return nil, ErrAuthCancelled
	}
	if err := ui.AuthURL("https://accounts.example.test/authorize?state=s"); err != nil {
		return nil, err
	}
	if err := ui.DeviceCode("ABCD-EFGH", "https://accounts.example.test/device", 10*time.Minute); err != nil {
		return nil, err
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ErrAuthCancelled
		}
	}
	return []byte(fakeEntry), nil
}

type pluginEnv struct {
	st      *store.Store
	p       *ProjectService
	svc     *Service
	helper  *fakePluginHelper
	states  *providerstate.Manager
	project uuid.UUID
}

func newPluginEnv(t *testing.T, enabled bool) *pluginEnv {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "Org", "Project")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/plugins/grokish/0.1.0"}`), Version: "0.1.0",
		Manifest: json.RawMessage(`{}`), InstallDir: "/plugins/grokish/0.1.0"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	if enabled {
		if _, _, err := st.SaveProjectPlugin(ctx, dc.Project.ID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
			t.Fatal(err)
		}
	}
	plugin := "grokish"
	models := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "openai-codex", AuthMethods: []string{"oauth"}},
		{ID: "fake-oauth", Name: "Fake", Source: "plugin", PluginID: &plugin, AuthMethods: []string{"oauth"}, Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}})
	templates, _ := clitemplate.Load("")
	svc := NewService(st, nil, nil, models, ServiceOptions{LoginTimeout: 5 * time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { closeAuth(t, svc) })
	e := &pluginEnv{st: st, svc: svc, helper: &fakePluginHelper{}, states: &providerstate.Manager{Root: filepath.Join(t.TempDir(), "provider-states")}, project: dc.Project.ID}
	e.p = NewProjectService(svc, templates, 0)
	e.p.Plugins = &PluginLogins{Helper: e.helper, States: e.states, Extensions: func(_ context.Context, id string) ([]string, error) {
		if id != "grokish" {
			return nil, apperr.NotFound("plugin")
		}
		return []string{"/plugins/grokish/0.1.0/src/index.ts"}, nil
	}}
	return e
}

func (e *pluginEnv) wait(t *testing.T, id uuid.UUID, ok func(cm.Operation) bool) cm.LoginState {
	t.Helper()
	var last cm.LoginState
	waitAuth(t, func() bool {
		s, err := e.p.GetLogin(context.Background(), e.project, id)
		last = s
		return err == nil && ok(s.Operation)
	})
	return last
}

func fieldOf(err error) string {
	if ae, ok := apperr.As(err); ok && len(ae.Fields) == 1 {
		return ae.Fields[0].Field
	}
	return ""
}

// Plugin provider login: the question is given through the operation view and only the current one can be answered; the
// authorization URL and device code are shown by the generic rules; on success a project account is created (oauth,
// logged in), the version records only the plugin, and the state directory is named after the version ID, has tightened
// permissions and contains auth.json; a re-login uses a brand-new directory.
func TestPluginProviderLogin(t *testing.T) {
	e := newPluginEnv(t, true)
	ctx := context.Background()
	name := "Subscription one"
	start, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	id := start.Operation.ID
	s := e.wait(t, id, func(o cm.Operation) bool { return o.Prompt != nil })
	if q := s.Operation.Prompt; q.Kind != "select" || len(q.Options) != 2 || s.Operation.Provider != "fake-oauth" {
		t.Fatalf("prompt: %+v", s.Operation)
	}
	if _, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "9", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("not the current prompt: %v", err)
	}
	if _, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "1", Value: "pigeon"}); fieldOf(err) != "value" {
		t.Fatalf("not an option: %v", err)
	}
	if after, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "1", Value: "device"}); err != nil || after.Operation.Prompt != nil {
		t.Fatalf("the prompt should clear after answering: %+v %v", after, err)
	}
	done := e.wait(t, id, func(o cm.Operation) bool { return terminal(o.Status) })
	if done.Operation.Status != "succeeded" || done.Account == nil || done.Operation.DeviceCode != nil || done.Operation.AuthorizationURL != nil {
		t.Fatalf("login result: %+v", done)
	}
	account := *done.Account
	if account.Provider == nil || *account.Provider != "fake-oauth" || account.AuthMethod == nil || *account.AuthMethod != "oauth" || account.Status != "logged_in" || account.CurrentVersionID == nil {
		t.Fatalf("account: %+v", account)
	}
	version, err := e.st.GetModelAuthVersion(ctx, *account.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := modelcredential.Decode(version.Secret)
	if err != nil || c.Kind() != modelcredential.PluginState || c.Plugin() != "grokish" {
		t.Fatalf("the version should record only the plugin: %v %v", c.Kind(), err)
	}
	dir := e.states.Dir(*account.CurrentVersionID)
	auth, err := os.ReadFile(filepath.Join(dir, providerstate.AuthFile))
	if err != nil || string(auth) != `{"fake-oauth":`+fakeEntry+`}` {
		t.Fatalf("auth.json: %s %v", auth, err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, ".pi", "fake-oauth"): 0o700, filepath.Join(dir, ".pi", "fake-oauth", "accounts.json"): 0o600, filepath.Join(dir, providerstate.AuthFile): 0o600} {
		// The state directory holds subscription tokens: on every platform only the current user may access it (Windows
		// checks the DACL), and on Unix the exact permission bits are checked as well.
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !privatefs.IsPrivate(path, info) || (runtime.GOOS != "windows" && info.Mode().Perm() != mode) {
			t.Fatalf("%s should be accessible only to the current user: %v", path, info.Mode())
		}
	}
	if pending, _ := filepath.Glob(filepath.Join(e.states.Root, ".pending-*")); len(pending) != 0 {
		t.Fatalf("the staging directory should have been renamed: %v", pending)
	}

	// In-progress view: given while waiting on the device code (here the list confirms that the same project can be
	// listed without provider).
	list, err := e.p.ListLogins(ctx, e.project, "", nil)
	if err != nil || len(list.Operations) != 0 {
		t.Fatalf("finished logins should not be in the in-progress list: %+v %v", list, err)
	}

	// Re-login: new version, new directory; the old directory is left to the sweeper.
	rev := account.Revision
	again, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", AccountID: &account.ID, ExpectedRevision: &rev})
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, again.Operation.ID, func(o cm.Operation) bool { return o.Prompt != nil })
	if _, err := e.p.Input(ctx, e.project, again.Operation.ID, cm.LoginInput{PromptID: "1", Value: "device"}); err != nil {
		t.Fatal(err)
	}
	relogin := e.wait(t, again.Operation.ID, func(o cm.Operation) bool { return terminal(o.Status) })
	if relogin.Operation.Status != "succeeded" || *relogin.Account.CurrentVersionID == *account.CurrentVersionID || relogin.Account.ID != account.ID {
		t.Fatalf("re-login: %+v", relogin)
	}
	if e.helper.homes[0] == e.helper.homes[1] {
		t.Fatal("a re-login should use a brand-new state directory")
	}
	if _, err := os.Stat(e.states.Dir(*relogin.Account.CurrentVersionID)); err != nil {
		t.Fatal(err)
	}

	// Sweep: only the account's current version is kept (versions used by running Pi processes and unfinished tasks are
	// added by the caller), and directories of old versions are deleted.
	keep, err := e.st.ProviderStateVersionsInUse(ctx)
	if err != nil || !keep[*relogin.Account.CurrentVersionID] || keep[*account.CurrentVersionID] {
		t.Fatalf("versions to keep: %v %v", keep, err)
	}
	removed, err := e.states.Sweep(func() (map[uuid.UUID]bool, error) { return keep, nil }, true)
	if err != nil || len(removed) != 1 || removed[0] != *account.CurrentVersionID {
		t.Fatalf("sweep: %v %v", removed, err)
	}
	if _, err := os.Stat(e.states.Dir(*account.CurrentVersionID)); !os.IsNotExist(err) {
		t.Fatal("the old version directory should have been deleted")
	}
	// Delete the account: the current version is no longer kept either.
	if _, err := e.p.Delete(ctx, e.project, account.ID, relogin.Account.Revision); err != nil {
		t.Fatal(err)
	}
	if keep, _ = e.st.ProviderStateVersionsInUse(ctx); keep[*relogin.Account.CurrentVersionID] {
		t.Fatal("should no longer be kept after deleting the account")
	}
	if removed, _ = e.states.Sweep(func() (map[uuid.UUID]bool, error) { return keep, nil }, false); len(removed) != 1 {
		t.Fatalf("sweep after deleting the account: %v", removed)
	}
}

func TestPluginProviderLoginRefusalsAndFailures(t *testing.T) {
	ctx := context.Background()
	name := "Subscription"
	off := newPluginEnv(t, false)
	if _, err := off.p.Start(ctx, off.project, cm.LoginStart{Provider: "fake-oauth", Name: &name}); !apperr.Is(err, apperr.CodePluginNotEnabled) {
		t.Fatalf("plugin not enabled in the project: %v", err)
	}
	if _, err := off.p.Start(ctx, off.project, cm.LoginStart{Provider: "nope", Name: &name}); fieldOf(err) != "provider" {
		t.Fatalf("not in the catalog: %v", err)
	}

	e := newPluginEnv(t, true)
	e.helper.fail = ErrExtensionLoad
	start, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	s := e.wait(t, start.Operation.ID, func(o cm.Operation) bool { return terminal(o.Status) })
	if s.Operation.Status != "failed" || s.Operation.ErrorCode == nil || *s.Operation.ErrorCode != "extension_load_failed" || s.Operation.ErrorMessage == nil {
		t.Fatalf("extension load failure: %+v", s.Operation)
	}
	if entries, _ := os.ReadDir(e.states.Root); len(entries) != 0 {
		t.Fatalf("a failure should delete the staging directory: %v", entries)
	}

	e.helper.fail = nil
	start, err = e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, start.Operation.ID, func(o cm.Operation) bool { return o.Prompt != nil })
	end, err := e.p.CancelLogin(ctx, e.project, start.Operation.ID)
	if err != nil || end.Operation.Status != "cancelled" || end.Operation.Prompt != nil {
		t.Fatalf("cancel: %+v %v", end, err)
	}
	if _, err := e.p.Input(ctx, e.project, start.Operation.ID, cm.LoginInput{PromptID: "1", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("answering after cancel should fail: %v", err)
	}
	waitAuth(t, func() bool { entries, _ := os.ReadDir(e.states.Root); return len(entries) == 0 })
}
