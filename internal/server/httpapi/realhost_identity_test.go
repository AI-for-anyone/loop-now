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
