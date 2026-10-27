package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

func rejectEvent(t *testing.T, s *Service) func(string) {
	t.Helper()
	ctx := context.Background()
	_, e := s.Store.Pool.Exec(ctx, `CREATE TABLE reject_task_event(kind text);
 CREATE FUNCTION reject_task_observation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF EXISTS(SELECT 1 FROM reject_task_event WHERE kind=NEW.type) THEN RAISE EXCEPTION 'injected observation failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_task_observation BEFORE INSERT ON lifecycle_events FOR EACH ROW EXECUTE FUNCTION reject_task_observation()`)
	if e != nil {
		t.Fatal(e)
	}
	return func(kind string) {
		t.Helper()
		if _, e := s.Store.Pool.Exec(ctx, `DELETE FROM reject_task_event`); e != nil {
			t.Fatal(e)
		}
		if kind != "" {
			if _, e := s.Store.Pool.Exec(ctx, `INSERT INTO reject_task_event VALUES($1)`, kind); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func TestTaskLifecycleFailureRollsBackState(t *testing.T) {
	ctx := context.Background()
	s, id, p := fixture(t, false)
	reject := rejectEvent(t, s)
	reject(store.EventRunQueued)
	_, e := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		_, e := s.Store.EnqueueTaskTurn(ctx, m, "work", "task_node", 1, nil)
		return nil, e
	})
	if e == nil {
		t.Fatal("queue event rejection accepted")
	}
	var count int
	s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1`, id).Scan(&count)
	r := current(t, s, id)
	if count != 0 || len(r.Current().RunIDs) != 0 {
		t.Fatal("queue state committed without event")
	}
	reject("")
	var queued store.TaskTurn
	_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		var e error
		queued, e = s.Store.EnqueueTaskTurn(ctx, m, "work", "task_node", 1, nil)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	reject(store.EventRunDispatched)
	if _, _, e = s.Store.ClaimTaskTurn(ctx, id, p.RuntimeID); e == nil {
		t.Fatal("dispatch rejection accepted")
	}
	assertState := func(want string) {
		t.Helper()
		v, e := s.Store.TaskTurn(ctx, queued.ID)
		if e != nil || v.Status != want {
			t.Fatalf("state=%s want=%s error=%v", v.Status, want, e)
		}
	}
	assertState("queued")
	reject("")
	v, ok, e := s.Store.ClaimTaskTurn(ctx, id, p.RuntimeID)
	if e != nil || !ok {
		t.Fatal(e)
	}
	reject(store.EventRunRunning)
	if e = s.Store.MarkTaskRunning(ctx, v, 7); e == nil {
		t.Fatal("running rejection accepted")
	}
	assertState("starting")
	reject(store.EventRunEnded)
	if _, e = s.Store.UpdateTaskTurn(ctx, id, v.ID, "completed", "done", nil, nil); e == nil {
		t.Fatal("end rejection accepted")
	}
	assertState("starting")
	if _, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) { _, _, e := s.Store.RecoverTaskTurns(ctx, m); return nil, e }); e == nil {
		t.Fatal("recovery rejection accepted")
	}
	assertState("starting")
	reject(store.EventRunAbortRequested)
	if _, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) { return nil, s.Store.InterruptTaskTurns(ctx, m, v.NodeRunID) }); e == nil {
		t.Fatal("abort rejection accepted")
	}
	assertState("starting")
	reject("")
	if e = s.Store.MarkTaskRunning(ctx, v, 7); e != nil {
		t.Fatal(e)
	}
	if e = s.Store.MarkTaskRunning(ctx, v, 7); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if _, e = s.Store.UpdateTaskTurn(ctx, id, v.ID, "completed", "done", nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{store.EventRunQueued, store.EventRunDispatched, store.EventRunRunning, store.EventRunEnded} {
		if e = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE task_turn_id=$1 AND type=$2`, v.ID, kind).Scan(&count); e != nil || count != 1 {
			t.Fatalf("event %s count=%d err=%v", kind, count, e)
		}
	}
}
func TestTaskObservationFailureWarnsWithScope(t *testing.T) {
	s, id, p := fixture(t, false)
	v := turn(t, s, id)
	reject := rejectEvent(t, s)
	reject("tool.changed")
	var buf bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	sink := &turnSink{service: s, ctx: context.Background(), turn: v}
	sink.Tool("call", "bash", "started")
	for _, value := range []string{"WARN", p.ID.String(), p.RuntimeID.String(), id.String(), v.ID.String(), v.NodeRunID.String()} {
		if !strings.Contains(buf.String(), value) {
			t.Fatalf("warning lacks %s: %s", value, buf.String())
		}
	}
	got, e := s.Store.TaskTurn(context.Background(), v.ID)
	if e != nil || got.Status != "starting" {
		t.Fatal("noncritical observation changed execution", e)
	}
}
func TestTaskUnboundInboxLifecycleKeepsNullNode(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	instanceID := uuid.New()
	s.Store.InstanceID = &instanceID
	r := current(t, s, id)
	agent, e := s.Store.CreateAgent(ctx, store.ProjectScope(r.View.Task.ProjectID), "另一实例", "example", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	runtime := uuid.New()
	if _, e = s.Store.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'),true)`, runtime, id, agent.ID, agent.Name, []byte("fixture")); e != nil {
		t.Fatal(e)
	}
	message := uuid.New()
	if e = s.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: agent.ID, RuntimeID: runtime}, Text: "协作", WhenBusy: "queue"}); e != nil {
		t.Fatal(e)
	}
	v, ok, e := s.Store.ClaimTaskInboxTurn(ctx, id, runtime)
	if e != nil || !ok {
		t.Fatal(e, ok)
	}
	if v.NodeRunID != uuid.Nil {
		t.Fatal("unbound round acquired node")
	}
	if e = s.Store.MarkTaskRunning(ctx, v, 1); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.UpdateTaskTurn(ctx, id, v.ID, "completed", "reply", nil, nil); e != nil {
		t.Fatal(e)
	}
	var count, nonNull int
	e = s.Store.Pool.QueryRow(ctx, `SELECT count(*),count(workflow_node) FROM lifecycle_events WHERE task_turn_id=$1 AND agent_id=$2 AND task_id=$3 AND inbox_message_id=$4 AND instance_id=$5`, v.ID, agent.ID, id, message, s.Store.InstanceID).Scan(&count, &nonNull)
	if e != nil || count != 4 || nonNull != 0 {
		t.Fatal("wrong unbound event association", count, nonNull, e)
	}
}

