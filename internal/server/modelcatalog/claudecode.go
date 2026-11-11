package modelcatalog

import (
	"slices"

	"loop-now/internal/server/agentruntime"
)

// SourceLocalCLI 是用户本机安装的 CLI 提供的模型：由对应的运行时执行，用 CLI 自己的登录，不需要账号。
const SourceLocalCLI = "local_cli"

// claudeCodeEfforts 是 Claude Code 的 --effort 取值，直接作为这些模型的思考等级（没有 off 与 minimal）。
var claudeCodeEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// ClaudeCodeProvider 是 Claude Code 运行时的模型目录：模型 id 是 Claude Code 的别名，随 Claude Code 指向各系列最新的模型。
// 上下文窗口与最大输出取随包 Pi 目录里 Claude 5 系列的值；默认等级 medium 与平台"未选时按 medium"一致。
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

// withLocal 返回 base 加上本机 CLI 的提供方（Claude Code）。本机提供方的 id 是保留的：base 里同 id 的（models.json 里自定义的）不发布。
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
