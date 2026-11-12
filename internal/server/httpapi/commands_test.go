package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
)

type commandList struct {
	CapturedAt *time.Time `json:"capturedAt"`
	Commands   []struct {
		Name              string          `json:"name"`
		Source            string          `json:"source"`
		PiSource          string          `json:"piSource"`
		Description       *string         `json:"description"`
		PluginID          *string         `json:"pluginId"`
		PluginDisplayName json.RawMessage `json:"pluginDisplayName"`
		Usage             *string         `json:"usage"`
		Available         bool            `json:"available"`
		UnavailableReason *string         `json:"unavailableReason"`
	} `json:"commands"`
}

// platformAvailability 把列表里的平台命令写成 名字=可用 或 名字:原因。
func platformAvailability(l commandList) []string {
	var out []string
	for _, c := range l.Commands {
		if c.Source != "platform" {
			continue
		}
		if c.Available {
			out = append(out, c.Name)
		} else {
			out = append(out, c.Name+":"+*c.UnavailableReason)
		}
	}
	return out
}

// 平台内置命令排在最前，按运行时与会话状态标出此刻能不能用。
func TestListAgentCommandsPlatform(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("命令清单")
	get := func() commandList { return decode[commandList](t, e.expect(200, "GET", path+"/commands", nil)) }
	l := get()
	if got := strings.Join(platformAvailability(l), " "); got != "session new compact:session_none reload copy export:session_none resume" {
		t.Fatalf("还没有会话：%s", got)
	}
	if c := l.Commands[2]; c.Usage == nil || *c.Usage != "/compact [instructions]" || c.PiSource != "" || c.Description != nil {
		t.Fatalf("平台命令的字段：%+v", c)
	}
	if err := e.st.RecordSessionFile(ctx, agent.ID, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact reload copy export resume" {
		t.Fatalf("有会话时都能用：%s", got)
	}
	if err := e.st.MarkSessionMissing(ctx, agent.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact:session_missing reload copy export:session_missing resume" {
		t.Fatalf("会话丢失：%s", got)
	}
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	e.api.Models, e.api.Config.Models = reg, reg
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "claude-code", "modelId": "sonnet"}})
	if got := strings.Join(platformAvailability(get()), " "); got != "session new compact:runtime_unsupported reload copy export:runtime_unsupported resume:runtime_unsupported" {
		t.Fatalf("Claude Code：%s", got)
	}
}

// GET /agents/{id}/commands：没读到过 Pi 命令时只有平台命令；读到后按来源文件所在的安装目录标出插件与插件显示名。
func TestListAgentCommands(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	path := "/agents/" + agent.ID.String() + "/commands"
	type list = commandList
	const platform = 7
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != platform {
		t.Fatalf("没有 PiCommands 时只有平台命令：%+v", l)
	}
	cached := map[uuid.UUID]*executor.PiCommands{}
	e.api.PiCommands = func(id uuid.UUID) (*executor.PiCommands, error) { return cached[id], nil }
	if l := decode[list](t, e.expect(200, "GET", path, nil)); l.CapturedAt != nil || len(l.Commands) != platform {
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
	all := decode[list](t, e.expect(200, "GET", path, nil))
	if all.CapturedAt == nil || len(all.Commands) != platform+3 {
		t.Fatalf("命令：%+v", all)
	}
	l := all
	l.Commands = all.Commands[platform:]
	c := l.Commands[0]
	if c.Name != "grok-cli-accounts" || c.Source != "pi" || c.PiSource != "extension" || c.Description == nil || c.PluginID == nil || *c.PluginID != "grokish" || string(c.PluginDisplayName) != `"Grokish"` ||
		!c.Available || c.Usage != nil || c.UnavailableReason != nil {
		t.Fatalf("插件命令：%+v %s", c, c.PluginDisplayName)
	}
	if l.Commands[1].PluginID != nil || string(l.Commands[1].PluginDisplayName) != "null" || l.Commands[2].PluginID != nil {
		t.Fatalf("不在插件目录里的命令：%+v", l.Commands[1:])
	}
	e.expect(404, "GET", "/agents/"+uuid.NewString()+"/commands", nil)
}
