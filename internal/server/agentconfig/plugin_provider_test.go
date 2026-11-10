package agentconfig_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// 插件注册的模型提供方：新建 Agent 不能直接选；修改时要求项目已启用该插件、Agent 已获授权，原因分别说明；平台 Agent 不能选。
func TestPluginProviderNeedsGrant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := modelcatalog.NewRegistry(e.svc.Models.Snapshot())
	captured := modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "grokish", Provider: captured}})
	e.svc.Models = reg
	providers, _ := json.Marshal(modelcatalog.Capture{Providers: []modelcatalog.CapturedProvider{captured}})
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: providers}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.CreateAgent(ctx, e.scope, "新建", "fake-oauth", "fake-1", ""); !isField(err, "model.provider", "先建好 Agent") {
		t.Fatalf("新建 Agent 不能直接选插件提供方：%v", err)
	}
	a := e.agent(t)
	set := func() error {
		p, m := "fake-oauth", "fake-1"
		_, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Provider: &p, ModelID: &m})
		return err
	}
	if err := set(); !isField(err, "model.provider", "先在项目里启用") {
		t.Fatalf("项目未启用：%v", err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "fake-oauth"); reason != store.PluginUnusableNotEnabled {
		t.Fatalf("原因：%q", reason)
	}
	if _, _, err := e.st.SaveProjectPlugin(ctx, e.scope.ProjectID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
		t.Fatal(err)
	}
	if err := set(); !isField(err, "model.provider", "先把该插件授权") {
		t.Fatalf("未授权：%v", err)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, a.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := set(); err != nil {
		t.Fatalf("已授权后可以选：%v", err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "fake-oauth"); reason != "" {
		t.Fatalf("已授权：%q", reason)
	}
	if plugin, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "anthropic"); plugin != "" || reason != "" {
		t.Fatal("内置提供方不受影响")
	}

	platform, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "平台助手", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, platform.ID, "fake-oauth"); reason != store.PluginUnusablePlatformAgent {
		t.Fatalf("平台 Agent：%q", reason)
	}
}

func isField(err error, field, contains string) bool {
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 {
		return false
	}
	return ae.Fields[0].Field == field && strings.Contains(ae.Fields[0].Message, contains)
}
