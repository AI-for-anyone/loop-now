package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"loop-now/internal/server/apitypes"
)

// pluginToken 返回唯一已安装插件的新令牌（宿主启动前准备的那一份）。
func (e *env) pluginToken() string {
	e.t.Helper()
	specs, err := e.api.Plugins.Specs(context.Background())
	if err != nil || len(specs) != 1 {
		e.t.Fatal(specs, err)
	}
	raw, err := specs[0].Prepare(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	var p struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Token
}

// 插件作为发件方（SPEC §11）：带渠道与发件名称发给启用了它的项目里的 Agent；发给其他项目的 Agent 或人时 403，
// 其他身份不能指定渠道。Agent 回复时只给这个插件产生 channel.outbound，插件在自己的 Inbox 里读到回复并能继续回复，
// 线索沿用渠道与发件名称。
func TestPluginInboxSender(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.installPlugin(writePluginDir(t, "bridge", "inbox.send", "inbox.read"))
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	agent, path := e.newAgent("助手")
	other := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "别的项目"}))
	e.expect(201, "POST", "/projects/"+other.ID.String()+"/agents", map[string]any{"name": "项目外", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	auth := []string{"Authorization", "Bearer " + e.pluginToken()}

	sent := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages",
		map[string]any{"to": "默认项目/助手", "text": "飞书群里有人问部署进度", "channelId": "lark", "senderName": "飞书 · 张三"}, auth...))
	if f := sent.From; f.Kind != "plugin" || *f.PluginID != "bridge" || *f.ChannelID != "lark" || *f.Address != "plugin:bridge/lark" || f.Name != "飞书 · 张三" || f.ID != nil {
		t.Fatalf("插件发件方：%+v", f)
	}
	e.console().expectError(403, "forbidden", "POST", "/inbox/messages", map[string]any{"to": "别的项目/项目外", "text": "x"}, auth...)
	e.console().expectError(403, "forbidden", "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "x"}, auth...)
	e.expectError(422, "validation_failed", "POST", "/inbox/messages", map[string]any{"to": "默认项目/助手", "text": "x", "channelId": "lark"})
	e.waitAgentIdle(path)

	reply := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent.ID.String()+"/reply", map[string]any{"text": "今晚八点上线"}))
	if to := reply.To; to.Kind != "plugin" || *to.ChannelID != "lark" || to.Name != "飞书 · 张三" {
		t.Fatalf("回复给插件：%+v", to)
	}
	var data []byte
	var target string
	if err := e.st.Pool.QueryRow(ctx, `SELECT e.data, d.plugin_id FROM plugin_events e JOIN plugin_event_deliveries d ON d.event_id=e.id WHERE e.type='channel.outbound'`).Scan(&data, &target); err != nil || target != "bridge" {
		t.Fatalf("channel.outbound 只投给 bridge：%q %v", target, err)
	}
	var ev map[string]any
	_ = json.Unmarshal(data, &ev)
	if ev["messageId"] != reply.ID.String() || ev["threadId"] != sent.ID.String() || ev["channelId"] != "lark" || ev["excerpt"] != "今晚八点上线" {
		t.Fatalf("channel.outbound：%v", ev)
	}

	page := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/inbox/messages", nil, auth...))
	if len(page.Messages) != 1 || page.Messages[0].ID != reply.ID {
		t.Fatalf("插件的 Inbox：%+v", page.Messages)
	}
	again := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+reply.ID.String()+"/reply", map[string]any{"text": "好的，已转告"}, auth...))
	if again.From.Kind != "plugin" || *again.From.ChannelID != "lark" || again.From.Name != "飞书 · 张三" || again.To.ID == nil || *again.To.ID != agent.ID {
		t.Fatalf("插件继续回复：%+v", again)
	}
}
