package modelcatalog

import (
	"slices"

	"loop-now/internal/server/agentruntime"
)

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
