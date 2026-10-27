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
	if ev["messageId"] != reply.ID.String() || ev["threadId"] != sent.ID.String() || ev["channelId"] != "lark" || ev["excerpt"] != "今晚八点上线" ||
		ev["isQuestion"] != false || ev["question"] != nil {
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

// 插件发来的线索里 Agent 可以提问（SPEC §11）：channel.outbound 带 isQuestion 与 question（形状与 identity.inbound 相同）；
// 插件回复这道提问即作答，效果与外部身份作答相同（问题记为已回答、提醒 Agent），审计记操作者为插件。
// 插件不能提问（422），别的插件作答 403，再答 409。
func TestPluginThreadQuestions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"bridge", "other"} {
		e.installPlugin(writePluginDir(t, id, "inbox.send", "inbox.read"))
		e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/"+id, map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	}
	agent, path := e.newAgent("助手")
	bridge := []string{"Authorization", "Bearer " + e.pluginTokenFor("bridge")}
	other := []string{"Authorization", "Bearer " + e.pluginTokenFor("other")}
	sent := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages",
		map[string]any{"to": "默认项目/助手", "text": "要发版吗", "channelId": "slack", "senderName": "Slack · 李四"}, bridge...))
	e.waitAgentIdle(path)

	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent.ID.String()+"/reply",
		map[string]any{"text": "今晚发吗？", "options": []string{"发", "不发"}}))
	if q.Question == nil || q.To.Kind != "plugin" || *q.To.ChannelID != "slack" {
		t.Fatalf("Agent 在插件线索里提问：%+v", q)
	}
	var data []byte
	if err := e.st.Pool.QueryRow(ctx, `SELECT e.data FROM plugin_events e JOIN plugin_event_deliveries d ON d.event_id=e.id
		WHERE e.type='channel.outbound' AND d.plugin_id='bridge'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	_ = json.Unmarshal(data, &ev)
	question, _ := ev["question"].(map[string]any)
	options, _ := question["options"].([]any)
	if ev["isQuestion"] != true || ev["messageId"] != q.ID.String() || question["id"] != q.ID.String() || question["allowText"] != true || question["deadline"] == nil ||
		len(options) != 2 || options[0].(map[string]any)["id"] != "1" || options[1].(map[string]any)["label"] != "不发" {
		t.Fatalf("channel.outbound 带提问：%v", ev)
	}

	reply := "/inbox/messages/" + q.ID.String() + "/reply"
	e.console().expectError(422, "validation_failed", "POST", reply, map[string]any{"text": "反问", "options": []string{"a"}}, bridge...)
	e.console().expectError(403, "forbidden", "POST", reply, map[string]any{"optionId": "1"}, other...)
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", reply, map[string]any{"optionId": "1"}, bridge...))
	if answer.From.Kind != "plugin" || answer.To.ID == nil || *answer.To.ID != agent.ID || answer.AnswerTo == nil || answer.AnswerTo.MessageID != q.ID {
		t.Fatalf("插件作答：%+v", answer)
	}
	if m, err := e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || m.Question.State != "answered" || *m.Question.Answer.OptionID != "1" {
		t.Fatalf("问题记为已回答：%+v %v", m.Question, err)
	}
	e.console().expectError(409, "already_answered", "POST", reply, map[string]any{"optionId": "2"}, bridge...)
	var actor string
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_kind||'|'||actor_address FROM audit_log WHERE operation='replyInboxMessage' AND status=201 ORDER BY at DESC LIMIT 1`).Scan(&actor); err != nil || actor != "plugin|plugin:bridge" {
		t.Fatalf("审计：%q %v", actor, err)
	}
	e.waitAgentIdle(path)
	var reminders int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='inbox_reminder' AND trigger_inbox_message_id=$2`, agent.ID, answer.ID).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatalf("作答应提醒 Agent 一次：%d %v", reminders, err)
	}
}
