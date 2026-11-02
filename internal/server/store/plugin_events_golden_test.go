package store_test

// 插件事件对拍：同一组写入在旧实现（PostgreSQL 触发器，647fe00）与新实现（SQLite 捕获触发器 + Go）下
// 产生的插件事件逐条相同。testdata/plugin_events_golden.json 是旧代码跑同一场景的结果，PostgreSQL 代码删除后
// 作为固定的记录保留：生成时把本文件与 PostgreSQL 版的 goldenStore、goldenTx 放进 647fe00 的临时工作树运行。
// 设置 LOOP_NOW_GOLDEN_WRITE=<路径> 时把本次结果写到该路径而不比较。
// 场景只用两边签名相同的 Store 方法与两边都能执行的 SQL；事务与夹具由 goldenStore、goldenTx 提供（各自一份）。
// 事件 ID 换成场景里登记的名字，场景写入的时间记作 T+分钟，写入时刻记作 <now>。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

type goldenEvent struct {
	Type    string   `json:"type"`
	At      string   `json:"at"`
	Project string   `json:"project,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Task    string   `json:"task,omitempty"`
	Run     string   `json:"run,omitempty"`
	Data    any      `json:"data"`
	To      []string `json:"to"`
}

// goldenBase 是场景里写入的时间的起点；事件时间等于 goldenBase 加整分钟时记作 T+分钟，其余（写入时刻）记作 <now>。
var goldenBase = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func goldenTimeLabel(at time.Time) string {
	d := at.Sub(goldenBase)
	if d >= 0 && d%time.Minute == 0 && d < 24*time.Hour {
		return fmt.Sprintf("T+%d", int(d/time.Minute))
	}
	return "<now>"
}

// labeler 把 UUID 换成稳定的名字：场景里登记过的用登记名，其余按第一次出现编号。
type labeler struct {
	names map[string]string
	next  int
}

func (l *labeler) add(id uuid.UUID, name string) {
	if l.names == nil {
		l.names = map[string]string{}
	}
	l.names[id.String()] = name
}

func (l *labeler) label(s string) string {
	if l.names == nil {
		l.names = map[string]string{}
	}
	if n, ok := l.names[s]; ok {
		return n
	}
	l.next++
	n := fmt.Sprintf("id#%d", l.next)
	l.names[s] = n
	return n
}

var goldenTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)

func (l *labeler) normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[k] = l.normalize(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = l.normalize(val)
		}
		return out
	case string:
		if _, err := uuid.Parse(x); err == nil && len(x) == 36 {
			return l.label(x)
		}
		if goldenTime.MatchString(x) {
			if at, err := time.Parse(time.RFC3339Nano, x); err == nil {
				return goldenTimeLabel(at)
			}
			return "<time?>"
		}
		return x
	}
	return v
}

func (l *labeler) idLabel(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return l.label(id.String())
}

func goldenEvents(t *testing.T, s *store.Store, l *labeler) []goldenEvent {
	t.Helper()
	ctx := context.Background()
	to := map[string][]string{}
	rows, err := s.Pool.Query(ctx, `SELECT event_id, plugin_id FROM plugin_event_deliveries ORDER BY event_id, plugin_id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ev, plugin string
		if err := rows.Scan(&ev, &plugin); err != nil {
			t.Fatal(err)
		}
		to[ev] = append(to[ev], plugin)
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT id, type, at, project_id, agent_id, task_id, run_id, data FROM plugin_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []goldenEvent
	for rows.Next() {
		var id, typ string
		var at time.Time
		var project, agent, task, run *uuid.UUID
		var data map[string]any
		if err := rows.Scan(&id, &typ, &at, &project, &agent, &task, &run, &data); err != nil {
			t.Fatal(err)
		}
		plugins := to[id]
		sort.Strings(plugins)
		out = append(out, goldenEvent{Type: typ, At: goldenTimeLabel(at), Project: l.idLabel(project), Agent: l.idLabel(agent), Task: l.idLabel(task), Run: l.idLabel(run),
			Data: l.normalize(data), To: plugins})
	}
	return out
}