func TestTaskReplyLogsAndUsageShareExactScope(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	v := turn(t, s, id)
	var buf bytes.Buffer
	s.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	s.Options.RecordTimeout = time.Second
	observer := &Observer{Store: s.Store, Normal: executor.NopObserver{}, Timeout: time.Second, Log: s.Log}
	s.Executor = &controlledExecutor{execute: func(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
		observer.Usage(req.AgentID, &req.RunID, executor.Usage{Kind: "assistant", Provider: "actual-provider", Model: "actual-model", Input: 2, Output: 3, CacheRead: 5, CacheWrite: 7})
		observer.Usage(req.AgentID, &req.RunID, executor.Usage{Kind: "tool", Input: 11, Output: 13, CacheRead: 17, CacheWrite: 19})
		return executor.Result{Outcome: "completed"}, nil
	}}
	if _, e := s.executeTurn(ctx, v, &turnSink{service: s, ctx: ctx, turn: v}); e != nil {
		t.Fatal(e)
	}
	var count, total int
	if e := s.Store.Pool.QueryRow(ctx, `SELECT count(*),sum(total) FROM usage_records WHERE agent_id=$1 AND task_id=$2 AND task_turn_id=$3 AND run_id IS NULL`, p.ID, id, v.ID).Scan(&count, &total); e != nil || count != 2 || total != 77 {
		t.Fatal("usage duplicated or misattributed", count, total, e)
	}
	rows := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(rows) != 2 {
		t.Fatal("missing reply logs", buf.String())
	}
	for i, line := range rows {
		var row map[string]any
		if e := json.Unmarshal([]byte(line), &row); e != nil {
			t.Fatal(e)
		}
		for key, want := range map[string]string{"agentId": p.ID.String(), "runtimeId": p.RuntimeID.String(), "taskId": id.String(), "nodeRunId": v.NodeRunID.String(), "runId": v.ID.String()} {
			if row[key] != want {
				t.Fatalf("%s mismatch: %v", key, row)
			}
		}
		if i == 0 {
			if row["whenBusy"] != "queue" || row["queuedMs"] == nil {
				t.Fatal("missing dispatch timing", row)
			}
		}
		if i == 1 {
			if row["status"] != "completed" || row["durationMs"] == nil || row["endReason"] != "" || row["tokens"] != float64(77) {
				t.Fatal("missing end timing/reason/tokens", row)
			}
		}
		if i == 1 && row["total"] != float64(77) {
			t.Fatal("wrong log usage", row)
		}
	}
}

