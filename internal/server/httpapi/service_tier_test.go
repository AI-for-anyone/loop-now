package httpapi_test

import (
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
	"testing"
)

func TestCodexServiceTierHTTPPersistenceAndProviderValidation(t *testing.T) {
	e := newEnv(t)
	e.api.Config.Models = modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{
		{ID: "openai-codex", Models: []modelcatalog.Model{{ID: "fixture", Input: []string{"text"}}}},
		{ID: "anthropic", Models: []modelcatalog.Model{{ID: "claude-x", Input: []string{"text"}}}},
	})
	model := map[string]string{"provider": "openai-codex", "modelId": "fixture"}
	a := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "speed", "model": model, "serviceTier": "priority"}))
	if a.ServiceTier == nil || *a.ServiceTier != "priority" {
		t.Fatal("create lost tier")
	}
	path := "/agents/" + a.ID.String()
	version := a.ConfigVersion
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"name": "renamed"}))
	if a.ServiceTier == nil || *a.ServiceTier != "priority" || a.ConfigVersion != version {
		t.Fatal("omission must retain tier")
	}
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"serviceTier": "ultrafast"}))
	if a.ServiceTier == nil || *a.ServiceTier != "ultrafast" || a.ConfigVersion != version+1 {
		t.Fatal("change must bump config version")
	}
	for _, bad := range []any{"auto", "fast", "", 2, true, []string{"priority"}} {
		e.expect(422, "PATCH", path, map[string]any{"serviceTier": bad}, "X-Contract-Invalid-Request", "1")
	}
	e.expect(422, "PATCH", path, map[string]any{"model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	a = decode[apitypes.Agent](t, e.expect(200, "GET", path, nil))
	if a.Model.Provider != "openai-codex" || *a.ServiceTier != "ultrafast" || a.ConfigVersion != version+1 {
		t.Fatal("invalid combined update partially wrote")
	}
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"serviceTier": nil}))
	if a.ServiceTier != nil || a.ConfigVersion != version+2 {
		t.Fatal("null must reset standard")
	}
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"serviceTier": "default"}))
	if a.ServiceTier == nil || *a.ServiceTier != "default" {
		t.Fatal("explicit standard lost")
	}
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}, "serviceTier": nil}))
	if a.ServiceTier != nil || a.Model.Provider != "anthropic" {
		t.Fatal("atomic provider+null rejected")
	}
	for _, tier := range []string{"default", "priority", "ultrafast"} {
		e.expect(422, "PATCH", path, map[string]any{"serviceTier": tier})
		e.expect(422, "POST", e.agents(), map[string]any{"name": "invalid-" + tier, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}, "serviceTier": tier})
	}
	for _, input := range []map[string]any{{"name": "legacy", "model": model}, {"name": "null", "model": model, "serviceTier": nil}} {
		old := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), input))
		if old.ServiceTier != nil {
			t.Fatal("default raw choice not null")
		}
	}
	list := decode[apitypes.AgentList](t, e.expect(200, "GET", e.agents(), nil))
	if len(list.Agents) != 3 {
		t.Fatal("list changed")
	}
}
