package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// 迁移 00028：未读消息挂到同优先级（没有时第一条）的排队提醒；已没有待处理消息的排队提醒随即作废并记结束事件；
// 由节点自己提问触发的提醒轮次标为问题结束唤醒、不作废。有作废记录后拒绝回退。
func TestQueueItems028BackfillVoidAndDown(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	s := f.s
	must := func(sql string, args ...any) {
		t.Helper()
		if _, e := s.Pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("%s：%v", sql, e)
		}
	}
	var project uuid.UUID
	if e := s.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, f.task).Scan(&project); e != nil {
		t.Fatal(e)
	}
	agent := func(name string) uuid.UUID {
		a, e := s.CreateAgent(ctx, store.ProjectScope(project), name, "anthropic", "m", "")
		if e != nil {
			t.Fatal(e)
		}
		return a.ID
	}
	sender, b, c := agent("发件人"), agent("收件B"), agent("收件C")
	// 常态 Agent：B 有普通与打断两轮排队提醒，C 只有一轮普通提醒。
	remind := func(to uuid.UUID, text, whenBusy string) (uuid.UUID, uuid.UUID) {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		out, e := s.InsertInboxWithReminder(ctx, &store.NewInboxMessage{ID: id, From: store.AgentParty(sender), To: store.AgentParty(to), Text: text, WhenBusy: whenBusy},
			store.NewRun{AgentID: to, RunID: uuid.Must(uuid.NewV7()), Text: "平台提醒", WhenBusy: whenBusy, TriggerInboxID: &id})
		if e != nil || out.Run == nil {
			t.Fatal(out, e)
		}
		return id, out.Run.ID
	}
	m1, r1 := remind(b, "普通", "queue")
	m2, r0 := remind(b, "打断", "interrupt")
	m3, rc := remind(c, "改为打断", "queue")
	// 任务实例：一条外部消息的普通提醒轮次、一条严格消息的轮次、一轮由节点自己提问触发的提醒。
	d1 := f.deliverFrom("外部", "queue")
	s1 := f.send("严格", "queue")
	q := f.queue()
	u1, strict := q.Items[0].RunID, q.Items[1].RunID
	question, u2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,from_runtime_id,to_kind,text) VALUES($1,$1,'agent',$2,$3,'console','提问')`, question, f.agent, f.runtime)
	must(`INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,trigger_inbox_message_id,execution_mode)
		SELECT $1,task_id,runtime_id,node_run_id,$2,$3,'queued','inbox_reminder',0,'问题已回答',$4,execution_mode FROM task_turns WHERE id=$5`, u2, uuid.New(), uuid.New(), question, u1)

	if e := s.MigrateDownTo(ctx, 27); e != nil {
		t.Fatalf("没有取消或作废记录时可以回退：%v", e)
	}
	// 027 的数据：B 的打断消息已读，C 的消息改为打断（没有同优先级提醒），严格消息已读。
	must(`UPDATE inbox_messages SET read_at=now() WHERE id=ANY($1)`, []uuid.UUID{m2, s1})
	must(`UPDATE inbox_messages SET when_busy='interrupt' WHERE id=$1`, m3)
	if e := s.MigrateUpTo(ctx, 28); e != nil {
		t.Fatal(e)
	}
	link := func(id uuid.UUID, column string) *uuid.UUID {
		var v *uuid.UUID
		if e := s.Pool.QueryRow(ctx, `SELECT `+column+` FROM inbox_messages WHERE id=$1`, id).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	if v := link(m1, "reminder_run_id"); v == nil || *v != r1 {
		t.Fatalf("未读消息应挂到同优先级提醒：%v", v)
	}
	if v := link(m2, "reminder_run_id"); v != nil {
		t.Fatal("已读消息不挂提醒")
	}
	if v := link(m3, "reminder_run_id"); v == nil || *v != rc {
		t.Fatalf("没有同优先级提醒时挂到第一条：%v", v)
	}
	if v := link(d1, "reminder_turn_id"); v == nil || *v != u1 {
		t.Fatalf("任务外部消息应挂到提醒轮次：%v", v)
	}
	runEnd := func(id uuid.UUID) (string, string, int) {
		var status, reason string
		var events int
		if e := s.Pool.QueryRow(ctx, `SELECT status,coalesce(end_reason,''),(SELECT count(*) FROM lifecycle_events WHERE run_id=$1 AND type='run.ended' AND data->>'endReason'='no_pending_messages') FROM runs WHERE id=$1`, id).Scan(&status, &reason, &events); e != nil {
			t.Fatal(e)
		}
		return status, reason, events
	}
	if st, reason, ev := runEnd(r0); st != "interrupted" || reason != "no_pending_messages" || ev != 1 {
		t.Fatalf("没有待处理消息的提醒应作废并记结束事件：%s %s %d", st, reason, ev)
	}
	for _, id := range []uuid.UUID{r1, rc} {
		if st, _, _ := runEnd(id); st != "queued" {
			t.Fatalf("仍有待处理消息的提醒保持排队：%s", st)
		}
	}
	turn := func(id uuid.UUID) (string, string, bool) {
		var status, void string
		var wake bool
		if e := s.Pool.QueryRow(ctx, `SELECT status,coalesce(void_reason,''),question_wakeup FROM task_turns WHERE id=$1`, id).Scan(&status, &void, &wake); e != nil {
			t.Fatal(e)
		}
		return status, void, wake
	}
	if st, void, _ := turn(strict); st != "interrupted" || void != "no_pending_messages" {
		t.Fatalf("严格消息已读的轮次应作废：%s %s", st, void)
	}
	if st, void, wake := turn(u2); st != "queued" || void != "" || !wake {
		t.Fatalf("问题结束唤醒的轮次不作废：%s %s %v", st, void, wake)
	}
	if st, _, wake := turn(u1); st != "queued" || wake {
		t.Fatalf("外部消息的提醒轮次：%s %v", st, wake)
	}
	e := s.MigrateDownTo(ctx, 27)
	if e == nil || !strings.Contains(e.Error(), "queue_items_down_refused") {
		t.Fatalf("有作废记录时应拒绝回退：%v", e)
	}
}
