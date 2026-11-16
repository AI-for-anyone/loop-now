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

// /model-providers lists plugin providers (source=plugin, display name, plugin id and plugin display name); with
// agentId, the reason this Agent cannot use one is marked, and picking it gives a 422 on field model.provider; once the
// project enables it and it is granted, the reason disappears and it can be picked.
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
		Manifest: json.RawMessage(`{"id":"grokish","name":"grokish","version":"0.1.0","displayName":{"zh-CN":"格罗克","en-US":"Grokish"}}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: providers}); err != nil { // check-english: zh (zh-CN value of a language-tag object)
		t.Fatal(err)
	}
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "Assistant", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	find := func(path string) apitypes.ModelProvider {
		list := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", path, nil))
		for _, p := range list.Providers {
			if p.ID == "fake-oauth" {
				return p
			}
		}
		t.Fatalf("the catalog has no plugin provider: %+v", list)
		return apitypes.ModelProvider{}
	}
	p := find("/model-providers")
	if p.Source != "plugin" || p.Name == nil || *p.Name != "Fake OAuth" || p.PluginID == nil || *p.PluginID != "grokish" || p.UnavailableReason != nil || string(p.PluginDisplayName) != `{"zh-CN":"格罗克","en-US":"Grokish"}` { // check-english: zh (zh-CN value of a language-tag object)
		t.Fatalf("plugin provider: %+v %s", p, p.PluginDisplayName)
	}
	withAgent := "/model-providers?agentId=" + agent.ID.String()
	if p = find(withAgent); p.UnavailableReason == nil || *p.UnavailableReason != "plugin_not_enabled" {
		t.Fatalf("not enabled: %+v", p.UnavailableReason)
	}
	bad := decode[apitypes.ErrorBody](t, e.expect(422, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}}))
	if len(bad.Error.Fields) != 1 || bad.Error.Fields[0].Field != "model.provider" {
		t.Fatalf("422 fields: %+v", bad)
	}
	if _, _, err := e.st.SaveProjectPlugin(ctx, e.dc.Project.ID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
		t.Fatal(err)
	}
	if p = find(withAgent); p.UnavailableReason == nil || *p.UnavailableReason != "agent_not_granted" {
		t.Fatalf("not granted: %+v", p.UnavailableReason)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	if p = find(withAgent); p.UnavailableReason != nil {
		t.Fatalf("granted: %+v", *p.UnavailableReason)
	}
	e.expect(200, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}})
	e.expect(422, "GET", "/model-providers?agentId=nope", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/model-providers?agentId="+uuid.NewString(), nil)
}
