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

// 真实宿主（设置 LOOP_NOW_PLUGIN_HOST_ENTRY 时）代管外部身份：identity.inbound 按 channelId 交给渠道的 identityInbound，
// 插件用 api.identities.read 读全文、用 api.identities.answer 以身份的名义回答，Agent 的问题记为已回答。
func TestRealHostManagesIdentity(t *testing.T) {
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("没有设置 LOOP_NOW_PLUGIN_HOST_ENTRY")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("找不到 node")
	}
	e := newEnv(t)
	ctx := context.Background()
	dir := writePluginDir(t, "bridge", "identities.manage")
	patchManifest(t, dir, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "lark", "label": "飞书", "manageIdentities": true}}
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

	agent, _ := e.newAgent("助手")
	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages", map[string]any{"to": "@boss", "text": "今晚上线吗？", "options": []string{"上线", "再等等"}}))
	deadline := time.Now().Add(20 * time.Second)
	for {
		m, err := e.st.GetInboxMessage(ctx, q.ID, 200)
		if err == nil && m.Question.State == "answered" {
			if m.Question.Answer == nil || *m.Question.Answer.OptionID != "2" {
				t.Fatalf("插件以身份的名义回答：%+v", m.Question.Answer)
			}
			break
		}
		if time.Now().After(deadline) {
			var log string
			_ = e.st.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(level||':'||line, ' | ' ORDER BY id),'') FROM plugin_host_log`).Scan(&log)
			t.Fatalf("问题没有被回答：%v；宿主日志：%s", err, log)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var log string
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(log, "answered 再等等") && time.Now().Before(deadline) {
		_ = e.st.Pool.QueryRow(ctx, `SELECT coalesce(string_agg(level||':'||line, ' | ' ORDER BY id),'') FROM plugin_host_log WHERE plugin_id='bridge'`).Scan(&log)
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(log, "inbound lark "+boss.ID.String()+" 2") || !strings.Contains(log, "read 今晚上线吗？") || !strings.Contains(log, "answered 再等等") {
		t.Fatalf("宿主日志：%s", log)
	}
}

// 真实宿主（设置 LOOP_NOW_PLUGIN_HOST_ENTRY 时）在插件线索里答题：插件经渠道发消息，Agent 在线索里提问，
// channel.outbound 带 question 交给渠道的 outbound，插件回复这道提问即作答，问题记为已回答。
func TestRealHostAnswersChannelQuestion(t *testing.T) {
	entry := os.Getenv("LOOP_NOW_PLUGIN_HOST_ENTRY")
	if entry == "" {
		t.Skip("没有设置 LOOP_NOW_PLUGIN_HOST_ENTRY")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("找不到 node")
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
      const ans = await api.channels.reply(event.question.id, { text: '发' })
      api.log.info('answered ' + ans.from.kind)
    }
  } })
  api.channels.send('slack', { to: '默认项目/助手', text: '要发版吗', senderName: 'Slack · 李四' }).then((m) => api.log.info('sent ' + m.id))
}`
	if err := os.WriteFile(filepath.Join(dir, "platform.ts"), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	e.installPlugin(dir)
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	agent, path := e.newAgent("助手")
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
	for e.st.Pool.QueryRow(ctx, `SELECT id::text FROM inbox_messages WHERE from_kind='plugin' AND to_agent_id=$1`, agent.ID).Scan(&sent) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("插件没有发出消息；宿主日志：%s", hostLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.waitAgentIdle(path)
	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages/"+sent+"/reply",
		map[string]any{"text": "今晚发吗？", "options": []string{"发", "不发"}}))
	deadline = time.Now().Add(20 * time.Second)
	for {
		m, err := e.st.GetInboxMessage(ctx, q.ID, 200)
		if err == nil && m.Question.State == "answered" {
			if m.Question.Answer == nil || m.Question.Answer.OptionID != nil || m.Question.Answer.TextPreview != "发" {
				t.Fatalf("插件以文字作答：%+v", m.Question.Answer)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("问题没有被回答：%v；宿主日志：%s", err, hostLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(hostLog(), "answered plugin") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if log := hostLog(); !strings.Contains(log, "outbound slack true 发/不发") || !strings.Contains(log, "answered plugin") {
		t.Fatalf("宿主日志：%s", log)
	}
}
