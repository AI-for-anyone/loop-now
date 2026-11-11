package httpapi_test

import (
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
)

// 模型目录里有本机 CLI 的 claude-code（runtime 为 claude-code，不需要账号）；Agent 的 runtime 由提供方派生、只读：
// 项目与平台 Agent 都能直接选 claude-code，思考等级只能是 effort 的取值，不能设 Codex 速度；
// Pi 的 Agent 改到 claude-code 时运行时随之切换，会话重置（序号加一）。
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
