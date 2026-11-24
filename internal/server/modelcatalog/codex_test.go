package modelcatalog

import (
	"slices"
	"testing"

	"loop-now/internal/server/agentruntime"
)

// The codex-cli provider has no models until the local Codex was asked; then it has the models Codex lists, without
// hidden ones, with the reasoning efforts the platform knows as thinking levels.
func TestCodexModels(t *testing.T) {
	r := NewRegistry(New("1.4.0", nil, nil))
	p, ok := r.Provider(agentruntime.CodexProvider)
	if !ok || len(p.Models) != 0 || p.Source != SourceLocalCLI || p.Name != "Codex" || len(p.AuthMethods) != 0 {
		t.Fatalf("before Codex was asked: %+v", p)
	}
	r.SetLocalProviders(agentruntime.Codex, []Provider{CodexProvider([]CodexModel{
		{ID: "gpt-test", Name: "GPT Test", Efforts: []string{"low", "medium", "high", "xhigh", "ultra"}, DefaultEffort: "medium", Images: true},
		{ID: "gpt-mini", Name: "GPT Mini", Efforts: []string{"low"}, DefaultEffort: "ultra"},
		{ID: "gpt-hidden", Name: "Hidden", Hidden: true},
	})})
	p, _ = r.Provider(agentruntime.CodexProvider)
	if len(p.Models) != 2 {
		t.Fatalf("models: %+v", p.Models)
	}
	m := p.Models[0]
	if m.ID != "gpt-test" || m.Name != "GPT Test" || !m.Reasoning || !slices.Equal(m.ThinkingLevels, []string{"low", "medium", "high", "xhigh"}) ||
		m.DefaultThinkingLevel != "medium" || !slices.Equal(m.Input, []string{"text", "image"}) {
		t.Fatalf("model: %+v", m)
	}
	if mini := p.Models[1]; mini.DefaultThinkingLevel != "low" || !slices.Equal(mini.Input, []string{"text"}) {
		t.Fatalf("a default the platform does not know falls back to a known level: %+v", mini)
	}
	if !r.HasModel(agentruntime.CodexProvider, "gpt-test") || r.SupportsImages(agentruntime.CodexProvider, "gpt-mini") {
		t.Fatal("catalog lookups")
	}
	if level, err := r.ResolveThinking(agentruntime.CodexProvider, "gpt-test", nil); err != nil || level != "medium" {
		t.Fatalf("default level: %q %v", level, err)
	}
	r.Replace(New("1.4.0", nil, nil))
	if p, _ := r.Provider(agentruntime.CodexProvider); len(p.Models) != 2 {
		t.Fatal("replacing the Pi catalog keeps the models Codex listed")
	}
}
