package modelcatalog

import (
	"slices"

	"loop-now/internal/server/agentruntime"
)

// LocalModel is one model a CLI installed on the user's computer lists for its login (Codex's model/list, Grok's
// initialize, agy models).
type LocalModel struct {
	ID, Name string
	// Efforts are the reasoning efforts the model supports, as the CLI names them; DefaultEffort is its default. None
	// means the thinking level cannot be chosen.
	Efforts       []string
	DefaultEffort string
	Images        bool
	Hidden        bool
	// LevelIDs: the CLI lists this model once per thinking level, as <ID>-<level> (Antigravity), and runs it by that id
	// (see MergeLevelModels and LevelModelID); its Efforts are those levels.
	LevelIDs bool
}

// CodexModel is one model the Codex installed on the user's computer lists for its login (model/list).
type CodexModel = LocalModel

// thinkingLevels are the thinking levels the platform knows, in order; Codex efforts outside them (such as ultra) are
// left out.
var thinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// CodexProvider is the catalog of the Codex runtime: the models the codex on this computer lists for its login, without
// hidden ones (none until Codex was asked, or when it is not installed).
func CodexProvider(models []CodexModel) Provider {
	return LocalCLIProvider(agentruntime.CodexProvider, "Codex", models)
}

// GrokProvider and AntigravityProvider are the catalogs of the Grok and Antigravity runtimes: the models the grok and
// agy on this computer list for their logins.
func GrokProvider(models []LocalModel) Provider {
	return LocalCLIProvider(agentruntime.GrokProvider, "Grok", models)
}

func AntigravityProvider(models []LocalModel) Provider {
	return LocalCLIProvider(agentruntime.AntigravityProvider, "Antigravity", models)
}

// LocalCLIProvider is the catalog of a runtime whose CLI lists its own models, without hidden ones. A model's thinking
// levels are its reasoning efforts that the platform knows (only off when it lists none); its default is the CLI's
// default when known, otherwise medium, otherwise the lowest. Context window and maximum output are not listed by these
// CLIs and stay 0.
func LocalCLIProvider(id, name string, models []LocalModel) Provider {
	p := Provider{ID: id, Name: name, Source: SourceLocalCLI, AuthMethods: []string{}, APIKeyEnvVars: []string{}, Models: []Model{}}
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
