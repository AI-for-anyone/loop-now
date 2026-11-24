package httpapi_test

import (
	"context"
	"testing"

	"loop-now/internal/server/claudecli"

	"loop-now/internal/server/runtimes"
	"loop-now/internal/testutil/fakeclaudebin"
)

// GET /runtimes is console only: the status of pi and claude-code is returned per the contract; refresh must be a boolean.
func TestListRuntimes(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	e.api.Runtimes = &runtimes.Checker{PiVersion: func() string { return "1.4.0" }, ClaudeBin: fakeclaudebin.Path(t), RealHome: func() (string, error) { return home, nil },
		HelperEnv: func(_ context.Context, home string) []string {
			return claudecli.Env(claudecli.EnvOptions{Home: home, User: "tester", Path: []string{"/usr/bin", "/bin"}})
		}}
	got := decode[struct {
		Runtimes []runtimes.Status `json:"runtimes"`
	}](t, e.expect(200, "GET", "/runtimes?refresh=true", nil))
	if len(got.Runtimes) != 5 || got.Runtimes[1].ID != "claude-code" || !got.Runtimes[1].Available || got.Runtimes[1].Login.State != "logged_in" ||
		got.Runtimes[2].ID != "codex" || got.Runtimes[2].Available || *got.Runtimes[2].UnavailableReason != "not_found" ||
		got.Runtimes[3].ID != "grok" || got.Runtimes[3].Available || *got.Runtimes[3].UnavailableReason != "not_found" ||
		got.Runtimes[4].ID != "antigravity" || got.Runtimes[4].Available || *got.Runtimes[4].UnavailableReason != "not_found" {
		t.Fatalf("runtimes: %+v", got)
	}
	e.expect(422, "GET", "/runtimes?refresh=maybe", nil, "X-Contract-Invalid-Request", "1")
}

// GET /runtime-definitions is console only and returns the registry per the contract, the default runtime first,
// without checking anything on this computer (no Checker is set).
func TestListRuntimeDefinitions(t *testing.T) {
	e := newEnv(t)
	got := decode[struct {
		Runtimes []struct {
			ID           string         `json:"id"`
			Name         string         `json:"name"`
			Providers    []string       `json:"providers"`
			IsDefault    bool           `json:"isDefault"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"runtimes"`
	}](t, e.expect(200, "GET", "/runtime-definitions", nil))
	if len(got.Runtimes) != 5 || got.Runtimes[0].ID != "pi" ||
		got.Runtimes[3].ID != "grok" || got.Runtimes[3].Name != "Grok" || len(got.Runtimes[3].Providers) != 1 || got.Runtimes[3].Providers[0] != "grok-cli" ||
		got.Runtimes[4].ID != "antigravity" || got.Runtimes[4].Name != "Antigravity" || len(got.Runtimes[4].Providers) != 1 || got.Runtimes[4].Providers[0] != "antigravity-cli" || !got.Runtimes[0].IsDefault || len(got.Runtimes[0].Providers) != 0 ||
		got.Runtimes[2].ID != "codex" || got.Runtimes[2].Name != "Codex" || len(got.Runtimes[2].Providers) != 1 || got.Runtimes[2].Providers[0] != "codex-cli" ||
		got.Runtimes[1].ID != "claude-code" || got.Runtimes[1].Name != "Claude Code" || got.Runtimes[1].IsDefault ||
		len(got.Runtimes[1].Providers) != 1 || got.Runtimes[1].Providers[0] != "claude-code" {
		t.Fatalf("runtime definitions: %+v", got)
	}
	if caps := got.Runtimes[1].Capabilities; caps["accounts"] != "local" || caps["realHome"] != true || caps["bundled"] != false || len(caps) != 5 {
		t.Fatalf("claude-code capabilities: %v", caps)
	}
}

// Checking the runtimes again (refresh=true) also reads the local CLIs' model lists again, so a CLI installed or
// logged in after startup shows its models without waiting; a plain read does not.
func TestRuntimesRefreshReadsModelsAgain(t *testing.T) {
	e := newEnv(t)
	calls := 0
	e.api.RefreshLocalModels = func(context.Context) { calls++ }
	var nudged []string
	e.api.ModelListRead = func(provider string) { nudged = append(nudged, provider) }
	e.expect(200, "GET", "/runtimes", nil)
	if calls != 0 {
		t.Fatalf("a plain read must not wait for the models: %d", calls)
	}
	if len(nudged) != 1 || nudged[0] != "" {
		t.Fatalf("a plain read lets old model lists and login conclusions be read again in the background: %v", nudged)
	}
	e.expect(200, "GET", "/runtimes?refresh=true", nil)
	if calls != 1 {
		t.Fatalf("refresh=true reads the models again: %d", calls)
	}
}
