package modelcatalog

import (
	"slices"

	"loop-now/internal/server/agentruntime"
)

// CodexModel is one model the Codex installed on the user's computer lists for its login (model/list).
type CodexModel struct {
	ID, Name string
	// Efforts are the reasoning efforts the model supports, as Codex names them; DefaultEffort is its default.
	Efforts       []string
	DefaultEffort string
	Images        bool
	Hidden        bool
}

// thinkingLevels are the thinking levels the platform knows, in order; Codex efforts outside them (such as ultra) are
// left out.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// CodexProvider is the catalog of the Codex runtime: the models the codex on this computer lists for its login, without
// hidden ones (none until Codex was asked, or when it is not installed). A model's thinking levels are its reasoning
// efforts that the platform knows; its default is Codex's default when known, otherwise medium, otherwise the lowest.
// Context window and maximum output are not listed by Codex and stay 0.
func CodexProvider(models []CodexModel) Provider {
	p := Provider{ID: agentruntime.CodexProvider, Name: "Codex", Source: SourceLocalCLI, AuthMethods: []string{}, APIKeyEnvVars: []string{}, Models: []Model{}}
	for _, m := range models {
		if m.Hidden || m.ID == "" {
			continue
		}
		var levels []string
		for _, level := range thinkingLevels {
			if slices.Contains(m.Efforts, level) {
				levels = append(levels, level)
			}
		}
		model := Model{ID: m.ID, Name: m.Name, Reasoning: len(levels) > 0, Input: []string{"text"}}
		if model.Name == "" {
			model.Name = m.ID
		}
		if m.Images {
			model.Input = append(model.Input, "image")
		}
		if len(levels) == 0 {
			levels = []string{"off"}
		}
		model.ThinkingLevels = levels
		switch {
		case slices.Contains(levels, m.DefaultEffort):
			model.DefaultThinkingLevel = m.DefaultEffort
		case slices.Contains(levels, "medium"):
			model.DefaultThinkingLevel = "medium"
		default:
			model.DefaultThinkingLevel = levels[0]
		}
		p.Models = append(p.Models, model)
	}
	return p
}
