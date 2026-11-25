package agentruntime

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

// The registry is well formed: unique ids and names, exactly one default runtime listed first, no provider claimed by
// two runtimes, and session commands taken from SessionCommands.
func TestRegistry(t *testing.T) {
	all := All()
	if len(all) == 0 || !all[0].IsDefault() {
		t.Fatal("the default runtime must come first")
	}
	ids, names, providers := map[string]bool{}, map[string]bool{}, map[string]string{}
	defaults := 0
	for _, r := range all {
		if r.ID == "" || r.Name == "" || ids[r.ID] || names[r.Name] {
			t.Fatalf("id and name must be set and unique: %+v", r)
		}
		ids[r.ID], names[r.Name] = true, true
		if r.IsDefault() {
			defaults++
		}
		for _, p := range r.Providers {
			if other, ok := providers[p]; ok {
				t.Fatalf("provider %s is listed by %s and %s", p, other, r.ID)
			}
			providers[p] = r.ID
		}
		for _, c := range r.Capabilities.SessionCommands {
			if !slices.Contains(SessionCommands, c) {
				t.Fatalf("%s: unknown session command %s", r.ID, c)
			}
		}
		if r.Capabilities.Accounts != AccountsManaged && r.Capabilities.Accounts != AccountsLocal {
			t.Fatalf("%s: accounts %q", r.ID, r.Capabilities.Accounts)
		}
	}
	if defaults != 1 {
		t.Fatalf("want exactly one default runtime, got %d", defaults)
	}
}

// ForProvider: a provider listed by a runtime goes to it, every other provider to the default runtime; ByID and Known
// find only registered ids; the results are copies.
func TestLookup(t *testing.T) {
	if got := ForProvider(ClaudeCodeProvider).ID; got != ClaudeCode {
		t.Fatalf("claude-code provider: %s", got)
	}
	for _, p := range []string{"anthropic", "openai-codex", "", "some-plugin"} {
		if got := ForProvider(p).ID; got != Pi {
			t.Fatalf("provider %q: %s", p, got)
		}
	}
	if r, ok := ByID(ClaudeCode); !ok || r.Name != "Claude Code" || !Known(Pi) || !Known(Codex) || !Known(Grok) || !Known(Antigravity) || Known("unknown-runtime") || Known("") {
		t.Fatal("ByID/Known")
	}
	r, _ := ByID(ClaudeCode)
	r.Providers[0] = "changed"
	if ForProvider(ClaudeCodeProvider).ID != ClaudeCode {
		t.Fatal("ByID must return a copy")
	}
}

// A runtime that is not bundled says how to install it and log in on each operating system; the bundled Pi needs
// nothing.
func TestSetupCommands(t *testing.T) {
	pi, _ := ByID(Pi)
	if pi.SetupOn("darwin") != nil {
		t.Fatal("the bundled Pi needs no setup")
	}
	cc, _ := ByID(ClaudeCode)
	for goos, install := range map[string]string{
		"darwin":  "curl -fsSL https://claude.ai/install.sh | bash",
		"linux":   "curl -fsSL https://claude.ai/install.sh | bash",
		"windows": "irm https://claude.ai/install.ps1 | iex",
	} {
		s := cc.SetupOn(goos)
		if s == nil || s.Install == nil || *s.Install != install || s.Login == nil || *s.Login != "claude auth login" || s.DocsURL == nil {
			t.Fatalf("%s: %+v", goos, s)
		}
	}
}

// Codex is installed with npm on every system, logs in with codex login, and needs at least the version tested.
func TestCodexSetup(t *testing.T) {
	cx, ok := ByID(Codex)
	if !ok || cx.MinVersion != "0.167.0" || ForProvider(CodexProvider).ID != Codex {
		t.Fatalf("registry entry: %+v", cx)
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		s := cx.SetupOn(goos)
		if s == nil || *s.Install != "npm install -g @openai/codex" || *s.Login != "codex login" || s.DocsURL == nil {
			t.Fatalf("%s: %+v", goos, s)
		}
	}
}

// A local ACP command is a runtime of its own, after the registry's, running the provider of its own id; the registry
// hands out copies.
func TestLocalCommands(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	rt := LocalCommandRuntime(id, "My Agent", LocalCommand{Command: "my-agent", Args: []string{"acp"}, Env: map[string]string{"K": "v"}})
	SetLocalCommands([]Runtime{rt})
	t.Cleanup(func() { SetLocalCommands(nil) })
	rid := LocalCommandRuntimeID(id)
	all := All()
	if last := all[len(all)-1]; last.ID != rid || last.Name != "My Agent" || last.CommandID != id || last.IsDefault() {
		t.Fatalf("listed last: %+v", last)
	}
	got, ok := ByID(rid)
	if !ok || got.Command == nil || got.Command.Command != "my-agent" || got.Capabilities.Accounts != AccountsLocal || got.Capabilities.TaskFork {
		t.Fatalf("by id: %+v %v", got, ok)
	}
	if ForProvider(rid).ID != rid || !Known(rid) {
		t.Fatal("its provider runs on it")
	}
	got.Command.Args[0], got.Command.Env["K"] = "changed", "changed"
	if again, _ := ByID(rid); again.Command.Args[0] != "acp" || again.Command.Env["K"] != "v" {
		t.Fatalf("a copy: %+v", again.Command)
	}
	SetLocalCommands(nil)
	if Known(rid) || ForProvider(rid).ID != Pi {
		t.Fatal("removed")
	}
}

