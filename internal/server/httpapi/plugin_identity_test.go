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

// installManager installs a plugin that can manage external identities: channel lark declares manageIdentities, channel
// plain does not.
func (e *env) installManager(id string) {
	e.t.Helper()
	dir := writePluginDir(e.t, id, "identities.manage")
	patchManifest(e.t, dir, func(ln map[string]any) {
		ln["channels"] = []any{map[string]any{"id": "lark", "label": "飞书", "manageIdentities": true}, map[string]any{"id": "plain", "label": "普通"}}
	})
	e.installPlugin(dir)
}

// Plugins managing external identities (plugins/SPEC.md §12): binding only accepts installed plugins whose channel
// declares managing; questions sent to a managed identity reach the managing plugin as identity.inbound; the managing
// plugin reads that identity's Inbox with identities.manage and answers in its name (no inbox.send needed), and the
// audit records the identity acted for; a plugin that is not the manager, a plugin with neither asIdentityId nor
// inbox.send, and a non-plugin caller with asIdentityId are all rejected. Uninstalling the plugin clears the binding.
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

// After a task node asks a managed identity, a plugin answering in the identity's name has the same effect as the
// identity answering itself: the question is marked answered, the question in the task is no longer open, and the node
// gets exactly one inbox_reminder turn (from waiting back to running).
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
			// The node's Inbox history holds this Q&A between the task instance and the external identity.
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
