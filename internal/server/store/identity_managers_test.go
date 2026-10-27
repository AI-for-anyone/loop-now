package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// 发给代管身份的消息在提交时以 identity.inbound 只投给代管的插件（不看订阅与项目启用），带发件方、项目、任务、节点、
// 提问的选项与期限；代管的插件不再收到这条消息的 inbox.received，其他订阅者照常。发给没有代管的身份没有 identity.inbound。
// 卸载插件时绑定一起删除；有绑定时 032 不回退，没有时可以回退再升级。
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
			t.Fatalf("%s：%v", sql, err)
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
		t.Fatalf("绑定：%+v %v", x, err)
	}
	if ok, err := s.IdentityManagedBy(ctx, boss.ID, "bridge"); err != nil || !ok {
		t.Fatalf("代管：%v %v", ok, err)
	}
	must(`DELETE FROM plugin_events`)

	plain := uuid.New()
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,'external',$3,$4)`,
		plain, agent, boss.ID, strings.Repeat("长", 100))
	question := uuid.New()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,from_runtime_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,$3,'external',$4,'上线吗？')`, []any{question, agent, turn.RuntimeID, boss.ID}},
		{`INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[{"id":"1","label":"上线"},{"id":"2","label":"再等等"}]',now()+interval '1 hour')`, []any{question}},
		{`INSERT INTO task_questions(question_message_id,task_id,runtime_id,node_run_id) VALUES($1,$2,$3,$4)`, []any{question, task, turn.RuntimeID, node}},
	} {
		if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text) VALUES($1,$1,'agent',$2,'external',$3,'没人代管')`, uuid.New(), agent, ops.ID)

	evs := readPluginEvents(t, s)
	var got []string
	for _, e := range evs {
		got = append(got, e.Type+"→"+strings.Join(e.To, "+"))
	}
	if strings.Join(got, ",") != "identity.inbound→bridge,inbox.received→obs,identity.inbound→bridge,inbox.received→obs,inbox.received→bridge+obs" {
		t.Fatalf("事件与收件插件：%v", got)
	}
	first, second := evs[0], evs[2]
	from, _ := first.Data["from"].(map[string]any)
	if first.Data["identityId"] != boss.ID.String() || first.Data["channelId"] != "lark" || first.Data["messageId"] != plain.String() || first.Data["isQuestion"] != false ||
		first.Data["question"] != nil || len([]rune(first.Data["excerpt"].(string))) != 80 || from["kind"] != "agent" || from["agentId"] != agent.String() || from["name"] != "node-agent" ||
		first.Data["projectId"] != project.String() || first.Data["taskId"] != nil || *first.ProjectID != project || *first.AgentID != agent {
		t.Fatalf("普通消息：%+v", first)
	}
	q, _ := second.Data["question"].(map[string]any)
	options, _ := q["options"].([]any)
	if second.Data["isQuestion"] != true || q["id"] != question.String() || q["allowText"] != true || q["deadline"] == nil || len(options) != 2 ||
		options[1].(map[string]any)["label"] != "再等等" || second.Data["taskId"] != task.String() || second.Data["nodeRunId"] != node.String() || *second.TaskID != task {
		t.Fatalf("提问：%+v", second)
	}

	if err := s.MigrateDownTo(ctx, 31); err == nil || !strings.Contains(err.Error(), "identity_managers_down_refused") {
		t.Fatalf("有绑定时不回退：%v", err)
	}
	must(`DELETE FROM plugins WHERE id='bridge'`)
	if x, err := s.GetExternalIdentity(ctx, boss.ID); err != nil || x.ManagedBy != nil {
		t.Fatalf("卸载插件后绑定清空：%+v %v", x, err)
	}
	if err := s.MigrateDownTo(ctx, 31); err != nil {
		t.Fatalf("没有绑定时回退：%v", err)
	}
	if err := s.MigrateUpTo(ctx, 32); err != nil {
		t.Fatalf("再升级：%v", err)
	}
}
