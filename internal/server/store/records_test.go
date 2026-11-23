package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
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
	s := sqlitetest.NewStore(t)
	instance := uuid.Must(uuid.NewV7())
	s.InstanceID = &instance
	ctx := context.Background()
	dc, err := s.EnsureDefaults(ctx, "Default organization", "Default project")
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

// A run's status change and its event are written in the same transaction, and the timestamps are written to runs.
func TestRunLifecycleEventsAndTimes(t *testing.T) {
	e := newRecEnv(t)
	active := e.send(true, "")
	queued := e.send(false, "interrupt")

	if got := e.types(active.ID); strings.Join(got, ",") != store.EventRunDispatched {
		t.Fatalf("a run started immediately should only have a dispatch event: %v", got)
	}
	evs, _ := e.s.RunEvents(e.ctx, queued.ID)
	if len(evs) != 1 || evs[0].Type != store.EventRunQueued || evs[0].Data["whenBusy"] != "interrupt" || evs[0].Data["requestId"] != "req-1" {
		t.Fatalf("a queued run should have a queued event with the busy choice and request ID: %+v", evs)
	}
	var instance *uuid.UUID
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT instance_id FROM lifecycle_events WHERE run_id = $1`, queued.ID).Scan(&instance); err != nil || instance == nil || *instance != *e.s.InstanceID {
		t.Fatalf("events should record the instance ID: %v %v", instance, err)
	}

	if err := e.s.SetRunPhase(e.ctx, active.ID, "preparing", "pi"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetRunRunning(e.ctx, active.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetRunRunning(e.ctx, active.ID, 1); err != nil { // repeated calls do not record again
		t.Fatal(err)
	}
	if _, err := e.s.MarkAborting(e.ctx, e.agent, queued.ID); err != nil {
		t.Fatal(err)
	}
	r, _, finished, err := e.s.FinishRun(e.ctx, active.ID, store.RunEnd{Status: "interrupted", EndReason: "user_interrupt"})
	if err != nil || !finished {
		t.Fatalf("finish failed: %v", err)
	}
	if got := strings.Join(e.types(active.ID), ","); got != "run.dispatched,run.phase,run.running,run.abort_requested,run.ended" {
		t.Fatalf("wrong event sequence: %s", got)
	}
	if r.StartedAt == nil || r.RunningAt == nil || r.AbortRequestedAt == nil || r.EndedAt == nil ||
		r.RunningAt.Before(*r.StartedAt) || r.AbortRequestedAt.Before(*r.RunningAt) || r.EndedAt.Before(*r.AbortRequestedAt) {
		t.Fatalf("wrong timestamps: %+v", r)
	}
	evs, _ = e.s.RunEvents(e.ctx, active.ID)
	if evs[3].Data["byRunId"] != queued.ID.String() || evs[4].Data["status"] != "interrupted" || evs[4].Data["endReason"] != "user_interrupt" {
		t.Fatalf("wrong event data: %+v %+v", evs[3], evs[4])
	}

	started, ok, err := e.s.StartQueuedRun(e.ctx, queued.ID, uuid.Must(uuid.NewV7()))
	if err != nil || !ok {
		t.Fatalf("starting the queued run failed: %v", err)
	}
	if got := strings.Join(e.types(queued.ID), ","); got != "run.queued,run.dispatched" {
		t.Fatalf("a dispatch event should follow dequeuing: %s", got)
	}
	if _, ok, _ := e.s.StartQueuedRun(e.ctx, queued.ID, uuid.Must(uuid.NewV7())); ok {
		t.Fatal("a started run should not be dispatched again")
	}
	if got := len(e.types(queued.ID)); got != 2 {
		t.Fatalf("dequeuing again should not record again: %d", got)
	}
	_ = started
}

// When inserting the event fails, the status change is rolled back with it.
func TestEventFailureRollsBackState(t *testing.T) {
	e := newRecEnv(t)
	r := e.send(true, "")
	// Make inserting the end event fail (every column except instance_id is protected by constraints, so a trigger is
	// used here to simulate a write failure).
	if _, err := e.s.Pool.Exec(e.ctx, `CREATE TRIGGER ev.fail_event BEFORE INSERT ON lifecycle_events BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err == nil {
		t.Fatal("finishing should fail when writing the event fails")
	}
	if st, _ := e.s.RunStatus(e.ctx, r.ID); st != "starting" {
		t.Fatalf("the status should roll back when writing the event fails: %s", st)
	}
}