// The runtimes whose accounts are local write texts with AI and the bundled Pi does not; Automatic tries Claude Code,
// Codex, Antigravity and Grok, then the local ACP commands in their order, each with its small model.
func TestTextWriters(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	SetLocalCommands([]Runtime{LocalCommandRuntime(a, "First", LocalCommand{Command: "first"}), LocalCommandRuntime(b, "Second", LocalCommand{Command: "second"})})
	defer SetLocalCommands(nil)
	for _, r := range All() {
		if (r.TextGeneration != nil) != (r.Capabilities.Accounts == AccountsLocal) {
			t.Errorf("%s: writes texts %v, accounts %s", r.ID, r.TextGeneration != nil, r.Capabilities.Accounts)
		}
	}
	var order []string
	for _, r := range Writers() {
		order = append(order, r.ID)
	}
	want := []string{ClaudeCode, Codex, Antigravity, Grok, LocalCommandRuntimeID(a), LocalCommandRuntimeID(b)}
	if !slices.Equal(order, want) {
		t.Fatalf("Automatic's order: %v, want %v", order, want)
	}
	for id, tg := range map[string]TextGeneration{
		ClaudeCode:  {Models: []string{"haiku"}},
		Codex:       {Models: []string{"luna", "-mini"}, Level: "low"},
		Antigravity: {Models: []string{"gemini-4-neon", "flash"}, Level: LowestLevel},
		Grok:        {},
	} {
		r, _ := ByID(id)
		if got := *r.TextGeneration; !slices.Equal(got.Models, tg.Models) || got.Level != tg.Level {
			t.Errorf("%s: small model %+v, want %+v", id, got, tg)
		}
	}
	r, _ := ByID(LocalCommandRuntimeID(a))
	if len(r.TextGeneration.Models) != 0 || r.TextGeneration.Level != "" {
		t.Errorf("a local command writes with its default model: %+v", r.TextGeneration)
	}
	// A copy does not share the registry's entry.
	r, _ = ByID(ClaudeCode)
	r.TextGeneration.Models[0] = "changed"
	if again, _ := ByID(ClaudeCode); again.TextGeneration.Models[0] != "haiku" {
		t.Fatal("ByID must return a copy")
	}
}

// Every runtime but the bundled Pi can work in a folder the person picks; Claude Code and Codex can also load the
// person's own CLI configuration. Every runtime offers Full access; Pi offers nothing else.
func TestWorkingFolderAndLocalConfigCapabilities(t *testing.T) {
	want := map[string][2]bool{Pi: {false, false}, ClaudeCode: {true, true}, Codex: {true, true}, Grok: {true, false}, Antigravity: {true, false}}
	for _, r := range All() {
		w, ok := want[r.ID]
		if !ok {
			continue
		}
		if c := r.Capabilities; c.WorkingDirectory != w[0] || c.LocalConfig != w[1] || !slices.Contains(c.PermissionModes, "full") {
			t.Errorf("%s: %+v", r.ID, c)
		}
	}
	if pi, _ := ByID(Pi); !slices.Equal(pi.Capabilities.PermissionModes, []string{"full"}) {
		t.Errorf("Pi offers Full access only: %v", pi.Capabilities.PermissionModes)
	}
	cmd := LocalCommandRuntime(uuid.New(), "Mine", LocalCommand{Command: "/bin/true"})
	if c := cmd.Capabilities; !c.WorkingDirectory || c.LocalConfig || !slices.Contains(c.PermissionModes, "full") {
		t.Errorf("a local ACP command: %+v", c)
	}
}

// The permission modes each runtime offers: Claude Code, Codex and Antigravity all four, Grok no Auto-accept edits, a
// local ACP command Supervised and Full access, Pi Full access only.
func TestPermissionModesPerRuntime(t *testing.T) {
	all := []string{"supervised", "accept_edits", "auto", "full"}
	want := map[string][]string{Pi: {"full"}, ClaudeCode: all, Codex: all, Antigravity: all, Grok: {"supervised", "auto", "full"}}
	for id, modes := range want {
		r, _ := ByID(id)
		if !slices.Equal(r.Capabilities.PermissionModes, modes) {
			t.Errorf("%s: %v, want %v", id, r.Capabilities.PermissionModes, modes)
		}
	}
	cmd := LocalCommandRuntime(uuid.New(), "Mine", LocalCommand{Command: "/bin/true"})
	if !slices.Equal(cmd.Capabilities.PermissionModes, []string{"supervised", "full"}) {
		t.Errorf("a local ACP command: %v", cmd.Capabilities.PermissionModes)
	}
}
