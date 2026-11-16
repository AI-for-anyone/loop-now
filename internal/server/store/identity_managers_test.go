package store_test

import (
	"context"
	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A message sent to a managed identity is delivered at commit time as identity.inbound only to the managing plugin
// (regardless of subscriptions and project enablement), with the sender, project, task, node, and the question's
// options and deadline; the managing plugin no longer receives inbox.received for this message, while other
// subscribers receive it as usual. A message to an identity without a manager produces no identity.inbound.
// Uninstalling the plugin deletes the bindings as well.
func TestIdentityInboundEvents(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, turn := taskConsoleRecord(t, s)
	var project uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, task).Scan(&project); err != nil {
		t.Fatal(err)
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	subscribe(t, s, "bridge", []string{"inbox.received"})
	subscribe(t, s, "obs", []string{"inbox.received"})
	must(`INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,'bridge',true),($1,'obs',true)`, project)
	boss, err := s.CreateExternalIdentity(ctx, "boss")
	if err != nil {
		t.Fatal(err)
	}
	ops, err := s.CreateExternalIdentity(ctx, "ops")
	if err != nil {
		t.Fatal(err)
	}
	x, err := s.SetIdentityManager(ctx, boss.ID, &store.IdentityManager{PluginID: "bridge", ChannelID: "lark"})
	if err != nil || x.ManagedBy == nil || x.ManagedBy.PluginID != "bridge" || x.ManagedBy.ChannelID != "lark" {
		t.Fatalf("bind: %+v %v", x, err)
	}
	if ok, err := s.IdentityManagedBy(ctx, boss.ID, "bridge"); err != nil || !ok {
		t.Fatalf("managed by: %v %v", ok, err)
	}
	must(`DELETE FROM plugin_events`)

	plain := uuid.New()
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,'external',$3,$4)`,
		plain, agent, boss.ID, strings.Repeat("x", 100))
	question := uuid.New()
	if err := s.Pool.InTx(ctx, func(tx *db.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,from_runtime_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,$3,'external',$4,'Ship it?')`, []any{question, agent, turn.RuntimeID, boss.ID}},
			{`INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[{"id":"1","label":"Ship"},{"id":"2","label":"Wait a bit"}]',now()+3600000000)`, []any{question}},
			{`INSERT INTO task_questions(question_message_id,task_id,runtime_id,node_run_id) VALUES($1,$2,$3,$4)`, []any{question, task, turn.RuntimeID, node}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,'external',$3,'unmanaged')`, uuid.New(), agent, ops.ID)

	evs := readPluginEvents(t, s)
	var got []string
	for _, e := range evs {
		got = append(got, e.Type+"→"+strings.Join(e.To, "+"))
	}
	if strings.Join(got, ",") != "identity.inbound→bridge,inbox.received→obs,identity.inbound→bridge,inbox.received→obs,inbox.received→bridge+obs" {
		t.Fatalf("events and receiving plugins: %v", got)
	}
	first, second := evs[0], evs[2]
	from, _ := first.Data["from"].(map[string]any)
	if first.Data["identityId"] != boss.ID.String() || first.Data["channelId"] != "lark" || first.Data["messageId"] != plain.String() || first.Data["isQuestion"] != false ||
		first.Data["question"] != nil || len([]rune(first.Data["excerpt"].(string))) != 80 || from["kind"] != "agent" || from["agentId"] != agent.String() || from["name"] != "node-agent" ||
		first.Data["projectId"] != project.String() || first.Data["taskId"] != nil || *first.ProjectID != project || *first.AgentID != agent {
		t.Fatalf("plain message: %+v", first)
	}
	q, _ := second.Data["question"].(map[string]any)
	options, _ := q["options"].([]any)
	if second.Data["isQuestion"] != true || q["id"] != question.String() || q["allowText"] != true || q["deadline"] == nil || len(options) != 2 ||
		options[1].(map[string]any)["label"] != "Wait a bit" || second.Data["taskId"] != task.String() || second.Data["nodeRunId"] != node.String() || *second.TaskID != task {
		t.Fatalf("question: %+v", second)
	}

	must(`DELETE FROM plugins WHERE id='bridge'`)
	if x, err := s.GetExternalIdentity(ctx, boss.ID); err != nil || x.ManagedBy != nil {
		t.Fatalf("uninstalling the plugin should clear the binding: %+v %v", x, err)
	}
}
