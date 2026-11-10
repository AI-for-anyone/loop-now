package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// GET /agents/{id}/commands：没读到过时为空；读到后按来源文件所在的安装目录标出插件与插件显示名。
func TestListAgentCommands(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	path := "/agents/" + agent.ID.String() + "/commands"
	type list struct {
		CapturedAt *time.Time `json:"capturedAt"`
		Commands   []struct {
			Name              string          `json:"name"`
			Source            string          `json:"source"`
			PiSource          string          `json:"piSource"`
			Description       *string         `json:"description"`
			PluginID          *string         `json:"pluginId"`
			PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
		} `json:"commands"`
	}
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != 0 {
		t.Fatalf("没有 PiCommands 时为空：%+v", l)
	}
	cached := map[uuid.UUID]*executor.PiCommands{}
	e.api.PiCommands = func(id uuid.UUID) (*executor.PiCommands, error) { return cached[id], nil }
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != 0 {
		t.Fatalf("从没启动过：%+v", l)
	}
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/opt/grokish"}`), Version: "0.1.0",
		Manifest: json.RawMessage(`{"id":"grokish","name":"pi-grokish","version":"0.1.0","displayName":"Grokish"}`), InstallDir: "/opt/grokish"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	desc := "管理账号"
	cached[agent.ID] = &executor.PiCommands{CapturedAt: time.Now().UTC(), Commands: []executor.PiCommand{
		{Name: "grok-cli-accounts", Description: &desc, Source: "extension", Path: "/opt/grokish/src/index.ts"},
		{Name: "skill:loopctl", Source: "skill", Path: "/work/agents/x/skills/loopctl"},
		{Name: "near-miss", Source: "extension", Path: "/opt/grokish-other/index.ts"},
	}}
	l := decode[list](t, e.expect(200, "GET", path, nil))
	if l.CapturedAt == nil || len(l.Commands) != 3 {
		t.Fatalf("命令：%+v", l)
	}
	c := l.Commands[0]
	if c.Name != "grok-cli-accounts" || c.Source != "pi" || c.PiSource != "extension" || c.Description == nil || c.PluginID == nil || *c.PluginID != "grokish" || string(c.PluginDisplayName) != `"Grokish"` {
		t.Fatalf("插件命令：%+v %s", c, c.PluginDisplayName)
	}
	if l.Commands[1].PluginID != nil || string(l.Commands[1].PluginDisplayName) != "null" || l.Commands[2].PluginID != nil {
		t.Fatalf("不在插件目录里的命令：%+v", l.Commands[1:])
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/commands", nil)
}