func TestMigrationConstraints(t *testing.T) {
	e := newRecEnv(t)
	bad := []store.UsageRecord{
		{AgentID: e.agent, Kind: "assistant", Input: -1},
		{AgentID: e.agent, Kind: "bogus"},
		{AgentID: e.agent, Kind: "assistant", Provider: "anthropic"}, // a provider only, no model
	}
	for i, u := range bad {
		if err := e.s.AddUsage(e.ctx, u); err == nil {
			t.Errorf("invalid usage record %d should be rejected", i)
		}
	}
	if err := e.s.AddUsage(e.ctx, store.UsageRecord{AgentID: e.agent, Kind: "assistant", Provider: "p", Model: "m", Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4}); err != nil {
		t.Fatal(err)
	}
	us, _ := e.s.AgentUsage(e.ctx, e.agent)
	if len(us) != 1 || us[0].Total != 10 {
		t.Fatalf("the total should be the sum of the four parts: %+v", us)
	}
	if err := e.s.AddEvent(e.ctx, store.LifecycleEvent{AgentID: e.agent, Type: "Bad Type"}); err == nil {
		t.Error("an invalid event type should be rejected")
	}
	var task, node, inbox *string
	var source string
	if err := e.s.AddEvent(e.ctx, store.LifecycleEvent{AgentID: e.agent, Type: store.EventPiStopped, Data: map[string]any{"reason": "idle_timeout"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Pool.QueryRow(e.ctx, `SELECT task_id, workflow_node, inbox_message_id, source FROM ev.lifecycle_events WHERE agent_id = $1`, e.agent).
		Scan(&task, &node, &inbox, &source); err != nil {
		t.Fatal(err)
	}
	if task != nil || node != nil || inbox != nil || source != "platform" {
		t.Fatalf("reserved fields should be empty and the source platform: %v %v %v %s", task, node, inbox, source)
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
	if err := e.s.MarkSessionMissing(e.ctx, e.agent, &r.ID); err != nil { // already missing: not recorded again
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
		t.Fatalf("wrong session events: %+v", sess)
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
		t.Fatalf("wrong first page: %d %d %v %v", total, len(items), next, err)
	}
	if items[0].Run.ID != s1.ID || items[1].Run.ID != i1.ID || items[0].AgentName != "A" {
		t.Fatalf("should be ordered by end time, newest first: %v %v", items[0].Run.ID, items[1].Run.ID)
	}
	rest, total2, next2, err := e.s.Anomalies(e.ctx, store.ScopeFilter{ProjectID: &e.dc.Project.ID}, nil, next, 2)
	if err != nil || total2 != 3 || len(rest) != 1 || next2 != nil || rest[0].Run.ID != f1.ID {
		t.Fatalf("wrong second page: %+v %v", rest, err)
	}
	future := time.Now().Add(time.Hour)
	if items, total, _, _ := e.s.Anomalies(e.ctx, store.ScopeFilter{ProjectID: &e.dc.Project.ID}, &future, nil, 10); total != 0 || len(items) != 0 {
		t.Fatalf("there should be no anomalies after the start time: %d", total)
	}
}

func TestOverviewQueries(t *testing.T) {
	e := newRecEnv(t)
	now := time.Now()
	for i, ev := range []store.LifecycleEvent{
		{AgentID: e.agent, Type: store.EventPiStopped, At: now.Add(-3 * time.Minute), Data: map[string]any{"reason": "config_changed"}},
		{AgentID: e.agent, Type: store.EventPiExited, At: now.Add(-2 * time.Minute)},
		{AgentID: e.agent, Type: store.EventPiStopped, At: now.Add(-time.Minute), Data: map[string]any{"reason": "idle_timeout"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now.Add(-time.Minute), Data: map[string]any{"cli": "gh", "version": "2.100.0", "result": "failed", "error": "checksum mismatch"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now, Data: map[string]any{"cli": "gh", "version": "2.100.0", "result": "succeeded"}},
		{AgentID: e.agent, Type: store.EventCLIInstall, At: now, Data: map[string]any{"cli": "lark-cli", "version": "1.0.94", "result": "failed", "error": "HTTP 404"}},
	} {
		if err := e.s.AddEvent(e.ctx, ev); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	exits, err := e.s.LastProcessExits(e.ctx, []uuid.UUID{e.agent})
	if err != nil || exits[e.agent].Reason != "idle_timeout" {
		t.Fatalf("the last exit should be the latest one: %+v %v", exits, err)
	}
	installs, err := e.s.LatestCLIInstalls(e.ctx)
	if err != nil || len(installs) != 2 {
		t.Fatalf("wrong install results: %+v %v", installs, err)
	}
	for _, r := range installs {
		if (r.CLI == "gh" && !r.Succeeded) || (r.CLI == "lark-cli" && (r.Succeeded || r.Error != "HTTP 404")) {
			t.Fatalf("should take the latest result of each version: %+v", installs)
		}
	}
	r := e.send(true, "")
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	last, err := e.s.LastEndedRuns(e.ctx, []uuid.UUID{e.agent})
	if err != nil || last[e.agent].ID != r.ID || last[e.agent].Status != "completed" {
		t.Fatalf("wrong latest ended run: %+v %v", last, err)
	}
}

func TestTaskProcessExitsDoNotOverrideNormalOverview(t *testing.T) {
	st, p, manager, agent, record, instances := taskFixture(t)
	ctx := context.Background()
	_, err := st.CreateTask(ctx, p.ID, manager.ID, store.TaskAction{ActorKey: "observe1", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) { return record, instances, nil })
	if err != nil {
		t.Fatal(err)
	}
	second := record
	second.View.Task.ID = uuid.New()
	secondInstances := []store.TaskInstance{{ID: uuid.New(), TaskID: second.View.Task.ID, AgentID: agent.ID, AgentName: agent.Name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}}
	_, err = st.CreateTask(ctx, p.ID, manager.ID, store.TaskAction{ActorKey: "observe2", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) { return second, secondInstances, nil })
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

// Changing the model provider switches the runtime (between Pi and Claude Code): rejected while a reply is in progress;
// otherwise the session is reset (sequence number plus one, a reset event recorded with reason runtime_changed), while
// changing the model within the same runtime leaves the session alone.
func TestRuntimeSwitchResetsSession(t *testing.T) {
	e := newRecEnv(t)
	provider, model := func(p string) *string { return &p }, func(m string) *string { return &m }
	r := e.send(true, "")
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("claude-code"), ModelID: model("sonnet")}); !apperr.Is(err, apperr.CodeAgentNotIdle) {
		t.Fatalf("switching the runtime should be rejected while a reply is in progress: %v", err)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("openai"), ModelID: model("gpt-x")}); err != nil {
		t.Fatal(err)
	}
	if sess, _ := e.s.GetAgentSession(e.ctx, e.agent); sess.State != "active" || sess.Epoch != 1 || sess.SessionFile != "/tmp/pi-session.jsonl" {
		t.Fatalf("changing the model within the same runtime should leave the session alone: %+v", sess)
	}
	a, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("claude-code"), ModelID: model("sonnet")})
	if err != nil || a.ModelProvider != "claude-code" {
		t.Fatalf("switch to Claude Code: %+v %v", a, err)
	}
	if sess, _ := e.s.GetAgentSession(e.ctx, e.agent); sess.State != "none" || sess.Epoch != 2 || sess.SessionFile != "" {
		t.Fatalf("switching the runtime should reset the session: %+v", sess)
	}
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("claude-code"), ModelID: model("opus")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("anthropic"), ModelID: model("claude-x")}); err != nil {
		t.Fatal(err)
	}
	evs, _ := e.s.AgentEvents(e.ctx, e.agent)
	var resets []store.LifecycleEvent
	for _, ev := range evs {
		if ev.Type == store.EventSessionReset {
			resets = append(resets, ev)
		}
	}
	if len(resets) != 2 || resets[0].Data["reason"] != "runtime_changed" || resets[0].Data["fromRuntime"] != "pi" || resets[0].Data["toRuntime"] != "claude-code" ||
		resets[0].Data["toEpoch"] != float64(2) || resets[1].Data["fromRuntime"] != "claude-code" || resets[1].Data["toEpoch"] != float64(3) {
		t.Fatalf("reset events: %+v", resets)
	}
}

// The runtime can also be switched while there are queued (not yet started) messages: they are later handled by the
// new runtime, and the session is reset as usual. Without a session row the switch is saved as usual.
func TestRuntimeSwitchWithQueuedMessages(t *testing.T) {
	e := newRecEnv(t)
	provider, model := func(p string) *string { return &p }, func(m string) *string { return &m }
	r := e.send(true, "")
	queued := e.send(false, "")
	if queued.Status != "queued" {
		t.Fatalf("the second message should be queued: %+v", queued.Status)
	}
	if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordSessionFile(e.ctx, e.agent, "/tmp/pi-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("claude-code"), ModelID: model("sonnet")}); err != nil {
		t.Fatalf("switch with queued messages: %v", err)
	}
	if sess, _ := e.s.GetAgentSession(e.ctx, e.agent); sess.State != "none" || sess.Epoch != 2 {
		t.Fatalf("the session should be reset: %+v", sess)
	}
	if _, err := e.s.Pool.Exec(e.ctx, `DELETE FROM agent_sessions WHERE agent_id = $1`, e.agent); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.UpdateAgent(e.ctx, e.agent, store.AgentPatch{ModelProvider: provider("anthropic"), ModelID: model("claude-x")}); err != nil {
		t.Fatalf("without a session row the switch should be saved as usual: %v", err)
	}
}
