package agentruntime

import (
	"slices"
	"testing"
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
	if r, ok := ByID(ClaudeCode); !ok || r.Name != "Claude Code" || !Known(Pi) || Known("codex") || Known("") {
		t.Fatal("ByID/Known")
	}
	r, _ := ByID(ClaudeCode)
	r.Providers[0] = "changed"
	if ForProvider(ClaudeCodeProvider).ID != ClaudeCode {
		t.Fatal("ByID must return a copy")
	}
}
