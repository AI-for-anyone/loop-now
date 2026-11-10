package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

// /model-providers 列出插件提供方（source=plugin、显示名、插件 id 与插件显示名）；带 agentId 时标出这个 Agent 不能用的原因，
// 选它的 422 字段为 model.provider；项目启用、授权之后原因消失、可以选。
func TestPluginProvidersInCatalogAPI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	captured := modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake OAuth", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", Name: "Fake One", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off", Input: []string{"text"}}}}
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "grokish", Provider: captured}})
	e.api.Models, e.api.Config.Models = reg, reg
	providers, _ := json.Marshal(modelcatalog.Capture{Providers: []modelcatalog.CapturedProvider{captured}})
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0",
		Manifest: json.RawMessage(`{"id":"grokish","name":"grokish","version":"0.1.0","displayName":{"zh-CN":"格罗克","en-US":"Grokish"}}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: providers}); err != nil {
		t.Fatal(err)
	}
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	find := func(path string) apitypes.ModelProvider {
		list := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", path, nil))
		for _, p := range list.Providers {
			if p.ID == "fake-oauth" {
				return p
			}
		}
		t.Fatalf("目录里没有插件提供方：%+v", list)
		return apitypes.ModelProvider{}
	}
	p := find("/model-providers")
	if p.Source != "plugin" || p.Name == nil || *p.Name != "Fake OAuth" || p.PluginID == nil || *p.PluginID != "grokish" || p.UnavailableReason != nil || string(p.PluginDisplayName) != `{"zh-CN":"格罗克","en-US":"Grokish"}` {
		t.Fatalf("插件提供方：%+v %s", p, p.PluginDisplayName)
	}
	withAgent := "/model-providers?agentId=" + agent.ID.String()
	if p = find(withAgent); p.UnavailableReason == nil || *p.UnavailableReason != "plugin_not_enabled" {
		t.Fatalf("未启用：%+v", p.UnavailableReason)
	}
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}}))
	if len(bad.Error.Fields) != 1 || bad.Error.Fields[0].Field != "model.provider" {
		t.Fatalf("422 字段：%+v", bad)
	}
	if _, _, err := e.st.SaveProjectPlugin(ctx, e.dc.Project.ID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
		t.Fatal(err)
	}
	if p = find(withAgent); p.UnavailableReason == nil || *p.UnavailableReason != "agent_not_granted" {
		t.Fatalf("未授权：%+v", p.UnavailableReason)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	if p = find(withAgent); p.UnavailableReason != nil {
		t.Fatalf("已授权：%+v", *p.UnavailableReason)
	}
	e.expect(200, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}})
	e.expect(422, "GET", "/model-providers?agentId=nope", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/model-providers?agentId="+uuid.NewString(), nil)
}
