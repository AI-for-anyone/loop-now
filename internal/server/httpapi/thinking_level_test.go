package httpapi_test

import (
	"fmt"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
)

func TestAgentThinkingLevelHTTPPersistenceAndModelValidation(t *testing.T) {
	e := newEnv(t)
	models := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "anthropic", Models: []modelcatalog.Model{
		{ID: "claude-x", Reasoning: true, ThinkingLevels: []string{"low", "medium", "high"}, DefaultThinkingLevel: "medium"},
		{ID: "plain", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"},
	}}})
	e.api.Models = models
	e.api.Config.Models = models
	model := map[string]string{"provider": "anthropic", "modelId": "claude-x"}
	a := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "thinking", "model": model, "thinkingLevel": "high"}))
	path := "/agents/" + a.ID.String()
	if a.ThinkingLevel == nil || *a.ThinkingLevel != "high" {
		t.Fatal("create level missing")
	}
	list := decode[apitypes.AgentList](t, e.expect(200, "GET", e.agents(), nil))
	if len(list.Agents) != 1 || list.Agents[0].ThinkingLevel == nil || *list.Agents[0].ThinkingLevel != "high" {
		t.Fatal("list projection missing")
	}
	oldVersion := a.ConfigVersion
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"name": "thinking-renamed"}))
	if a.ThinkingLevel == nil || *a.ThinkingLevel != "high" || a.ConfigVersion != oldVersion {
		t.Fatal("omission reset level/bumped config")
	}
	for _, bad := range []any{"unknown", "", 2, true, map[string]any{}} {
		e.expect(422, "PATCH", path, map[string]any{"thinkingLevel": bad}, "X-Contract-Invalid-Request", "1")
	}
	e.expect(422, "PATCH", path, map[string]any{"thinkingLevel": "max"})
	e.expect(422, "PATCH", path, map[string]any{"model": map[string]string{"provider": "anthropic", "modelId": "plain"}})
	unchanged := decode[apitypes.Agent](t, e.expect(200, "GET", path, nil))
	if unchanged.Model.ModelID != "claude-x" || unchanged.ConfigVersion != oldVersion || *unchanged.ThinkingLevel != "high" {
		t.Fatal("rejected combo partially saved")
	}
	a = decode[apitypes.Agent](t, e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "anthropic", "modelId": "plain"}, "thinkingLevel": nil}))
	if a.ThinkingLevel != nil || a.ConfigVersion != oldVersion+1 {
		t.Fatal("null reset/model update did not commit once")
	}
	e.expect(422, "PATCH", path, map[string]any{"thinkingLevel": "high"})
	for i, body := range []map[string]any{{"name": "default", "model": model}, {"name": "default-null", "model": model, "thinkingLevel": nil}} {
		got := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), body))
		if got.ThinkingLevel != nil {
			t.Fatal("default not null", i)
		}
	}
	directory := decode[apitypes.ModelList](t, e.expect(200, "GET", "/model-providers/anthropic/models", nil))
	if len(directory.Models) != 2 || fmt.Sprint(directory.Models[0].ThinkingLevels) != "[low medium high]" || directory.Models[0].DefaultThinkingLevel != "medium" {
		t.Fatal("catalog metadata", directory)
	}
}
