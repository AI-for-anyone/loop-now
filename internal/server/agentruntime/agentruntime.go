// Package agentruntime 定义 Agent 的运行时：执行一个 Agent 的是随包的 Pi，还是用户本机安装的 Claude Code。
// 运行时不单独保存，由模型提供方决定：提供方为 claude-code 时是 Claude Code，其余都是 Pi。
package agentruntime

// 运行时，与 OpenAPI 的 AgentRuntime 一致。
const (
	Pi         = "pi"
	ClaudeCode = "claude-code"
)

// ClaudeCodeProvider 是 Claude Code 运行时的模型提供方 id：模型由用户本机安装的 claude 提供，用它自己的登录。
const ClaudeCodeProvider = "claude-code"

// ForProvider 返回选用模型提供方 provider 的 Agent 由哪个运行时执行。
func ForProvider(provider string) string {
	if provider == ClaudeCodeProvider {
		return ClaudeCode
	}
	return Pi
}
