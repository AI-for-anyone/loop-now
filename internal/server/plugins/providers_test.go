package plugins_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/plugins"
)

// fakeCapture returns the registered providers by the plugin directory of the extensions without running any code,
// and records every directory it is asked to read.
type fakeCapture struct {
	byDir map[string]modelcatalog.Capture
	dirs  []string
}

func (f *fakeCapture) capture(_ context.Context, dir string, extensions []string) (modelcatalog.Capture, error) {
	f.dirs = append(f.dirs, dir)
	return f.byDir[dir], nil
}

func provider(id string, oauth bool) modelcatalog.CapturedProvider {
	return modelcatalog.CapturedProvider{ID: id, Name: "Name " + id, Extension: "agent/index.ts", OAuth: oauth,
		Models: []modelcatalog.Model{{ID: "m1", Name: "M1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off", Input: []string{"text"}}}}
}

// Once an install is confirmed, the providers registered by the plugin are read: a new provider with OAuth is
// merged into the model catalog (source=plugin, with pluginId); one without OAuth, or with the same id as a built-in
// provider, is only recorded; when another plugin registers the same id it is not installed (409); after an
// uninstall the provider disappears from the catalog.
func TestPluginProvidersReachCatalog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "anthropic", Models: []modelcatalog.Model{{ID: "c", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}})
	reg := modelcatalog.NewRegistry(base)
	e.svc.Models = reg
	dirA, dirB := t.TempDir(), t.TempDir()
	plugin(t, dirA, "grokish", "0.1.0")
	plugin(t, dirB, "copycat", "0.1.0")
	override := provider("anthropic", true)
	override.OverridesBuiltin = true
	fc := &fakeCapture{byDir: map[string]modelcatalog.Capture{
		dirA: {Providers: []modelcatalog.CapturedProvider{provider("fake-oauth", true), provider("plain", false), override}, Errors: []modelcatalog.CaptureError{{Path: "agent/other.ts", Error: "boom"}}},
		dirB: {Providers: []modelcatalog.CapturedProvider{provider("fake-oauth", true)}},
	}}
	e.svc.CaptureProviders = fc.capture

	pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: dirA}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.dirs) != 0 {
		t.Fatalf("preview must not run plugin code: %v", fc.dirs)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Provider("fake-oauth")
	if !ok || p.Source != "plugin" || p.PluginID == nil || *p.PluginID != "grokish" || p.Name != "Name fake-oauth" || !slices.Equal(p.AuthMethods, []string{"oauth"}) || !reg.HasModel("fake-oauth", "m1") {
		t.Fatalf("a provider with OAuth should enter the catalog: %+v %v", p, ok)
	}
	if _, ok := reg.Provider("plain"); ok {
		t.Fatal("a provider without OAuth should not enter the catalog")
	}
	if a, _ := reg.Provider("anthropic"); a.Source == "plugin" {
		t.Fatal("a provider overriding a built-in one should not enter the catalog")
	}
	d, err := e.svc.Get(ctx, "grokish")
	if err != nil || len(d.Providers) != 3 || d.ProvidersError != nil || len(d.ProviderErrors) != 1 || d.ProviderErrors[0].Path != "agent/other.ts" {
		t.Fatalf("providers in the details: %+v %v", d, err)
	}
	in := map[string]bool{}
	for _, v := range d.Providers {
		in[v.ID] = v.InCatalog
	}
	if !in["fake-oauth"] || in["plain"] || in["anthropic"] {
		t.Fatalf("inCatalog: %v", in)
	}

	pv, _ = e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: dirB}})
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNameConflict {
		t.Fatalf("another plugin registering the same provider should get 409: %v", err)
	}
	if _, err := e.st.GetPlugin(ctx, "copycat"); err == nil {
		t.Fatal("should not install on a conflict")
	}

	// Server restart: merged in again from the database.
	reg2 := modelcatalog.NewRegistry(base)
	e.svc.Models = reg2
	if err := e.svc.PublishProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Provider("fake-oauth"); !ok {
		t.Fatal("should be merged in from the database after a restart")
	}
	// When models.json replaces the Pi catalog part, the plugin providers are kept.
	reg2.Replace(modelcatalog.New("1.4.0", nil, nil))
	if _, ok := reg2.Provider("fake-oauth"); !ok {
		t.Fatal("plugin providers should be kept after the Pi catalog is replaced")
	}
	// By default an uninstall keeps every project's subscription accounts and platform Agents' own logins; only when
	// deleting data is chosen are the logins of the providers it registered deleted.
	var deleted [][]string
	e.svc.DeleteProviderAccounts = func(_ context.Context, providers []string) error {
		deleted = append(deleted, slices.Clone(providers))
		return nil
	}
	if err := e.svc.Uninstall(ctx, "grokish", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Provider("fake-oauth"); ok {
		t.Fatal("should disappear from the catalog after uninstall")
	}
	if _, err := os.Stat(dirA); err != nil {
		t.Fatal("the local source directory should not be deleted")
	}
	if len(deleted) != 0 {
		t.Fatalf("a default uninstall should not delete logins: %v", deleted)
	}
	pv, err = e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: dirA}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Uninstall(ctx, "grokish", true); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || !slices.Contains(deleted[0], "fake-oauth") {
		t.Fatalf("deleting data should delete the logins of the providers it registered: %v", deleted)
	}
}

// A failed read does not block the install; the reason is saved with the version in providersError.
func TestPluginProviderCaptureFailureKeepsInstall(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Models = modelcatalog.NewRegistry(modelcatalog.New("1.4.0", nil, nil))
	e.svc.CaptureProviders = func(context.Context, string, []string) (modelcatalog.Capture, error) {
		return modelcatalog.Capture{}, os.ErrDeadlineExceeded
	}
	dir := plugin(t, t.TempDir(), "flaky", "0.1.0")
	e.install(plugins.Source{Kind: "local", Location: dir}, true)
	d, err := e.svc.Get(ctx, "flaky")
	if err != nil || d.ProvidersError == nil || len(d.Providers) != 0 {
		t.Fatalf("a failed read should record the reason: %+v %v", d, err)
	}
}

// At install time, a declared state path that overlaps a CLI template's credential file is pointed out ahead of time
// in the details' providerErrors (the plugin is still installed).
func TestProviderStateOverlapWarning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.ManagedHomePaths = func() []string { return []string{".config/gh/hosts.yml"} }
	dir := plugin(t, t.TempDir(), "overlapper", "0.1.0")
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]any
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	pkg["loopNow"].(map[string]any)["providerState"] = map[string]any{"fake-oauth": map[string]any{"paths": []any{".config/gh"}}, "fine": map[string]any{"paths": []any{".pi/fine"}}}
	b, _ = json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	e.install(plugins.Source{Kind: "local", Location: dir}, true)
	d, err := e.svc.Get(ctx, "overlapper")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.ProviderErrors) != 1 || d.ProviderErrors[0].Path != "providerState.fake-oauth" || !strings.Contains(d.ProviderErrors[0].Error, ".config/gh/hosts.yml") {
		t.Fatalf("overlap warning: %+v", d.ProviderErrors)
	}
	if len(d.ProviderState) != 2 {
		t.Fatalf("the declarations should still be saved: %+v", d.ProviderState)
	}
}
