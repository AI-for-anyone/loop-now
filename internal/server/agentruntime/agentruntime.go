// Package agentruntime defines an Agent's runtime: whether an Agent is run by the bundled Pi or by Claude Code
// installed on the user's computer. The runtime is not stored separately; the model provider decides it: provider
// claude-code means Claude Code, everything else is Pi.
package agentruntime

// Runtimes, matching AgentRuntime in the OpenAPI spec.
const (
	Pi         = "pi"
	ClaudeCode = "claude-code"
)

// ClaudeCodeProvider is the model provider id of the Claude Code runtime: models come from the claude installed on the
// user's computer, using its own login.
const ClaudeCodeProvider = "claude-code"

// ForProvider returns which runtime runs an Agent that uses the model provider named provider.
func ForProvider(provider string) string {
	if provider == ClaudeCodeProvider {
		return ClaudeCode
	}
	return Pi
}
