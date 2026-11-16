package modelcatalog

import (
	"slices"

	"loop-now/internal/server/agentruntime"
)

// SourceLocalCLI marks models provided by a CLI installed on the user's computer: they are run by the matching runtime,
// use the CLI's own login, and need no account.
const SourceLocalCLI = "local_cli"

// claudeCodeEfforts are Claude Code's --effort values, used directly as the thinking levels of these models (there is
// no off or minimal).
var claudeCodeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// ClaudeCodeProvider is the model catalog of the Claude Code runtime: model ids are Claude Code aliases, which follow
// Claude Code to the latest model of each family. Context window and max output take the values of the Claude 5 family
// in the bundled Pi catalog; the default level medium matches the platform's "medium when none is chosen" rule.
func ClaudeCodeProvider() Provider {
	model := func(id, name string) Model {
		return Model{ID: id, Name: name, Reasoning: true, ThinkingLevels: slices.Clone(claudeCodeEfforts), DefaultThinkingLevel: "medium",
			ContextWindow: 1000000, MaxTokens: 128000, Input: []string{"text", "image"}}
	}
	return Provider{ID: agentruntime.ClaudeCodeProvider, Name: "Claude Code", Source: SourceLocalCLI, AuthMethods: []string{}, APIKeyEnvVars: []string{},
		Models: []Model{
			model("fable", "Claude Fable (latest)"),
			model("opus", "Claude Opus (latest)"),
			model("sonnet", "Claude Sonnet (latest)"),
			model("haiku", "Claude Haiku (latest)"),
		}}
}

// withLocal returns base plus the providers of local CLIs (Claude Code). The ids of local providers are reserved:
// providers in base with the same id (custom ones from models.json) are not published.
func withLocal(base *Catalog) *Catalog {
	if base == nil {
		return nil
	}
	providers := make([]Provider, 0, len(base.Providers)+1)
	for _, p := range base.Providers {
		if p.ID != agentruntime.ClaudeCodeProvider {
			providers = append(providers, p)
		}
	}
	c := New(base.PiVersion, base.GeneratedAt, append(providers, ClaudeCodeProvider()))
	c.Revision, c.providerConfig = base.Revision, base.providerConfig
	return c
}
