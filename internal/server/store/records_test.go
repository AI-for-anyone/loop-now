package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type recEnv struct {
	t     *testing.T
	s     *store.Store
	ctx   context.Context
	dc    store.DefaultContext
	agent uuid.UUID
}

func newRecEnv(t *testing.T) *recEnv {
	t.Helper()
	s := pgtest.NewStore(t)
	instance := uuid.Must(uuid.NewV7())
	s.InstanceID = &instance
	ctx := context.Background()
	dc, err := s.EnsureDefaults(ctx, "默认组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "A", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	return &recEnv{t: t, s: s, ctx: ctx, dc: dc, agent: a.ID}
}

func (e *recEnv) types(runID uuid.UUID) []string {
	e.t.Helper()
	evs, err := e.s.RunEvents(e.ctx, runID)
	if err != nil {
		e.t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func (e *recEnv) send(start bool, whenBusy string) store.Run {
	e.t.Helper()
	_, r, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "hi", Start: start,
		WhenBusy: whenBusy, InstanceID: uuid.Must(uuid.NewV7()), RequestID: "req-1"})
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// 执行状态的变化与对应事件在同一事务中写入，时间点写入 runs。
func TestRunLifecycleEventsAndTimes(t *testing.T) {
	e := newRecEnv(t)
	active := e.send(true, "")
	queued := e.send(false, "interrupt")

	if got := e.types(active.ID); strings.Join(got, ",") != store.EventRunDispatched {
		t.Fatalf("立即开始的执行应只有派发事件：%v", got)
	}
	evs, _ := e.s.RunEvents(e.ctx, queued.ID)
	if len(evs) != 1 || evs[0].Type != store.EventRunQueued || evs[0].Data["whenBusy"] != "interrupt" || evs[0].Data["requestId"] != "req-1" {
		t.Fatalf("排队执行应有入队事件并带忙时选择与请求编号：%+v", evs)
	}
	var instance *uuid.UUID
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT instance_id FROM lifecycle_events WHERE run_id = $1`, queued.ID).Scan(&instance); err != nil || instance == nil || *instance != *e.s.InstanceID {
		t.Fatalf("事件应记录实例 ID：%v %v", instance, err)
	}

	if err := e.s.SetRunPhase(e.ctx, active.ID, "preparing"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetRunRunning(e.ctx, active.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetRunRunning(e.ctx, active.ID, 1); err != nil { // 重复调用不重复记录
		t.Fatal(err)
	}
	if _, err := e.s.MarkAborting(e.ctx, e.agent, queued.ID); err != nil {
		t.Fatal(err)
	}
	r, _, finished, err := e.s.FinishRun(e.ctx, active.ID, store.RunEnd{Status: "interrupted", EndReason: "user_interrupt"})
	if err != nil || !finished {
		t.Fatalf("结束失败：%v", err)
	}
	if got := strings.Join(e.types(active.ID), ","); got != "run.dispatched,run.phase,run.running,run.abort_requested,run.ended" {
		t.Fatalf("事件序列不对：%s", got)
	}
	if r.StartedAt == nil || r.RunningAt == nil || r.AbortRequestedAt == nil || r.EndedAt == nil ||
		r.RunningAt.Before(*r.StartedAt) || r.AbortRequestedAt.Before(*r.RunningAt) || r.EndedAt.Before(*r.AbortRequestedAt) {
		t.Fatalf("时间点不对：%+v", r)
	}
	evs, _ = e.s.RunEvents(e.ctx, active.ID)
	if evs[3].Data["byRunId"] != queued.ID.String() || evs[4].Data["status"] != "interrupted" || evs[4].Data["endReason"] != "user_interrupt" {
		t.Fatalf("事件数据不对：%+v %+v", evs[3], evs[4])
	}

	started, ok, err := e.s.StartQueuedRun(e.ctx, queued.ID, uuid.Must(uuid.NewV7()))
	if err != nil || !ok {
		t.Fatalf("开始排队执行失败：%v", err)
	}
	if got := strings.Join(e.types(queued.ID), ","); got != "run.queued,run.dispatched" {
		t.Fatalf("出队后应有派发事件：%s", got)
	}
	if _, ok, _ := e.s.StartQueuedRun(e.ctx, queued.ID, uuid.Must(uuid.NewV7())); ok {
		t.Fatal("已开始的执行不应再次派发")
	}
	if got := len(e.types(queued.ID)); got != 2 {
		t.Fatalf("重复出队不应重复记录：%d", got)
	}
	_ = started
}

// 事件插入失败时，状态变化一并回滚。
func TestEventFailureRollsBackState(t *testing.T) {
	e := newRecEnv(t)
	r := e.send(true, "")
	// 让结束事件的插入失败（instance_id 外的列都受约束保护，这里用触发器模拟写入失败）。
	if _, err := e.s.Pool.Exec(e.ctx, `CREATE FUNCTION fail_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'boom'; END $$;
		CREATE TRIGGER fail_event BEFORE INSERT ON lifecycle_events FOR EACH ROW EXECUTE FUNCTION fail_event();`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err == nil {
		t.Fatal("事件写入失败时结束应失败")
	}
	if st, _ := e.s.RunStatus(e.ctx, r.ID); st != "starting" {
		t.Fatalf("事件写入失败时状态应回滚：%s", st)
	}
}

func TestMigrationConstraints(t *testing.T) {
	e := newRecEnv(t)
	bad := []store.UsageRecord{
		{AgentID: e.agent, Kind: "assistant", Input: -1},
		{AgentID: e.agent, Kind: "bogus"},
		{AgentID: e.agent, Kind: "assistant", Provider: "anthropic"}, // 只有提供方没有模型
	}
	for i, u := range bad {
		if err := e.s.AddUsage(e.ctx, u); err == nil {
			t.Errorf("第 %d 条非法用量应被拒绝", i)
		}
	}
	if err := e.s.AddUsage(e.ctx, store.UsageRecord{AgentID: e.agent, Kind: "assistant", Provider: "p", Model: "m", Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}); err != nil {
		t.Fatal(err)
	}
	us, _ := e.s.AgentUsage(e.ctx, e.agent)
	if len(us) != 1 || us[0].Total != 10 {
		t.Fatalf("总数应为四项之和：%+v", us)
	}
	if err := e.s.AddEvent(e.ctx, store.LifecycleEvent{AgentID: e.agent, Type: "Bad Type"}); err == nil {
		t.Error("非法的事件类型应被拒绝")
	}
	var task, node, inbox *string
	var source string
	if err := e.s.AddEvent(e.ctx, store.LifecycleEvent{AgentID: e.agent, Type: store.EventPiStopped, Data: map[string]any{"reason": "idle_timeout"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT task_id::text, workflow_node, inbox_message_id::text, source FROM lifecycle_events WHERE agent_id = $1`, e.agent).
		Scan(&task, &node, &inbox, &source); err != nil {
		t.Fatal(err)
	}
	if task != nil || node != nil || inbox != nil || source != "platform" {
		t.Fatalf("预留字段应为空、来源为平台：%v %v %v %s", task, node, inbox, source)
	}
}

func TestSessionEvents(t *testing.T) {
	e := newRecEnv(t)
	r := e.send(true, "")
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MarkSessionMissing(e.ctx, e.agent, &r.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MarkSessionMissing(e.ctx, e.agent, &r.ID); err != nil { // 已是丢失状态：不重复记录
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "failed", ErrorCode: "session_missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ResetSession(e.ctx, e.agent); err != nil {
		t.Fatal(err)
	}
	evs, _ := e.s.AgentEvents(e.ctx, e.agent)
	var sess []store.LifecycleEvent
	for _, ev := range evs {
		if strings.HasPrefix(ev.Type, "session.") {
			sess = append(sess, ev)
		}
	}
	if len(sess) != 2 || sess[0].Type != store.EventSessionMissing || sess[0].RunID == nil || *sess[0].RunID != r.ID ||
		sess[0].Data["sessionFile"] != "/tmp/s.jsonl" || sess[1].Type != store.EventSessionReset ||
		sess[1].Data["fromEpoch"] != float64(1) || sess[1].Data["toEpoch"] != float64(2) {
		t.Fatalf("会话事件不对：%+v", sess)
	}
}

func TestAnomaliesQuery(t *testing.T) {
	e := newRecEnv(t)
	b, _ := e.s.CreateAgent(e.ctx, store.ProjectScope(e.dc.Project.ID), "B", "anthropic", "claude-x", "")
	finish := func(agent uuid.UUID, end store.RunEnd) store.Run {
		_, r, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: agent, RunID: uuid.Must(uuid.NewV7()), Text: "x", Start: true, InstanceID: uuid.Must(uuid.NewV7())})
		if err != nil {
			t.Fatal(err)
		}
		r, _, _, err = e.s.FinishRun(e.ctx, r.ID, end)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	finish(e.agent, store.RunEnd{Status: "completed"})
	f1 := finish(e.agent, store.RunEnd{Status: "failed", ErrorCode: "workspace_sync_failed", ErrorMessage: "x"})
	i1 := finish(e.agent, store.RunEnd{Status: "interrupted", EndReason: "user_interrupt"})
	s1 := finish(e.agent, store.RunEnd{Status: "interrupted", EndReason: "server_restart"})
	finish(b.ID, store.RunEnd{Status: "failed", ErrorCode: "pi_exited"})
	if err := e.s.SoftDeleteAgent(e.ctx, b.ID); err != nil {
		t.Fatal(err)
	}

	items, total, next, err := e.s.Anomalies(e.ctx, store.ScopeFilter{ProjectID: &e.dc.Project.ID}, nil, nil, 2)
	if err != nil || total != 3 || len(items) != 2 || next == nil {
		t.Fatalf("第一页不对：%d %d %v %v", total, len(items), next, err)
	}
	if items[0].Run.ID != s1.ID || items[1].Run.ID != i1.ID || items[0].AgentName != "A" {
		t.Fatalf("应按结束时间倒序：%v %v", items[0].Run.ID, items[1].Run.ID)
	}
	rest, total2, next2, err := e.s.Anomalies(e.ctx, store.ScopeFilter{ProjectID: &e.dc.Project.ID}, nil, next, 2)
	if err != nil || total2 != 3 || len(rest) != 1 || next2 != nil || rest[0].Run.ID != f1.ID {
		t.Fatalf("第二页不对：%+v %v", rest, err)
	}
	future := time.Now().Add(time.Hour)
	if items, total, _, _ := e.s.Anomalies(e.ctx, store.ScopeFilter{ProjectID: &e.dc.Project.ID}, &future, nil, 10); total != 0 || len(items) != 0 {
		t.Fatalf("开始时间之后没有异常：%d", total)
	}
}

func TestOverviewQueries(t *testing.T) {
	e := newRecEnv(t)
	now := time.Now()
	for i, ev := range []store.LifecycleEvent{
		{AgentID: e.agent, Type: store.EventPiStopped, At: now.Add(-3 * time.Minute), Data: map[string]any{"reason": "config_changed"}},
		{AgentID: e.agent, Type: store.EventPiExited, At: now.Add(-2 * time.Minute)},
		{AgentID: e.agent, Type: store.EventPiStopped, At: now.Add(-time.Minute), Data: map[string]any{"reason": "idle_timeout"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now.Add(-time.Minute), Data: map[string]any{"cli": "gh", "version": "2.100.0", "result": "failed", "error": "摘要不符"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now, Data: map[string]any{"cli": "gh", "version": "2.100.0", "result": "succeeded"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now, Data: map[string]any{"cli": "lark-cli", "version": "1.0.94", "result": "failed", "error": "HTTP 404"}},
	} {
		if err := e.s.AddEvent(e.ctx, ev); err != nil {
			t.Fatalf("第 %d 条：%v", i, err)
		}
	}
	exits, err := e.s.LastProcessExits(e.ctx, []uuid.UUID{e.agent})
	if err != nil || exits[e.agent].Reason != "idle_timeout" {
		t.Fatalf("上次退出应取最近一条：%+v %v", exits, err)
	}
	installs, err := e.s.LatestCLIInstalls(e.ctx)
	if err != nil || len(installs) != 2 {
		t.Fatalf("安装结果不对：%+v %v", installs, err)
	}
	for _, r := range installs {
		if (r.CLI == "gh" && !r.Succeeded) || (r.CLI == "lark-cli" && (r.Succeeded || r.Error != "HTTP 404")) {
			t.Fatalf("应取每个版本最近一次的结果：%+v", installs)
		}
	}
	r := e.send(true, "")
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	last, err := e.s.LastEndedRuns(e.ctx, []uuid.UUID{e.agent})
	if err != nil || last[e.agent].ID != r.ID || last[e.agent].Status != "completed" {
		t.Fatalf("最近一次结束的执行不对：%+v %v", last, err)
	}
}

func TestTaskProcessExitsDoNotOverrideNormalOverview(t *testing.T) {
	st, p, manager, agent, record, instances := taskFixture(t)
	ctx := context.Background()
	_, err := st.CreateTask(ctx, p.ID, manager.ID, store.TaskAction{ActorKey: "observe1", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { return record, instances, nil })
	if err != nil {
		t.Fatal(err)
	}
	second := record
	second.View.Task.ID = uuid.New()
	secondInstances := []store.TaskInstance{{ID: uuid.New(), TaskID: second.View.Task.ID, AgentID: agent.ID, AgentName: agent.Name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}}
	_, err = st.CreateTask(ctx, p.ID, manager.ID, store.TaskAction{ActorKey: "observe2", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { return second, secondInstances, nil })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	normalAt := now.Add(-3 * time.Minute)
	if err = st.AddEvent(ctx, store.LifecycleEvent{AgentID: agent.ID, Type: store.EventPiStopped, At: normalAt, Data: map[string]any{"reason": "idle_timeout"}}); err != nil {
		t.Fatal(err)
	}
	observer := &tasks.Observer{Store: st, Normal: executor.NopObserver{}, Timeout: time.Second}
	observer.Event(instances[0].ID, nil, executor.Event{Type: store.EventPiStopped, At: now.Add(-2 * time.Minute), Data: map[string]any{"reason": "released"}})
	observer.Event(secondInstances[0].ID, nil, executor.Event{Type: store.EventPiExited, At: now.Add(-time.Minute)})
	var count int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE agent_id=$1 AND task_id IS NOT NULL AND type IN ('pi.stopped','pi.exited')`, agent.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("task exit fixtures not recorded", err, count)
	}
	exits, err := st.LastProcessExits(ctx, []uuid.UUID{agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if exits[agent.ID].Reason != "idle_timeout" || !exits[agent.ID].At.Equal(normalAt.Truncate(time.Microsecond)) {
		t.Fatal("task exits polluted normal overview", exits[agent.ID])
	}
}
