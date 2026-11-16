package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"loop-now/internal/server/apitypes"
)

// pluginToken returns a new token for the only installed plugin (the one prepared before the host starts).
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

// A plugin as sender (SPEC §11): it sends with a channel and a sender name to Agents in projects that have it enabled;
// sending to Agents of other projects or to people gives 403, and other identities cannot specify a channel. An Agent's
// reply produces channel.outbound for this plugin only; the plugin reads the reply in its own Inbox and can reply
// again, with the thread keeping the channel and sender name.
func TestPluginInboxSender(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.installPlugin(writePluginDir(t, "bridge", "inbox.send", "inbox.read"))
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	agent, path := e.newAgent("Assistant")
	other := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "Other project"}))
	e.expect(201, "POST", "/projects/"+other.ID.String()+"/agents", map[string]any{"name": "Outside project", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}})
	auth := []string{"Authorization", "Bearer " + e.pluginToken()}

	sent := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages",
		map[string]any{"to": "Default project/Assistant", "text": "Someone in the Lark group asks how the deploy is going", "channelId": "lark", "senderName": "Lark · Alice"}, auth...))
	if f := sent.From; f.Kind != "plugin" || *f.PluginID != "bridge" || *f.ChannelID != "lark" || *f.Address != "plugin:bridge/lark" || f.Name != "Lark · Alice" || f.ID != nil {
		t.Fatalf("plugin sender: %+v", f)
	}
	e.console().expectError(403, "forbidden", "POST", "/inbox/messages", map[string]any{"to": "Other project/Outside project", "text": "x"}, auth...)
	e.console().expectError(403, "forbidden", "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "x"}, auth...)
	e.expectError(422, "validation_failed", "POST", "/inbox/messages", map[string]any{"to": "Default project/Assistant", "text": "x", "channelId": "lark"})
	e.waitAgentIdle(path)

	reply := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent.ID.String()+"/reply", map[string]any{"text": "Going live at eight tonight"}))
	if to := reply.To; to.Kind != "plugin" || *to.ChannelID != "lark" || to.Name != "Lark · Alice" {
		t.Fatalf("reply to the plugin: %+v", to)
	}
	var data []byte
	var target string
	if err := e.st.Pool.QueryRow(ctx, `SELECT e.data, d.plugin_id FROM plugin_events e JOIN plugin_event_deliveries d ON d.event_id=e.id WHERE e.type='channel.outbound'`).Scan(&data, &target); err != nil || target != "bridge" {
		t.Fatalf("channel.outbound should go to bridge only: %q %v", target, err)
	}
	var ev map[string]any
	_ = json.Unmarshal(data, &ev)
	if ev["messageId"] != reply.ID.String() || ev["threadId"] != sent.ID.String() || ev["channelId"] != "lark" || ev["excerpt"] != "Going live at eight tonight" ||
		ev["isQuestion"] != false || ev["question"] != nil {
		t.Fatalf("channel.outbound: %v", ev)
	}

	page := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/inbox/messages", nil, auth...))
	if len(page.Messages) != 1 || page.Messages[0].ID != reply.ID {
		t.Fatalf("the plugin's Inbox: %+v", page.Messages)
	}
	again := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+reply.ID.String()+"/reply", map[string]any{"text": "OK, passed it on"}, auth...))
	if again.From.Kind != "plugin" || *again.From.ChannelID != "lark" || again.From.Name != "Lark · Alice" || again.To.ID == nil || *again.To.ID != agent.ID {
		t.Fatalf("the plugin replies again: %+v", again)
	}
}

// An Agent can ask questions in a thread started by a plugin (SPEC §11): channel.outbound carries isQuestion and
// question (same shape as in identity.inbound); the plugin replying to that question answers it, with the same effect
// as an external identity answering (the question is marked answered and the Agent is reminded), and the audit records
// the plugin as the actor. A plugin cannot ask questions (422), another plugin answering gets 403, a second answer 409.
func TestPluginThreadQuestions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"bridge", "other"} {
		e.installPlugin(writePluginDir(t, id, "inbox.send", "inbox.read"))
		e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/"+id, map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	}
	agent, path := e.newAgent("Assistant")
	bridge := []string{"Authorization", "Bearer " + e.pluginTokenFor("bridge")}
	other := []string{"Authorization", "Bearer " + e.pluginTokenFor("other")}
	sent := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages",
		map[string]any{"to": "Default project/Assistant", "text": "Shall we release?", "channelId": "slack", "senderName": "Slack · Bob"}, bridge...))
	e.waitAgentIdle(path)

	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent.ID.String()+"/reply",
		map[string]any{"text": "Release tonight?", "options": []string{"Yes", "No"}}))
	if q.Question == nil || q.To.Kind != "plugin" || *q.To.ChannelID != "slack" {
		t.Fatalf("the Agent asks in the plugin thread: %+v", q)
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
		len(options) != 2 || options[0].(map[string]any)["id"] != "1" || options[1].(map[string]any)["label"] != "No" {
		t.Fatalf("channel.outbound with a question: %v", ev)
	}

	reply := "/inbox/messages/" + q.ID.String() + "/reply"
	e.console().expectError(422, "validation_failed", "POST", reply, map[string]any{"text": "counter-question", "options": []string{"a"}}, bridge...)
	e.console().expectError(403, "forbidden", "POST", reply, map[string]any{"optionId": "1"}, other...)
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", reply, map[string]any{"optionId": "1"}, bridge...))
	if answer.From.Kind != "plugin" || answer.To.ID == nil || *answer.To.ID != agent.ID || answer.AnswerTo == nil || answer.AnswerTo.MessageID != q.ID {
		t.Fatalf("plugin answers: %+v", answer)
	}
	if m, err := e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || m.Question.State != "answered" || *m.Question.Answer.OptionID != "1" {
		t.Fatalf("question recorded as answered: %+v %v", m.Question, err)
	}
	e.console().expectError(409, "already_answered", "POST", reply, map[string]any{"optionId": "2"}, bridge...)
	var actor string
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_kind||'|'||actor_address FROM audit_log WHERE operation='replyInboxMessage' AND status=201 ORDER BY at DESC LIMIT 1`).Scan(&actor); err != nil || actor != "plugin|plugin:bridge" {
		t.Fatalf("audit: %q %v", actor, err)
	}
	e.waitAgentIdle(path)
	var reminders int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='inbox_reminder' AND trigger_inbox_message_id=$2`, agent.ID, answer.ID).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatalf("answering should remind the Agent once: %d %v", reminders, err)
	}
}
