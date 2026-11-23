package modelcatalog_test

import (
	"testing"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/modelcatalog"
)

// The published catalog always has the local CLI claude-code: its models are Claude Code aliases, its thinking levels
// are the effort values (no off), medium when none is chosen, and images are supported; its id is reserved: a custom
// provider with the same id in models.json is not published, and plugins cannot register this id either.
func TestClaudeCodeProviderIsPublished(t *testing.T) {
	base := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", Models: []modelcatalog.Model{{ID: "claude-x", Name: "X"}}},
		{ID: agentruntime.ClaudeCodeProvider, Source: "config", Models: []modelcatalog.Model{{ID: "custom", Name: "Custom"}}},
	})
	reg := modelcatalog.NewRegistry(base)
	p, ok := reg.Provider(agentruntime.ClaudeCodeProvider)
	if !ok || p.Source != modelcatalog.SourceLocalCLI || p.Name != "Claude Code" || len(p.AuthMethods) != 0 {
		t.Fatalf("claude-code provider: %+v %v", p, ok)
	}
	for _, id := range []string{"fable", "opus", "sonnet", "haiku"} {
		if !reg.HasModel(agentruntime.ClaudeCodeProvider, id) || !reg.SupportsImages(agentruntime.ClaudeCodeProvider, id) {
			t.Fatalf("model %s should be in the catalog and support images", id)
		}
	}
	if reg.HasModel(agentruntime.ClaudeCodeProvider, "custom") {
		t.Fatal("a custom provider with the same id in models.json should not be published")
	}
	if level, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "sonnet", nil); err != nil || level != "medium" {
		t.Fatalf("level when none is chosen: %q %v", level, err)
	}
	for _, level := range []string{"low", "xhigh", "max"} {
		if _, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "opus", &level); err != nil {
			t.Fatalf("level %s: %v", level, err)
		}
	}
	for _, level := range []string{"off", "minimal"} {
		if _, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "opus", &level); err == nil {
			t.Fatalf("Claude Code has no level %s", level)
		}
	}
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "squatter", Provider: modelcatalog.CapturedProvider{ID: agentruntime.ClaudeCodeProvider, OAuth: true, Models: []modelcatalog.Model{{ID: "m", Name: "M", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}}})
	if p, _ := reg.Provider(agentruntime.ClaudeCodeProvider); p.Source != modelcatalog.SourceLocalCLI {
		t.Fatalf("a plugin must not take over claude-code: %+v", p)
	}
	if _, ok := reg.Base().Provider(agentruntime.ClaudeCodeProvider); !ok {
		t.Fatal("Base should also include the local CLI provider, used to check plugin providers for id clashes")
	}
	if agentruntime.ForProvider(agentruntime.ClaudeCodeProvider).ID != agentruntime.ClaudeCode || agentruntime.ForProvider("anthropic").ID != agentruntime.Pi {
		t.Fatal("the runtime should be decided by the provider")
	}
}
