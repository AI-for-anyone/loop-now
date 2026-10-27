package store_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// pluginEvents 是写入的插件事件，按 id 排序；data 只取 keys 里的字段。
type pluginEvent struct {
	Type      string
	ProjectID *uuid.UUID
	AgentID   *uuid.UUID
	TaskID    *uuid.UUID
	RunID     *uuid.UUID
	Data      map[string]any
	To        []string
}

func readPluginEvents(t *testing.T, s *store.Store) []pluginEvent {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT e.type,e.project_id,e.agent_id,e.task_id,e.run_id,e.data,
		(SELECT array_agg(plugin_id ORDER BY plugin_id) FROM plugin_event_deliveries d WHERE d.event_id=e.id) FROM plugin_events e ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []pluginEvent
	for rows.Next() {
		var ev pluginEvent
		if err := rows.Scan(&ev.Type, &ev.ProjectID, &ev.AgentID, &ev.TaskID, &ev.RunID, &ev.Data, &ev.To); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func eventTypes(evs []pluginEvent) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func subscribe(t *testing.T, s *store.Store, id string, events []string) {
	t.Helper()
	m, _ := json.Marshal(map[string]any{"events": events})
	if _, err := s.Pool.Exec(context.Background(), `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$1,'{}','1',$2,'/x','installed')`, id, m); err != nil {
		t.Fatal(err)
	}
}

// 触发器与任务事务按订阅与启用写观察类事件：项目内事件只给在该项目启用了插件的，平台（项目为空）的给全部订阅者；
// 收件事件在提交时看得到同一事务里的提问；正文只给前 80 个字符；全局停用时不写。
func TestPluginEventProduction(t *testing.T) {
	ctx := context.Background()
	s := taskConsoleStore(t)
	task, node, agent, initial := taskConsoleRecord(t, s)
	var project uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, task).Scan(&project); err != nil {
		t.Fatal(err)
	}
	all := []string{"agent.created", "agent.updated", "agent.deleted", "run.queued", "run.started", "run.ended", "pi.started", "pi.exited", "model.retry", "context.compacted",
		"inbox.received", "inbox.read", "question.asked", "question.answered", "sleep.requested", "sleep.woken", "task.blocked", "node.blocked"}
	subscribe(t, s, "obs", all)
	subscribe(t, s, "other", all) // 订阅了，但没有在项目里启用
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	must(`INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,'obs',true)`, project)
	must(`DELETE FROM plugin_events`)

	// Agent：项目内的只给 obs；平台 Agent 的给两个订阅者。
	a, err := s.CreateAgent(ctx, store.ProjectScope(project), "观察员", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAgent(ctx, store.PlatformScope(), "巡检", "anthropic", "m", ""); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE agents SET name='观察员2', config_version=config_version+1 WHERE id=$1`, a.ID)
	must(`UPDATE agents SET updated_at=now() WHERE id=$1`, a.ID) // 没有配置变化：不写事件
	if err := s.SoftDeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	evs := readPluginEvents(t, s)
	if got := strings.Join(eventTypes(evs), ","); got != "agent.created,agent.created,agent.updated,agent.deleted" {
		t.Fatalf("Agent 事件：%s", got)
	}
	if !slices.Equal(evs[0].To, []string{"obs"}) || evs[1].ProjectID != nil || !slices.Equal(evs[1].To, []string{"obs", "other"}) ||
		evs[0].Data["name"] != "观察员" || !strings.HasSuffix(evs[0].Data["address"].(string), "/观察员") || evs[2].Data["name"] != "观察员2" {
		t.Fatalf("Agent 事件内容：%+v", evs)
	}
	must(`DELETE FROM plugin_events`)

	// 运行与进程：lifecycle_events 的类型名换成插件事件名，run.ended 带这一轮的 token。
	run := uuid.New()
	base := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	must(`INSERT INTO runs(id,agent_id,user_message_id,config_version,priority,status,origin,queued_at,started_at,running_at,ended_at,end_reason)
		VALUES($1,$2,$3,1,1,'interrupted','console',$4,$4,$4,$4,'user_interrupt')`, run, agent, uuid.New(), base)
	life := func(typ string, data map[string]any) {
		t.Helper()
		if err := s.AddEvent(ctx, store.LifecycleEvent{AgentID: agent, RunID: &run, Type: typ, At: base, Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO usage_records(id,at,agent_id,run_id,kind,input,output,cache_read) VALUES($1,$2,$3,$4,'assistant',100,20,300)`, uuid.New(), base, agent, run)
	life("run.dispatched", nil)
	life("run.running", nil)
	life("model.retry_started", map[string]any{"attempt": 2, "delayMs": 4000, "errorMessage": "不应外泄"})
	life("context.compaction", map[string]any{"reason": "threshold"})
	life("run.ended", map[string]any{"status": "interrupted", "endReason": "user_interrupt"})
	life("pi.stopped", map[string]any{"reason": "idle_timeout"})
	life("pi.exited", map[string]any{"stderrTail": "不应外泄"})
	life("run.phase", nil) // 不是插件事件
	evs = readPluginEvents(t, s)
	if got := strings.Join(eventTypes(evs), ","); got != "run.queued,run.started,model.retry,context.compacted,run.ended,pi.exited,pi.exited" {
		t.Fatalf("运行事件：%s", got)
	}
	ended := evs[4]
	tokens, _ := ended.Data["tokens"].(map[string]any)
	if ended.Data["origin"] != "console" || ended.Data["status"] != "interrupted" || ended.Data["endReason"] != "user_interrupt" || tokens["total"] != float64(420) ||
		*ended.RunID != run || *ended.ProjectID != project {
		t.Fatalf("run.ended：%+v", ended)
	}
	if evs[2].Data["attempt"] != float64(2) || evs[2].Data["errorMessage"] != nil || evs[5].Data["reason"] != "idle_timeout" || evs[6].Data["reason"] != "unexpected_exit" || evs[6].Data["stderrTail"] != nil {
		t.Fatalf("进程与重试事件：%+v", evs)
	}
	must(`DELETE FROM plugin_events`)

	// 收件与提问：提交时触发，同一事务里的提问已可见；正文只给前 80 个字符。
	msg := uuid.New()
	text := strings.Repeat("长", 100)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,text,created_at) VALUES($1,$1,'agent',$2,'console',$3,$4)`,
		`INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[]',now()+interval '1 hour')`,
	} {
		args := []any{msg}
		if strings.Contains(q, "inbox_messages") {
			args = append(args, agent, text, base)
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	must(`UPDATE inbox_messages SET read_at=now() WHERE id=$1`, msg)
	must(`UPDATE inbox_questions SET state='answered', answer_option_id='yes', answered_at=now() WHERE message_id=$1`, msg)
	evs = readPluginEvents(t, s)
	if got := strings.Join(eventTypes(evs), ","); got != "inbox.received,question.asked,inbox.read,question.answered" {
		t.Fatalf("收件事件：%s", got)
	}
	rec := evs[0].Data
	from, _ := rec["from"].(map[string]any)
	to, _ := rec["to"].(map[string]any)
	if rec["isQuestion"] != true || len([]rune(rec["excerpt"].(string))) != 80 || from["kind"] != "agent" || from["id"] != agent.String() || to["kind"] != "console" ||
		*evs[0].AgentID != agent || evs[3].Data["optionId"] != "yes" {
		t.Fatalf("收件事件内容：%+v", evs)
	}
	must(`DELETE FROM plugin_events`)

	// 任务与节点：任务事务比较前后两份文档。
	if _, err := s.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		reason := "缺少输入"
		m.Record.View.Task.Status, m.Record.View.Task.Reason = "blocked", &reason
		for i := range m.Record.View.Nodes {
			if m.Record.View.Nodes[i].ID == node {
				m.Record.View.Nodes[i].Status, m.Record.View.Nodes[i].Reason = "blocked", &reason
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	evs = readPluginEvents(t, s)
	if got := strings.Join(eventTypes(evs), ","); got != "task.blocked,node.blocked" {
		t.Fatalf("任务事件：%s", got)
	}
	if *evs[0].TaskID != task || evs[0].Data["reason"] != "缺少输入" || evs[1].Data["nodeRunId"] != node.String() || *evs[1].AgentID != agent {
		t.Fatalf("任务事件内容：%+v", evs)
	}
	_ = initial
	must(`DELETE FROM plugin_events`)

	// sleep 与全局停用。
	sleep := uuid.New()
	must(`INSERT INTO agent_sleeps(id,scope_key,scope_kind,mode,agent_id,session_epoch,source_run_id,action_id,body_hash,receipt,state,seconds,timer_run_id,revision)
		VALUES($1,'normal/'||$2::uuid::text,'normal','normal',$2::uuid,1,$3,$4,$5,'{}','requested',60,$6,1)`, sleep, agent, uuid.New(), uuid.New(), make([]byte, 32), uuid.New())
	must(`UPDATE agent_sleeps SET state='sleeping', entered_at=now(), wake_at=now()+interval '1 minute' WHERE id=$1`, sleep)
	must(`UPDATE plugin_settings SET disabled=true`)
	must(`UPDATE agent_sleeps SET state='woken', ended_at=now(), end_reason='deadline', wake_run_id=$2 WHERE id=$1`, sleep, uuid.New())
	evs = readPluginEvents(t, s)
	if got := strings.Join(eventTypes(evs), ","); got != "sleep.requested" || evs[0].Data["sleepId"] != sleep.String() {
		t.Fatalf("sleep 与停用：%s %+v", got, evs)
	}
}