func TestPluginEventsGolden(t *testing.T) {
	ctx := context.Background()
	s := goldenStore(t)
	l := &labeler{}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	at := func(minutes int) time.Time { return goldenBase.Add(time.Duration(minutes) * time.Minute) }

	dc, err := s.EnsureDefaults(ctx, "组织", "项目")
	must(err)
	project := dc.Project.ID
	l.add(project, "project")
	l.add(dc.Organization.ID, "org")
	all := []string{"agent.created", "agent.updated", "agent.deleted", "run.queued", "run.started", "run.ended", "pi.started", "pi.exited",
		"model.retry", "context.compacted", "inbox.received", "inbox.read", "inbox.cancelled", "question.asked", "question.answered",
		"sleep.requested", "sleep.woken", "plugin.config_changed", "plugin.enabled", "plugin.disabled", "folder.changed"}
	for _, p := range []struct {
		id     string
		events []string
		on     bool
	}{{"bridge", []string{"inbox.received"}, true}, {"chat", []string{"inbox.received"}, true}, {"obs", all, true}, {"other", all, false}} {
		m, _ := json.Marshal(map[string]any{"events": p.events})
		exec(`INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES($1,$1,'{}','1',$2,'/x','installed')`, p.id, string(m))
		if p.on {
			exec(`INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,$2,true)`, project, p.id)
		}
	}

	// Agent：项目内、平台、改名、无关的更新、删除。
	a1, err := s.CreateAgent(ctx, store.ProjectScope(project), "甲", "anthropic", "m", "")
	must(err)
	l.add(a1.ID, "agent:甲")
	p1, err := s.CreateAgent(ctx, store.PlatformScope(), "乙", "anthropic", "m", "")
	must(err)
	l.add(p1.ID, "agent:乙")
	name := "甲2"
	_, err = s.UpdateAgent(ctx, a1.ID, store.AgentPatch{Name: &name})
	must(err)
	exec(`UPDATE agents SET updated_at=$2 WHERE id=$1`, a1.ID, at(1))
	must(s.SoftDeleteAgent(ctx, p1.ID))

	// 运行：排队、派发、进入回复、用量、重试、压缩、进程、结束；直接开始的一轮被打断。
	w, err := s.CreateAgent(ctx, store.ProjectScope(project), "工", "anthropic", "m", "")
	must(err)
	l.add(w.ID, "agent:工")
	instance := uuid.New()
	l.add(instance, "instance")
	r1 := uuid.New()
	l.add(r1, "run:1")
	_, _, err = s.InsertMessageAndRun(ctx, store.NewRun{AgentID: w.ID, RunID: r1, Text: "你好", WhenBusy: "queue", InstanceID: instance})
	must(err)
	_, started, err := s.StartQueuedRun(ctx, r1, instance)
	must(err)
	if !started {
		t.Fatal("排队的执行应能开始")
	}
	must(s.SetRunRunning(ctx, r1, 1))
	must(s.AddUsage(ctx, store.UsageRecord{AgentID: w.ID, RunID: &r1, Kind: "assistant", Input: 100, Output: 20, CacheRead: 300, At: at(2)}))
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, RunID: &r1, Type: "model.retry_started", At: at(3), Data: map[string]any{"attempt": 2, "delayMs": 4000, "errorMessage": "不外泄"}}))
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, RunID: &r1, Type: "context.compaction", At: at(4), Data: map[string]any{"reason": "threshold"}}))
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, Type: "pi.started", At: at(5)}))
	_, _, _, err = s.FinishRun(ctx, r1, store.RunEnd{Status: "completed", Text: "好的"})
	must(err)
	r2 := uuid.New()
	l.add(r2, "run:2")
	_, _, err = s.InsertMessageAndRun(ctx, store.NewRun{AgentID: w.ID, RunID: r2, Text: "直接开始", Start: true, InstanceID: instance})
	must(err)
	_, _, _, err = s.FinishRun(ctx, r2, store.RunEnd{Status: "interrupted", EndReason: "user_interrupt", ErrorCode: "x"})
	must(err)
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, Type: "pi.stopped", At: at(6), Data: map[string]any{"reason": "idle_timeout"}}))
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, Type: "pi.exited", At: at(7), Data: map[string]any{"stderrTail": "不外泄"}}))
	must(s.AddEvent(ctx, store.LifecycleEvent{AgentID: w.ID, RunID: &r2, Type: "run.phase", At: at(8)}))

	// 收件与提问：同一事务里写消息与提问（提交时可见）；已读、作答、取消。
	m1, m2 := uuid.New(), uuid.New()
	l.add(m1, "msg:1")
	l.add(m2, "msg:2")
	goldenTx(t, s, func(exec func(string, ...any)) {
		exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,text,created_at) VALUES($1,$1,'agent',$2,'console',$3,$4)`, m1, w.ID, strings.Repeat("长", 100), at(10))
		exec(`INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,$2,$3)`, m1, `[{"id":"yes","label":"好"},{"id":"no","label":"不"}]`, at(70))
	})
	exec(`UPDATE inbox_messages SET read_at=$2 WHERE id=$1`, m1, at(11))
	exec(`UPDATE inbox_questions SET state='answered', answer_option_id='yes', answered_at=$2 WHERE message_id=$1`, m1, at(12))
	exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,to_agent_id,when_busy,text,created_at) VALUES($1,$1,'console','agent',$2,'queue','稍后处理',$3)`, m2, w.ID, at(13))
	exec(`UPDATE inbox_messages SET cancelled_at=$2 WHERE id=$1`, m2, at(14))

	// 代管身份：identity.inbound 只给代管插件，它不再收到 inbox.received；没有代管的身份照常。
	boss, err := s.CreateExternalIdentity(ctx, "boss")
	must(err)
	l.add(boss.ID, "identity:boss")
	ops, err := s.CreateExternalIdentity(ctx, "ops")
	must(err)
	l.add(ops.ID, "identity:ops")
	_, err = s.SetIdentityManager(ctx, boss.ID, &store.IdentityManager{PluginID: "bridge", ChannelID: "lark"})
	must(err)
	m3, m4, m5 := uuid.New(), uuid.New(), uuid.New()
	l.add(m3, "msg:3")
	l.add(m4, "msg:4")
	l.add(m5, "msg:5")
	goldenTx(t, s, func(exec func(string, ...any)) {
		exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text,created_at) VALUES($1,$1,'agent',$2,'external',$3,'上线吗？',$4)`, m3, w.ID, boss.ID, at(20))
		exec(`INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,$2,$3)`, m3, `[{"id":"1","label":"上线"}]`, at(80))
	})
	exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_identity_id,text,created_at) VALUES($1,$1,'agent',$2,'external',$3,'没人代管',$4)`, m4, w.ID, ops.ID, at(21))

	// 插件线索：发给插件的消息以 channel.outbound 只给那个插件。
	party := uuid.New()
	l.add(party, "party:chat")
	exec(`INSERT INTO plugin_parties(id,plugin_id,display_name) VALUES($1,'chat','聊天')`, party)
	exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,to_plugin_party_id,plugin_channel,text,created_at) VALUES($1,$1,'agent',$2,'plugin',$3,'c1','回复线索',$4)`, m5, w.ID, party, at(22))

	// 一个事务里混合：收件（原来是延迟触发器）在提交时最后处理；两个库的立即变更按语句顺序。
	m6 := uuid.New()
	l.add(m6, "msg:6")
	goldenTx(t, s, func(exec func(string, ...any)) {
		exec(`INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,text,created_at) VALUES($1,$1,'agent',$2,'console','混合',$3)`, m6, w.ID, at(23))
		exec(`INSERT INTO lifecycle_events(id,agent_id,type,at,data) VALUES($1,$2,'pi.stopped',$3,'{"reason":"idle_timeout"}')`, uuid.New(), w.ID, at(24))
		exec(`UPDATE agents SET name='工2', config_version=config_version+1 WHERE id=$1`, w.ID)
		exec(`INSERT INTO lifecycle_events(id,agent_id,type,at,data) VALUES($1,$2,'pi.exited',$3,'{}')`, uuid.New(), w.ID, at(25))
	})

	// sleep：请求、入睡（不发事件）、醒来。
	sleep := uuid.New()
	l.add(sleep, "sleep")
	exec(`INSERT INTO agent_sleeps(id,scope_key,scope_kind,mode,agent_id,session_epoch,source_run_id,action_id,body_hash,receipt,state,seconds,timer_run_id,revision,requested_at)
		VALUES($1,$2,'normal','normal',$3,1,$4,$5,$6,'{}','requested',60,$7,1,$8)`, sleep, "normal/"+w.ID.String(), w.ID, r1, uuid.New(), make([]byte, 32), uuid.New(), at(30))
	exec(`UPDATE agent_sleeps SET state='sleeping', entered_at=$2, wake_at=$3 WHERE id=$1`, sleep, at(31), at(32))
	exec(`UPDATE agent_sleeps SET state='woken', ended_at=$2, end_reason='deadline', wake_run_id=$3 WHERE id=$1`, sleep, at(32), r2)

	// 代码直接发的观察类事件：配置变化、项目里停用与启用、文件夹变化。
	_, err = s.SavePluginConfig(ctx, "obs", &project, store.PluginConfigUpdate{ExpectedRevision: 0, Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) {
		c.Values = map[string]any{"k": "v"}
		return c, nil
	}})
	must(err)
	keep := store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}
	_, _, err = s.SaveProjectPlugin(ctx, project, "other", true, keep)
	must(err)
	_, _, err = s.SaveProjectPlugin(ctx, project, "other", false, keep)
	must(err)
	must(s.EmitPluginEvent(ctx, "folder.changed", &project, &w.ID, nil, nil, map[string]any{"level": "agent", "path": "notes"}))

	// 全局停用：之后的写入不产生事件。
	exec(`UPDATE plugin_settings SET disabled=true`)
	_, err = s.CreateAgent(ctx, store.ProjectScope(project), "丙", "anthropic", "m", "")
	must(err)

	got := goldenEvents(t, s, l)
	raw, err := json.MarshalIndent(got, "", "  ")
	must(err)
	if path := os.Getenv("LOOP_NOW_GOLDEN_WRITE"); path != "" {
		must(os.WriteFile(path, append(raw, '\n'), 0o644))
		t.Logf("写入 %d 条事件到 %s", len(got), path)
		return
	}
	want, err := os.ReadFile("testdata/plugin_events_golden.json")
	must(err)
	var wantEvents []goldenEvent
	must(json.Unmarshal(want, &wantEvents))
	n := max(len(wantEvents), len(got))
	for i := range n {
		var w, g string
		if i < len(wantEvents) {
			b, _ := json.Marshal(wantEvents[i])
			w = string(b)
		}
		if i < len(got) {
			b, _ := json.Marshal(got[i])
			g = string(b)
		}
		if w != g {
			t.Errorf("第 %d 条事件不同：\n  旧：%s\n  新：%s", i+1, w, g)
		}
	}
	if !t.Failed() {
		t.Logf("%d 条事件与旧实现逐条相同", len(got))
	}
}
