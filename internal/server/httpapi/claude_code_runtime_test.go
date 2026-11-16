package httpapi_test

import (
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
)

// The model catalog has claude-code backed by the local CLI (runtime claude-code, no account needed); an Agent's
// runtime is derived from the provider and read-only: both project and platform Agents can pick claude-code directly,
// the thinking level can only take effort values, and the Codex speed cannot be set; when a Pi Agent moves to
// claude-code its runtime switches with it and the session is reset (epoch plus one).
func TestClaudeCodeRuntimeAPI(t *testing.T) {
	e := newEnv(t)
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg

	list := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", "/model-providers", nil))
	var cc, anthropic *apitypes.ModelProvider
	for i := range list.Providers {
		switch list.Providers[i].ID {
		case "claude-code":
			cc = &list.Providers[i]
		case "anthropic":
			anthropic = &list.Providers[i]
		}
	}
	if cc == nil || cc.Source != "local_cli" || cc.Runtime != "claude-code" || cc.Name == nil || *cc.Name != "Claude Code" || len(cc.AuthMethods) != 0 || cc.ModelCount != 4 {
		t.Fatalf("claude-code 提供方：%+v", cc)
	}
	if anthropic == nil || anthropic.Runtime != "pi" {
		t.Fatalf("其余提供方的运行时为 pi：%+v", anthropic)
	}
	models := decode[apitypes.ModelList](t, e.expect(200, "GET", "/model-providers/claude-code/models", nil))
	if len(models.Models) != 4 || models.Models[0].ID != "fable" || models.Models[0].DefaultThinkingLevel != "medium" {
		t.Fatalf("claude-code 的模型：%+v", models)
	}

	ccModel := map[string]string{"provider": "claude-code", "modelId": "sonnet"}
	created := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "cc", "model": ccModel, "thinkingLevel": "high"}))
	if created.Runtime != "claude-code" || created.Session.Epoch != 1 {
		t.Fatalf("新建的 Claude Code Agent：%+v", created)
	}
	platform := decode[apitypes.Agent](t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": "cc-platform", "model": ccModel}))
	if platform.Runtime != "claude-code" || platform.Scope != "platform" {
		t.Fatalf("平台 Agent 也能用 Claude Code：%+v", platform)
	}
	e.expect(422, "POST", e.agents(), map[string]any{"name": "cc-off", "model": ccModel, "thinkingLevel": "off"})
	e.expect(422, "POST", e.agents(), map[string]any{"name": "cc-tier", "model": ccModel, "serviceTier": "priority"})

	pi := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "pi", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	if pi.Runtime != "pi" {
		t.Fatalf("Pi Agent：%+v", pi)
	}
	switched := decode[apitypes.Agent](t, e.expect(200, "PATCH", "/agents/"+pi.ID.String(), map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "haiku"}}))
	if switched.Runtime != "claude-code" || switched.Session.State != "none" || switched.Session.Epoch != 2 {
		t.Fatalf("切换运行时后：%+v", switched)
	}
	listed := decode[apitypes.AgentList](t, e.expect(200, "GET", e.agents(), nil))
	for _, a := range listed.Agents {
		if a.ID == pi.ID && a.Runtime != "claude-code" {
			t.Fatalf("列表项的运行时：%+v", a)
		}
	}
}