func TestBlockedTaskInboxReplyDoesNotResumeWorkflow(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	if e := s.blockTask(ctx, id, "需要重试"); e != nil {
		t.Fatal(e)
	}
	before := current(t, s, id)
	message := uuid.New()
	if e := s.DeliverInbox(ctx, store.NewInboxMessage{ID: message, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, Text: "解释现状", WhenBusy: "queue"}); e != nil {
		t.Fatal(e)
	}
	calls := 0
	s.Executor = &controlledExecutor{execute: func(ctx context.Context, r executor.Request, sink executor.Sink) (executor.Result, error) {
		calls++
		sink.Running(1)
		sink.TextDelta("说明")
		return executor.Result{Outcome: "completed", Text: "说明"}, nil
	}}
	if e := s.driveInbox(ctx, p.RuntimeID); e != nil {
		t.Fatal(e)
	}
	after := current(t, s, id)
	if calls != 1 || after.View.Task.Status != "blocked" || after.Current().ID != before.Current().ID || after.Current().Attempt != before.Current().Attempt {
		t.Fatal("Inbox resumed blocked workflow")
	}
	messages, e := s.Store.TaskNodeMessages(ctx, id, before.Current().ID, "", 20)
	if e != nil || len(messages.Messages) != 2 || messages.Messages[1].Text != "说明" {
		t.Fatal("blocked Inbox conversation missing", e, messages)
	}
}

func TestTaskNoncurrentInboxRunsWhileCurrentBusy(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	r := current(t, s, id)
	agent, e := s.Store.CreateAgent(ctx, store.ProjectScope(r.View.Task.ProjectID), "协作者", "example", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	runtime := uuid.New()
	if _, e = s.Store.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'),true)`, runtime, id, agent.ID, agent.Name, []byte("fixture")); e != nil {
		t.Fatal(e)
	}
	_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		_, e := s.Store.EnqueueTaskTurn(ctx, m, "node work", "task_node", 1, nil)
		return nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	mainStarted, sideDone := make(chan struct{}), make(chan struct{})
	s.Executor = &controlledExecutor{execute: func(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
		sink.Running(1)
		if req.AgentID == p.RuntimeID {
			close(mainStarted)
			<-ctx.Done()
			return executor.Result{Outcome: "interrupted"}, nil
		}
		sink.TextDelta("协作回复")
		close(sideDone)
		return executor.Result{Outcome: "completed", Text: "协作回复"}, nil
	}}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer s.Stop(ctx)
	select {
	case <-mainStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("main did not start")
	}
	if e = s.DeliverInbox(ctx, store.NewInboxMessage{ID: uuid.New(), From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: agent.ID, RuntimeID: runtime}, Text: "帮忙", WhenBusy: "queue"}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-sideDone:
	case <-time.After(3 * time.Second):
		t.Fatal("noncurrent Inbox waited for workflow")
	}
	eventually(t, func() bool {
		var ended bool
		_ = s.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status='completed' AND node_run_id IS NULL)`, runtime).Scan(&ended)
		return ended
	})
	r = current(t, s, id)
	if r.View.Task.Status != "running" || r.Current().RuntimeID != p.RuntimeID || len(r.View.Nodes) != 1 {
		t.Fatal("side reply advanced workflow")
	}
	page, e := s.Store.TaskNodeMessages(ctx, id, r.Current().ID, "", 20)
	if e != nil {
		t.Fatal(e)
	}
	for _, message := range page.Messages {
		if message.Text == "协作回复" {
			t.Fatal("side reply polluted node conversation")
		}
	}
	if _, e = s.Cancel(ctx, auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()}); e != nil {
		t.Fatal(e)
	}
}

func TestRestartOfUnboundInboxDoesNotBlockWaitingNode(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	r := current(t, s, id)
	agent, e := s.Store.CreateAgent(ctx, store.ProjectScope(r.View.Task.ProjectID), "恢复协作者", "example", "model", "")
	if e != nil {
		t.Fatal(e)
	}
	runtime := uuid.New()
	if _, e = s.Store.Pool.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'),true)`, runtime, id, agent.ID, agent.Name, []byte("fixture")); e != nil {
		t.Fatal(e)
	}
	_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "waiting"
		m.Record.Current().Status = "waiting"
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeliverInbox(ctx, store.NewInboxMessage{ID: uuid.New(), From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: agent.ID, RuntimeID: runtime}, Text: "协作", WhenBusy: "queue"}); e != nil {
		t.Fatal(e)
	}
	v, ok, e := s.Store.ClaimTaskInboxTurn(ctx, id, runtime)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer s.Stop(ctx)
	got := current(t, s, id)
	if got.View.Task.Status != "waiting" || got.Current().Status != "waiting" {
		t.Fatal("side round recovery blocked unrelated node")
	}
	v, e = s.Store.TaskTurn(ctx, v.ID)
	if e != nil || v.Status != "interrupted" || v.ErrorCode == nil || *v.ErrorCode != "server_restarted" {
		t.Fatal("side round replayed or lost recovery record", e)
	}
}
