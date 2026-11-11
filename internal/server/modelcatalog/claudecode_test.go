package modelcatalog_test

import (
	"testing"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/modelcatalog"
)

// 发布的目录总有本机 CLI 的 claude-code：模型是 Claude Code 的别名，思考等级即 effort（没有 off），未选时为 medium，支持图片；
// 它的 id 是保留的：models.json 里同 id 的自定义提供方不发布，插件也不能注册这个 id。
func TestClaudeCodeProviderIsPublished(t *testing.T) {
	base := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", Models: []modelcatalog.Model{{ID: "claude-x", Name: "X"}}},
		{ID: agentruntime.ClaudeCodeProvider, Source: "config", Models: []modelcatalog.Model{{ID: "custom", Name: "Custom"}}},
	})
	reg := modelcatalog.NewRegistry(base)
	p, ok := reg.Provider(agentruntime.ClaudeCodeProvider)
	if !ok || p.Source != modelcatalog.SourceLocalCLI || p.Name != "Claude Code" || len(p.AuthMethods) != 0 {
		t.Fatalf("claude-code 提供方：%+v %v", p, ok)
	}
	for _, id := range []string{"fable", "opus", "sonnet", "haiku"} {
		if !reg.HasModel(agentruntime.ClaudeCodeProvider, id) || !reg.SupportsImages(agentruntime.ClaudeCodeProvider, id) {
			t.Fatalf("模型 %s 应在目录里且支持图片", id)
		}
	}
	if reg.HasModel(agentruntime.ClaudeCodeProvider, "custom") {
		t.Fatal("models.json 里同 id 的自定义提供方不应发布")
	}
	if level, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "sonnet", nil); err != nil || level != "medium" {
		t.Fatalf("未选时的等级：%q %v", level, err)
	}
	for _, level := range []string{"low", "xhigh", "max"} {
		if _, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "opus", &level); err != nil {
			t.Fatalf("等级 %s：%v", level, err)
		}
	}
	for _, level := range []string{"off", "minimal"} {
		if _, err := reg.ResolveThinking(agentruntime.ClaudeCodeProvider, "opus", &level); err == nil {
			t.Fatalf("Claude Code 没有等级 %s", level)
		}
	}
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "squatter", Provider: modelcatalog.CapturedProvider{ID: agentruntime.ClaudeCodeProvider, OAuth: true, Models: []modelcatalog.Model{{ID: "m", Name: "M", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}}})
	if p, _ := reg.Provider(agentruntime.ClaudeCodeProvider); p.Source != modelcatalog.SourceLocalCLI {
		t.Fatalf("插件不能占用 claude-code：%+v", p)
	}
	if _, ok := reg.Base().Provider(agentruntime.ClaudeCodeProvider); !ok {
		t.Fatal("Base 也要含本机 CLI 的提供方，判断插件提供方是否撞 id 时用")
	}
	if agentruntime.ForProvider(agentruntime.ClaudeCodeProvider) != agentruntime.ClaudeCode || agentruntime.ForProvider("anthropic") != agentruntime.Pi {
		t.Fatal("运行时由提供方决定")
	}
}
