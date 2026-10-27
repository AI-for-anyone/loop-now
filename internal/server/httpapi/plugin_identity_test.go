package httpapi_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/tasks"
)

// installManager 安装一个能代管外部身份的插件：渠道 lark 声明了 manageIdentities，渠道 plain 没有。
func (e *env) installManager(id string) {
	e.t.Helper()
	dir := writePluginDir(e.t, id, "identities.manage")
	patchManifest(e.t, dir, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "lark", "label": "飞书", "manageIdentities": true}, map[string]any{"id": "plain", "label": "普通"}}
	})
	e.installPlugin(dir)
}

// 插件代管外部身份（plugins/SPEC.md §12）：绑定只接受已安装、渠道声明了代管的插件；发给代管身份的提问以 identity.inbound 送到代管插件；
// 代管插件凭 identities.manage 读这个身份的收件箱、以它的名义回答（不需要 inbox.send），审计记下代表的身份；
// 不是代管者、没有 asIdentityId 又没有 inbox.send、非插件带 asIdentityId 都被拒绝。卸载插件后绑定清空。
func TestPluginManagesIdentity(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.installManager("bridge")
	e.installManager("other")
	bad := writePluginDir(t, "bad")
	patchManifest(t, bad, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "lark", "label": "飞书", "manageIdentities": true}}
	})
	e.expectError(422, "validation_failed", "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "local", "location": bad}})
	boss, _ := e.identity("boss")
	ops, _ := e.identity("ops")
	manager := "/external-identities/" + boss.ID.String() + "/manager"
	e.expectError(422, "validation_failed", "PUT", manager, map[string]any{"managedBy": map[string]any{"pluginId": "nobody", "channelId": "lark"}})
	e.expectError(422, "validation_failed", "PUT", manager, map[string]any{"managedBy": map[string]any{"pluginId": "bridge", "channelId": "plain"}})
	e.expectError(404, "not_found", "PUT", "/external-identities/"+uuid.NewString()+"/manager", map[string]any{"managedBy": nil})
	got := decode[apitypes.ExternalIdentity](t, e.expect(200, "PUT", manager, map[string]any{"managedBy": map[string]any{"pluginId": "bridge", "channelId": "lark"}}))
	if got.ManagedBy == nil || got.ManagedBy.PluginID != "bridge" || got.ManagedBy.ChannelID != "lark" {
		t.Fatalf("绑定：%+v", got)
	}
	if x := decode[apitypes.ExternalIdentity](t, e.expect(200, "GET", "/external-identities/"+ops.ID.String(), nil)); x.ManagedBy != nil {
		t.Fatalf("没有代管：%+v", x)
	}

	journal := e.startFakeHost("bridge", "other")
	agent, _ := e.newAgent("助手")
	q := decode[apitypes.InboxMessage](t, e.agentCaller(agent.ID).expect(201, "POST", "/inbox/messages", map[string]any{"to": "@boss", "text": "今晚上线吗？", "options": []string{"上线", "再等等"}}))
	deadline := time.Now().Add(10 * time.Second)
	var inbound map[string]any
	for inbound == nil {
		for _, m := range journalEntries(t, journal, "event") {
			if m["eventType"] == "identity.inbound" {
				if m["plugin"] != "bridge" {
					t.Fatalf("identity.inbound 只给代管插件：%v", m)
				}
				inbound = m["event"].(map[string]any)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("代管插件没有收到 identity.inbound")
		}
		time.Sleep(20 * time.Millisecond)
	}
	question, _ := inbound["question"].(map[string]any)
	if inbound["identityId"] != boss.ID.String() || inbound["channelId"] != "lark" || inbound["messageId"] != q.ID.String() || question["id"] != q.ID.String() || len(question["options"].([]any)) != 2 {
		t.Fatalf("identity.inbound：%v", inbound)
	}

	bridge := []string{"Authorization", "Bearer " + e.pluginTokenFor("bridge")}
	other := []string{"Authorization", "Bearer " + e.pluginTokenFor("other")}
	read := decode[apitypes.InboxMessage](t, e.expect(200, "GET", "/external-identities/"+boss.ID.String()+"/inbox/"+q.ID.String(), nil, bridge...))
	if read.Text != "今晚上线吗？" {
		t.Fatalf("读代管身份的消息：%+v", read)
	}
	e.expect(200, "GET", "/external-identities/"+boss.ID.String()+"/inbox", nil, bridge...)
	e.console().expectError(403, "forbidden", "GET", "/external-identities/"+ops.ID.String()+"/inbox", nil, bridge...)
	e.console().expectError(403, "forbidden", "GET", "/external-identities/"+boss.ID.String()+"/inbox/"+q.ID.String(), nil, other...)
	reply := "/inbox/messages/" + q.ID.String() + "/reply"
	e.console().expectError(403, "forbidden", "POST", reply, map[string]any{"optionId": "1", "asIdentityId": boss.ID}, other...)
	e.console().expectError(403, "forbidden", "POST", reply, map[string]any{"text": "我自己"}, bridge...)
	e.expectError(422, "validation_failed", "POST", reply, map[string]any{"optionId": "1", "asIdentityId": boss.ID})

	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", reply, map[string]any{"optionId": "1", "asIdentityId": boss.ID}, bridge...))
	if answer.From.Kind != "external" || *answer.From.ID != boss.ID || answer.AnswerTo == nil || answer.AnswerTo.MessageID != q.ID {
		t.Fatalf("以身份的名义回答：%+v", answer)
	}
	if m, err := e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || m.Question.State != "answered" {
		t.Fatalf("问题记为已回答：%+v %v", m.Question, err)
	}
	var actor string
	var onBehalfOf *uuid.UUID
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_kind||'|'||actor_address, on_behalf_of FROM audit_log WHERE operation='replyInboxMessage' AND status=201 ORDER BY at DESC LIMIT 1`).Scan(&actor, &onBehalfOf); err != nil ||
		actor != "plugin|plugin:bridge" || onBehalfOf == nil || *onBehalfOf != boss.ID {
		t.Fatalf("审计：%q %v %v", actor, onBehalfOf, err)
	}

	e.expect(204, "DELETE", "/plugins/bridge", nil)
	if x := decode[apitypes.ExternalIdentity](t, e.expect(200, "GET", "/external-identities/"+boss.ID.String(), nil)); x.ManagedBy != nil {
		t.Fatalf("卸载插件后绑定清空：%+v", x)
	}
}

// 任务节点向代管身份提问后，插件以身份的名义回答与身份亲自回答效果相同：问题记为已回答，任务里的问题不再 open，
// 节点恰好得到一次 inbox_reminder 轮次（由等待转回运行）。
func TestPluginAnswersTaskQuestionAsIdentity(t *testing.T) {
	for _, by := range []string{"plugin", "identity"} {
		t.Run(by, func(t *testing.T) {
			e, _, d := taskEnv(t)
			ctx := context.Background()
			e.installManager("bridge")
			boss, token := e.identity("boss")
			e.expect(200, "PUT", "/external-identities/"+boss.ID.String()+"/manager", map[string]any{"managedBy": map[string]any{"pluginId": "bridge", "channelId": "lark"}})
			node := d.Nodes[0]
			e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
			if _, err := e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, node.RuntimeID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.st.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
				m.Record.View.Task.Status = "running"
				m.Record.Current().Status = "running"
				return nil, nil
			}); err != nil {
				t.Fatal(err)
			}
			q := decode[apitypes.InboxMessage](t, e.agentCaller(node.RuntimeID).expect(201, "POST", "/inbox/messages",
				map[string]any{"to": "@boss", "text": "要不要继续？", "options": []string{"继续", "停下"}, "nodeRunId": node.ID}))
			reply := "/inbox/messages/" + q.ID.String() + "/reply"
			var answer apitypes.InboxMessage
			if by == "plugin" {
				answer = decode[apitypes.InboxMessage](t, e.expect(201, "POST", reply, map[string]any{"optionId": "1", "asIdentityId": boss.ID}, "Authorization", "Bearer "+e.pluginTokenFor("bridge")))
			} else {
				answer = decode[apitypes.InboxMessage](t, e.external(token).expect(201, "POST", reply, map[string]any{"optionId": "1"}))
			}
			// 节点的 Inbox 历史里有任务实例与外部身份之间的这一问一答。
			page := decode[map[string]any](t, e.expect(200, "GET", "/tasks/"+d.Task.ID.String()+"/nodes/"+node.ID.String()+"/inbox-messages", nil))
			var ids []string
			for _, it := range page["items"].([]any) {
				m := it.(map[string]any)["message"].(map[string]any)
				ids = append(ids, m["id"].(string)+"/"+m["from"].(map[string]any)["kind"].(string))
			}
			if want := q.ID.String() + "/agent," + answer.ID.String() + "/external"; strings.Join(ids, ",") != want {
				t.Fatalf("节点 Inbox 历史：%v，期望 %s", ids, want)
			}
			for range 3 {
				if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if m, err := e.st.GetInboxMessage(ctx, q.ID, 200); err != nil || m.Question.State != "answered" || *m.Question.Answer.OptionID != "1" {
				t.Fatalf("问题：%+v %v", m.Question, err)
			}
			var turns int
			if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='inbox_reminder'`, node.RuntimeID).Scan(&turns); err != nil || turns != 1 {
				t.Fatalf("节点应恰好得到一次提醒轮次：%d %v", turns, err)
			}
			record, err := e.st.GetTask(ctx, d.Task.ID)
			if err != nil || record.Current().Question == nil || record.Current().Question.Status == "open" {
				t.Fatalf("任务里的问题：%+v %v", record.Current().Question, err)
			}
		})
	}
}
