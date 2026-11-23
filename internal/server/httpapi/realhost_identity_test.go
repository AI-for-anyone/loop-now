package httpapi_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
)

// The real host (when LOOP_NOW_PLUGIN_HOST_ENTRY is set) manages an external identity: identity.inbound goes by
// channelId to the channel's identityInbound, the plugin reads the full text with api.identities.read and answers in
// the identity's name with api.identities.answer, and the Agent's question is marked answered.
func TestRealHostManagesIdentity(t *testing.T) {
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("LOOP_NOW_PLUGIN_HOST_ENTRY is not set")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	e := newEnv(t)
	ctx := context.Background()
	dir := writePluginDir(t, "bridge", "identities.manage")
	patchManifest(t, dir, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "lark", "label": "Lark", "manageIdentities": true}}
	})
	code := `export default function (api) {
  api.channels.register('lark', { identityInbound: async (event) => {
    api.log.info('inbound ' + event.channelId + ' ' + event.identityId + ' ' + (event.question ? event.question.options.length : 0))
    const msg = await api.identities.read(event.identityId, event.messageId)
    api.log.info('read ' + msg.text)
    if (event.question) {
      const ans = await api.identities.answer(event.identityId, event.question.id, { optionId: event.question.options[1].id })
      api.log.info('answered ' + (ans.answerTo ? ans.answerTo.optionLabel : 'none'))
    }
  } })
}`
	if err := os.WriteFile(filepath.Join(dir, "platform.ts"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	e.installPlugin(dir)
	boss, _ := e.identity("boss")
	e.expect(200, "PUT", "/external-identities/"+boss.ID.String()+"/manager", map[string]any{"managedBy": map[string]any{"pluginId": "bridge", "channelId": "lark"}})
	e.api.Plugins.APIURL = e.srv.URL + "/api/v1"
	root := t.TempDir()
	e.startHost([]string{node, entry}, []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + root, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT=" + root, "LOOP_NOW_API_URL=" + e.api.Plugins.APIURL}, "bridge")

	agent, _ := e.newAgent("Assistant")
	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages", map[string]any{"to": "@boss", "text": "Going live tonight?", "options": []string{"Go live", "Wait a bit"}}))
	deadline := time.Now().Add(20 * time.Second)
	for {
		m, err := e.st.GetInboxMessage(ctx, q.ID, 200)
		if err == nil && m.Question.State == "answered" {
			if m.Question.Answer == nil || *m.Question.Answer.OptionID != "2" {
				t.Fatalf("the plugin answers in the identity's name: %+v", m.Question.Answer)
			}
			break
		}
		if time.Now().After(deadline) {
			var log string
			_ = e.st.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(level||':'||line, ' | ' ORDER BY id),'') FROM plugin_host_log`).Scan(&log)
			t.Fatalf("the question was not answered: %v; host log: %s", err, log)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var log string
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(log, "answered Wait a bit") && time.Now().Before(deadline) {
		_ = e.st.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(level||':'||line, ' | ' ORDER BY id),'') FROM plugin_host_log WHERE plugin_id='bridge'`).Scan(&log)
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(log, "inbound lark "+boss.ID.String()+" 2") || !strings.Contains(log, "read Going live tonight?") || !strings.Contains(log, "answered Wait a bit") {
		t.Fatalf("host log: %s", log)
	}
}

// The real host (when LOOP_NOW_PLUGIN_HOST_ENTRY is set) answers in a plugin thread: the plugin sends a message through
// its channel, the Agent asks a question in the thread, channel.outbound with the question goes to the channel's
// outbound, the plugin's reply to that question is the answer, and the question is marked answered.
func TestRealHostAnswersChannelQuestion(t *testing.T) {
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("LOOP_NOW_PLUGIN_HOST_ENTRY is not set")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	e := newEnv(t)
	ctx := context.Background()
	dir := writePluginDir(t, "bridge", "inbox.send", "inbox.read")
	patchManifest(t, dir, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "slack", "label": "Slack"}}
	})
	code := `export default function (api) {
  api.channels.register('slack', { outbound: async (event) => {
    api.log.info('outbound ' + event.channelId + ' ' + event.isQuestion + ' ' + (event.question ? event.question.options.map((o) => o.label).join('/') : ''))
    if (event.question) {
      const ans = await api.channels.reply(event.question.id, { text: 'Yes' })
      api.log.info('answered ' + ans.from.kind)
    }
  } })
  api.channels.send('slack', { to: 'Default project/Assistant', text: 'Shall we release?', senderName: 'Slack · Bob' }).then((m) => api.log.info('sent ' + m.id))
}`
	if err := os.WriteFile(filepath.Join(dir, "platform.ts"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	e.installPlugin(dir)
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	agent, path := e.newAgent("Assistant")
	e.api.Plugins.APIURL = e.srv.URL + "/api/v1"
	root := t.TempDir()
	e.startHost([]string{node, entry}, []string{"PATH=" + filepath.Dir(node) + ":/usr/bin:/bin", "HOME=" + root, "LANG=C.UTF-8", "LOOP_NOW_PLUGINS_ROOT=" + root, "LOOP_NOW_API_URL=" + e.api.Plugins.APIURL}, "bridge")
	hostLog := func() string {
		var log string
		_ = e.st.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(level||':'||line, ' | ' ORDER BY id),'') FROM plugin_host_log WHERE plugin_id='bridge'`).Scan(&log)
		return log
	}

	var sent string
	deadline := time.Now().Add(20 * time.Second)
	for e.st.Pool.QueryRow(ctx, `SELECT id FROM inbox_messages WHERE from_kind='plugin' AND to_agent_id=$1`, agent.ID).Scan(&sent) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("the plugin sent no message; host log: %s", hostLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.waitAgentIdle(path)
	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent+"/reply",
		map[string]any{"text": "Release tonight?", "options": []string{"Yes", "No"}}))
	deadline = time.Now().Add(20 * time.Second)
	for {
		m, err := e.st.GetInboxMessage(ctx, q.ID, 200)
		if err == nil && m.Question.State == "answered" {
			if m.Question.Answer == nil || m.Question.Answer.OptionID != nil || m.Question.Answer.TextPreview != "Yes" {
				t.Fatalf("the plugin answers with text: %+v", m.Question.Answer)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the question was not answered: %v; host log: %s", err, hostLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(hostLog(), "answered plugin") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if log := hostLog(); !strings.Contains(log, "outbound slack true Yes/No") || !strings.Contains(log, "answered plugin") {
		t.Fatalf("host log: %s", log)
	}
}
