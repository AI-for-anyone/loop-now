package local

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/testutil/fsperm"
)

// makeState creates a state version directory for a subscription account: auth.json and the extension's own state.
func makeState(t *testing.T, root string) (*agentconfig.ProviderState, string) {
	t.Helper()
	v := uuid.New()
	dir := filepath.Join(root, v.String())
	if err := os.MkdirAll(filepath.Join(dir, ".pi", "fake-oauth"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"fake-oauth":{"type":"oauth","access":"m","refresh":"m","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi", "fake-oauth", "accounts.json"), []byte(`{"token":"in-state"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return &agentconfig.ProviderState{PluginID: "grokish", VersionID: v, Dir: dir, Paths: []string{".pi/fake-oauth"}}, dir
}

func readLink(t *testing.T, p string) string {
	t.Helper()
	got, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("%s should be a link: %v", p, err)
	}
	return got
}

// Subscription account state: auth.json and the declared paths are linked to the account's state directory, and two
// Agents share one copy; after switching away the links are cleaned up and the state directory is untouched; it fails
// explicitly when the plugin is not mounted or the state directory is missing; releasing an Agent does not delete the
// state through a link.
func TestProviderStateLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows these are directory junctions and hard links; verified separately")
	}
	h := newHarness(t, nil, "")
	state, dir := makeState(t, t.TempDir())
	withState := func(s *agentconfig.Snapshot) {
		st := *state
		s.ProviderState = &st
		s.Plugins = agentconfig.PluginAssets{IDs: []string{"grokish"}}
	}
	a, b := h.agent(withState), h.agent(withState)
	for _, id := range []uuid.UUID{a, b} {
		if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("start: %+v", res)
		}
		d := h.e.dirs(id)
		if got := readLink(t, filepath.Join(d.pi, "auth.json")); got != filepath.Join(dir, "auth.json") {
			t.Fatalf("auth.json link: %s", got)
		}
		if got := readLink(t, filepath.Join(d.home, ".pi", "fake-oauth")); got != filepath.Join(dir, ".pi", "fake-oauth") {
			t.Fatalf("state path link: %s", got)
		}
		// The second message reuses the same Pi process: auth.json must not be rewritten like an ordinary credential
		// (after a real login the second message once failed because of this).
		if res, _ := h.exec(id, "again"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("second message on the reused process: %+v", res)
		}
		if got := readLink(t, filepath.Join(d.pi, "auth.json")); got != filepath.Join(dir, "auth.json") {
			t.Fatalf("auth.json link after reuse: %s", got)
		}
	}
	// A write through one Agent's HOME is visible to the other Agent and in the state directory (tokens the extension
	// refreshes itself are shared by the Agents of the same project).
	if err := os.WriteFile(filepath.Join(h.e.dirs(a).home, ".pi", "fake-oauth", "refreshed.json"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(h.e.dirs(b).home, ".pi", "fake-oauth", "refreshed.json")); string(got) != "new" {
		t.Fatalf("one shared copy of the state: %q", got)
	}
	if !h.e.ProviderStatesInUse()[state.VersionID] {
		t.Fatal("the version a running Pi uses should be reported to the cleaner")
	}

	// Switch to not using a subscription account: restart, the links are removed, auth.json goes back to an ordinary
	// file written by the platform, and the state directory is unchanged.
	h.src.update(a, func(s *agentconfig.Snapshot) { s.ProviderState, s.Plugins = nil, agentconfig.PluginAssets{} })
	if res, _ := h.exec(a, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("after switching away: %+v", res)
	}
	d := h.e.dirs(a)
	if info, err := os.Lstat(filepath.Join(d.pi, "auth.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("auth.json should be a regular file: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(d.home, ".pi", "fake-oauth")); !os.IsNotExist(err) {
		t.Fatalf("the old link should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.root, providerLinksFile)); !os.IsNotExist(err) {
		t.Fatal("the link record should be gone")
	}

	// Release the Agent: only its own directories are deleted; everything in the state directory is still there.
	if err := h.e.Release(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"auth.json", ".pi/fake-oauth/accounts.json", ".pi/fake-oauth/refreshed.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Fatalf("releasing an Agent must not touch the state directory: %s %v", f, err)
		}
	}

	// Plugin not mounted: fails explicitly.
	c := h.agent(func(s *agentconfig.Snapshot) { st := *state; s.ProviderState = &st })
	if res, _ := h.exec(c, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "grokish") {
		t.Fatalf("plugin not mounted: %+v", res)
	}
	// State directory missing: fails explicitly.
	e := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Dir = filepath.Join(t.TempDir(), "gone")
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	// A platform Agent's (no project context) subscription account is its own login, so the hint says to log in again
	// in its model credentials; for a project Agent the hint says to log in again in the project.
	if res, _ := h.exec(e, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "again in this Agent's model credentials") {
		t.Fatalf("state directory missing (platform Agent): %+v", res)
	}
	project := uuid.New()
	pe := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Dir = filepath.Join(t.TempDir(), "gone")
		s.ContextProjectID = &project
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	if res, _ := h.exec(pe, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "again in the project") {
		t.Fatalf("state directory missing (project Agent): %+v", res)
	}
	// A declared path covers the credential file of any CLI template (not only those this Agent configured): fail
	// explicitly, rather than erroring when credential files are deleted and rewritten on every turn.
	managed := h.e.tmpl.AllCredentialFilePaths()
	if len(managed) == 0 {
		t.Fatal("the built-in templates should have credential file paths")
	}
	cover := strings.SplitN(managed[0], "/", 2)[0]
	g := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Paths = []string{cover}
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	if res, _ := h.exec(g, "hi"); res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, managed[0]) {
		t.Fatalf("overlaps a template credential file: %+v", res)
	}
	// A declared path not yet written at login: create an empty directory in the state directory, then link it.
	f := h.agent(func(s *agentconfig.Snapshot) {
		st := *state
		st.Paths = []string{".config/fake-later"}
		s.ProviderState, s.Plugins = &st, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	if res, _ := h.exec(f, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("path not written yet: %+v", res)
	}
	if info, err := os.Stat(filepath.Join(dir, ".config", "fake-later")); err != nil || !info.IsDir() {
		t.Fatalf("an empty directory should be created in the state directory: %v", err)
	}
}

// When a declared path was not yet written at login and an empty directory must be created in the state directory:
// every newly created level is accessible only to the current user, not relying on inheritance from the parent (even
// when the parent is open to everyone); an existing level that is a link is rejected, so nothing is created outside
// the state directory through a link. Runs on all platforms.
func TestMkdirPrivate(t *testing.T) {
	root := t.TempDir()
	fsperm.Open(t, root)
	if runtime.GOOS == "windows" {
		plain := filepath.Join(root, "plain")
		if err := os.Mkdir(plain, 0o700); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Lstat(plain); err != nil || privatefs.IsPrivate(plain, info) {
			t.Fatalf("a directory created right inside the open directory should inherit the open ACL, or the tightening cannot be checked: %v", err)
		}
	}
	dir := filepath.Join(root, ".config", "fake-later")
	if err := mkdirPrivate(root, dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, ".config"), dir} {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || !privatefs.IsPrivate(p, info) || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
			t.Fatalf("%s should be a directory only the current user can access: %v", p, err)
		}
	}
	if err := mkdirPrivate(root, dir); err != nil {
		t.Fatalf("should succeed as usual when it already exists: %v", err)
	}
	if err := mkdirPrivate(root, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("a path outside the state directory should be rejected")
	}
	outside := t.TempDir()
	if err := linkDir(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := mkdirPrivate(root, filepath.Join(root, "linked", "x")); err == nil {
		t.Fatal("a path through a link should be rejected")
	}
	if _, err := os.Lstat(filepath.Join(outside, "x")); !os.IsNotExist(err) {
		t.Fatal("must not create anything outside the state directory through a link")
	}
}

// With “用本机的登录状态” ("Use local logins") on: no link is created, nothing is created or deleted in the user's
// real home directory; when a declared path does not exist there, it fails before startup; when it exists, the process
// starts and uses the local state, and auth.json is still linked to the account.
func TestProviderStateWithLocalLogins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("verified separately on Windows")
	}
	realHome, before := fakeRealHome(t)
	h := newHarness(t, func(o *Options) { o.RealHome = func() (string, error) { return realHome, nil } }, "")
	state, dir := makeState(t, t.TempDir())
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Logins: true}
		s.ProviderState, s.Plugins = state, agentconfig.PluginAssets{IDs: []string{"grokish"}}
	})
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable || !strings.Contains(res.ErrorMessage, "Use local logins") || !strings.Contains(res.ErrorMessage, ".pi/fake-oauth") ||
		!strings.Contains(res.ErrorMessage, "instead of this Agent's own login") {
		t.Fatalf("no local login for this extension: %+v", res)
	}
	sameTree(t, realHome, before)

	own := filepath.Join(realHome, ".pi", "fake-oauth")
	if err := os.MkdirAll(own, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "accounts.json"), []byte("users-own"), 0o600); err != nil {
		t.Fatal(err)
	}
	before = snapshotTree(t, realHome)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("start with a local login: %+v", res)
	}
	d := h.e.dirs(id)
	if got := readLink(t, filepath.Join(d.pi, "auth.json")); got != filepath.Join(dir, "auth.json") {
		t.Fatalf("auth.json should still link to the account: %s", got)
	}
	if _, err := os.Lstat(filepath.Join(d.home, ".pi", "fake-oauth")); !os.IsNotExist(err) {
		t.Fatalf("with the switch on no state link should be created in the Agent HOME: %v", err)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	sameTree(t, realHome, before)
}
