package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
	"testing"
	"time"
)

func TestSleepNormalTransactionsAndTimerReplacement(t *testing.T) {
	e := newRecEnv(t)
	ctx := e.ctx
	r := e.send(true, "")
	if err := e.s.SetRunRunning(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: e.agent}
	in := sleepmodel.Input{ActionID: uuid.New(), RunID: r.ID, Seconds: 120}
	receipt, err := e.s.RequestSleep(ctx, p, in, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Sleep.State != "requested" || receipt.Sleep.WakeAt != nil || !receipt.EndTurn {
		t.Fatal(receipt)
	}
	if _, _, _, err = e.s.FinishRun(ctx, r.ID, store.RunEnd{Status: "completed"}); err == nil {
		t.Fatal("missing session activated sleep")
	}
	if active, err := e.s.ActiveRun(ctx, e.agent); err != nil || active == nil {
		t.Fatal("completion and sleep did not rollback", err)
	}
	if err = e.s.RecordSessionFile(ctx, e.agent, "offline-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = e.s.FinishRun(ctx, r.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	state, err := e.s.SleepState(ctx, p)
	if err != nil || state.Sleep == nil || state.Sleep.State != "sleeping" || state.CurrentRunID != nil {
		t.Fatal(state, err)
	}
	wake := *state.Sleep.WakeAt
	again, err := e.s.RequestSleep(ctx, p, in, 1000)
	a, _ := json.Marshal(again)
	b, _ := json.Marshal(receipt)
	if err != nil || string(a) != string(b) {
		t.Fatal("receipt changed", err)
	}
	in.Seconds++
	if _, err = e.s.RequestSleep(ctx, p, in, 1000); err == nil {
		t.Fatal("changed replay accepted")
	}
	// Due time is persisted. Reconciliation commits one stable queued timer.
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = e.s.ReconcileSleeps(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	queued, err := e.s.QueuedRuns(ctx, e.agent)
	if err != nil || len(queued) != 1 || queued[0].Origin != "sleep_wake" {
		t.Fatal(queued, err)
	}
	timer := queued[0].ID
	msg, work, err := e.s.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: uuid.New(), Text: "real work", WhenBusy: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Text != "real work" || work.Origin != "console" || work.WakeSleepID == nil || *work.WakeSleepID != receipt.Sleep.ID {
		t.Fatal(work)
	}
	if _, started, err := e.s.StartQueuedRun(ctx, timer, uuid.New()); err != nil || started {
		t.Fatal("replaced timer claimed", err)
	}
	queued, err = e.s.QueuedRuns(ctx, e.agent)
	if err != nil || len(queued) != 1 || queued[0].ID != work.ID {
		t.Fatal(queued, err)
	}
	if !wake.After(time.Now()) {
		t.Fatal("entered timer wrong")
	}
}
func TestSleepRequestedFailureAndInvalidRequests(t *testing.T) {
	e := newRecEnv(t)
	ctx := context.Background()
	r := e.send(true, "")
	if err := e.s.SetRunRunning(ctx, r.ID, 1); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{Kind: auth.KindAgent, ID: e.agent}
	in := sleepmodel.Input{ActionID: uuid.New(), RunID: r.ID, Seconds: 1}
	if _, err := e.s.RequestSleep(ctx, p, in, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(ctx, r.ID, store.RunEnd{Status: "interrupted", EndReason: "server_restart"}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	state, err := e.s.SleepState(ctx, p)
	if err != nil || state.Sleep != nil {
		t.Fatal(state, err)
	}
	in.ActionID = uuid.New()
	if _, err = e.s.RequestSleep(ctx, p, in, 100); err == nil {
		t.Fatal("late source accepted")
	}
	var count int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE origin='sleep_wake'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestSleepTaskModesAtomicFinishAndCommittedVersion(t *testing.T) {
	for _, mode := range []string{"task_node", "task_conversation"} {
		t.Run(mode, func(t *testing.T) {
			s, project, manager, agent, r, instances := taskFixture(t)
			ctx := context.Background()
			inst := instances[0]
			node := taskmodel.NodeRun{ID: uuid.New(), TaskID: r.View.Task.ID, RuntimeID: inst.ID, AgentID: agent.ID, StepID: "a", Status: "running", RunIDs: []uuid.UUID{}, Inputs: map[string]any{}}
			r.View.Nodes = []taskmodel.NodeRun{node}
			r.View.Task.CurrentNodeRunID = &node.ID
			r.View.Task.Status = "preparing"
			if mode == "task_conversation" {
				r.View.Nodes[0].Status = "blocked"
			}
			_, err := s.CreateTask(ctx, project.ID, manager.ID, store.TaskAction{ActorKey: "fixture", ID: uuid.New(), Operation: "create", Body: []byte("fixture")}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { return r, instances, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='active',session_file='offline-session.jsonl' WHERE id=$1`, inst.ID); err != nil {
				t.Fatal(err)
			}
			var turn store.TaskTurn
			_, err = s.MutateTask(ctx, r.View.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
				var err error
				m.Record.View.Task.Status = "running"
				if mode == "task_conversation" {
					m.Record.View.Task.Status = "blocked"
				}
				if mode == "task_node" {
					turn, err = s.EnqueueTaskTurn(ctx, m, "node", "task_node", 1, nil)
				} else {
					turn, err = enqueueBackedInboxTurn(ctx, s, m, inst, "ordinary", 1)
				}
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			var ok bool
			if mode == "task_node" {
				turn, ok, err = s.ClaimTaskTurn(ctx, r.View.Task.ID, inst.ID)
			} else {
				turn, ok, err = s.ClaimTaskInboxTurn(ctx, r.View.Task.ID, inst.ID)
			}
			if err != nil || !ok {
				t.Fatal("claim", err)
			}
			if mode == "task_conversation" && turn.NodeRunID != node.ID {
				t.Fatal("fixture did not reproduce blocked Inbox node id")
			}
			if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
				t.Fatal(err)
			}
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: r.View.Task.ID, RuntimeID: inst.ID}
			receipt, err := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 120}, 100)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "task_conversation" && receipt.Sleep.NodeRunID != nil {
				t.Fatal("blocked Inbox became node sleep")
			}
			finish := func() error {
				_, e := s.MutateTask(ctx, r.View.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
					if _, e := s.UpdateTaskTurnTx(ctx, m, turn.ID, "completed", "done", nil, nil); e != nil {
						return nil, e
					}
					waiting, e := s.FinishSleepTx(ctx, m.Tx, m, turn.ID, "completed")
					if e != nil {
						return nil, e
					}
					if !waiting {
						return nil, errors.New("sleep not waiting")
					}
					if mode == "task_node" {
						m.Record.Current().Status = "waiting"
						m.Record.View.Task.Status = "waiting"
					}
					return nil, nil
				})
				return e
			}
			if _, err = s.Pool.Exec(ctx, `CREATE FUNCTION reject_sleep_save() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'offline injected'; END $$; CREATE TRIGGER reject_sleep_save BEFORE UPDATE ON task_records FOR EACH ROW EXECUTE FUNCTION reject_sleep_save()`); err != nil {
				t.Fatal(err)
			}
			if err = finish(); err == nil {
				t.Fatal("failure did not rollback")
			}
			current, err := s.TaskTurn(ctx, turn.ID)
			if err != nil || current.Status != "running" {
				t.Fatal("turn escaped atomic rollback", err)
			}
			state, err := s.SleepState(ctx, p)
			if err != nil || state.Sleep.State != "requested" {
				t.Fatal("sleep escaped atomic rollback", err)
			}
			if _, err = s.Pool.Exec(ctx, `DROP TRIGGER reject_sleep_save ON task_records;DROP FUNCTION reject_sleep_save()`); err != nil {
				t.Fatal(err)
			}
			if err = finish(); err != nil {
				t.Fatal(err)
			}
			record, err := s.GetTask(ctx, r.View.Task.ID)
			if err != nil || len(record.View.SleepingInstances) != 1 {
				t.Fatal("projection", err)
			}
			if _, err = s.DeleteProject(ctx, project.ID); !apperr.Is(err, apperr.CodeTaskNotIdle) {
				t.Fatal("sleeping unfinished task bypassed project deletion guard", err)
			}
			preserved, err := s.SleepState(ctx, p)
			if err != nil || preserved.Sleep == nil || preserved.Sleep.ID != receipt.Sleep.ID || preserved.Sleep.State != "sleeping" {
				t.Fatal("refused project deletion changed effective sleep", err)
			}
			if mode == "task_node" && record.Current().Sleep == nil {
				t.Fatal("node sleep absent")
			}
			if mode == "task_conversation" && (record.Current().Sleep != nil || record.Current().Status != "blocked") {
				t.Fatal("conversation altered workflow")
			}
			var eventVersion int64
			if err = s.Pool.QueryRow(ctx, `SELECT (data->>'taskVersion')::bigint FROM task_events WHERE task_id=$1 AND type='sleep.updated' ORDER BY id DESC LIMIT 1`, r.View.Task.ID).Scan(&eventVersion); err != nil || eventVersion != record.View.Task.Version {
				t.Fatal("event used pre-save version", eventVersion, record.View.Task.Version, err)
			}
			if _, err = s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.ReconcileSleeps(ctx, 100); err != nil {
				t.Fatal(err)
			}
			if mode == "task_node" {
				turn, ok, err = s.ClaimTaskTurn(ctx, r.View.Task.ID, inst.ID)
			} else {
				turn, ok, err = s.ClaimTaskInboxTurn(ctx, r.View.Task.ID, inst.ID)
			}
			if err != nil || !ok || turn.Origin != "sleep_wake" {
				t.Fatal("timer driver", turn, err)
			}
			if mode == "task_conversation" && turn.NodeRunID != uuid.Nil {
				t.Fatal("ordinary timer gained node")
			}
			if mode == "task_node" {
				if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
					t.Fatal(err)
				}
				service := tasks.New(s, nil, nil, tasks.Options{}, nil)
				if _, err = service.Fail(ctx, p, r.View.Task.ID, node.ID, taskmodel.FailInput{ActionID: uuid.New(), Reason: "explicit failure before source settled"}); err != nil {
					t.Fatal(err)
				}
				if _, err = s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 1}, 100); !apperr.Is(err, apperr.CodeSleepConflict) {
					t.Fatal("node driver became ordinary conversation after Fail", err)
				}
				var count int
				if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_sleeps WHERE task_id=$1`, r.View.Task.ID).Scan(&count); err != nil || count != 1 {
					t.Fatal("failed node sleep wrote state", count, err)
				}
			}
		})
	}
}

func TestSleepExpiryWorkCompetitionAndMonotonicRevision(t *testing.T) {
	e := newRecEnv(t)
	ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
	defer cancel()
	p := auth.Principal{Kind: auth.KindAgent, ID: e.agent}
	run := e.send(true, "")
	if err := e.s.RecordSessionFile(ctx, e.agent, "offline.jsonl"); err != nil {
		t.Fatal(err)
	}
	var previous int64
	for i := 0; i < 8; i++ {
		if err := e.s.SetRunRunning(ctx, run.ID, 1); err != nil {
			t.Fatal(err)
		}
		receipt, err := e.s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Sleep.Revision <= previous {
			t.Fatal("scope revision reset")
		}
		if _, _, _, err = e.s.FinishRun(ctx, run.ID, store.RunEnd{Status: "completed"}); err != nil {
			t.Fatal(err)
		}
		if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 2)
		next := uuid.New()
		go func() { done <- e.s.ReconcileSleeps(ctx, 100) }()
		go func() {
			_, _, err := e.s.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: next, Text: "must not lose", WhenBusy: "queue"})
			done <- err
		}()
		for j := 0; j < 2; j++ {
			if err = <-done; err != nil {
				t.Fatal(err)
			}
		}
		queued, err := e.s.QueuedRuns(ctx, e.agent)
		if err != nil || len(queued) != 1 || queued[0].ID != next || queued[0].WakeSleepID == nil || *queued[0].WakeSleepID != receipt.Sleep.ID {
			t.Fatal("duplicate/missing real carrier", queued, err)
		}
		if rec, rev, err := e.s.ActiveSleepProjection(ctx, e.agent, uuid.Nil); err != nil || rec != nil || rev <= receipt.Sleep.Revision {
			t.Fatal("terminal projection", rec, rev, err)
		} else {
			previous = rev
		}
		var ok bool
		run, ok, err = e.s.StartQueuedRun(ctx, next, uuid.New())
		if err != nil || !ok {
			t.Fatal(err)
		}
	}
}

func TestSleepBatchFailureContinuesAndLifecycleCancelsTimer(t *testing.T) {
	e := newRecEnv(t)
	ctx := e.ctx
	other, err := e.s.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "other", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := []uuid.UUID{e.agent, other.ID}
	sleeps := []uuid.UUID{}
	for _, agent := range ids {
		_, run, err := e.s.InsertMessageAndRun(ctx, store.NewRun{AgentID: agent, RunID: uuid.New(), Text: "source", Start: true, InstanceID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.s.SetRunRunning(ctx, run.ID, 1); err != nil {
			t.Fatal(err)
		}
		if err = e.s.RecordSessionFile(ctx, agent, "offline.jsonl"); err != nil {
			t.Fatal(err)
		}
		receipt, err := e.s.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 1}, 100)
		if err != nil {
			t.Fatal(err)
		}
		sleeps = append(sleeps, receipt.Sleep.ID)
		if _, _, _, err = e.s.FinishRun(ctx, run.ID, store.RunEnd{Status: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Pool.Exec(ctx, `CREATE FUNCTION reject_one_sleep() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.agent_id='`+e.agent.String()+`'::uuid THEN RAISE EXCEPTION 'offline injected'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_one_sleep BEFORE UPDATE ON agent_sleeps FOR EACH ROW EXECUTE FUNCTION reject_one_sleep()`); err != nil {
		t.Fatal(err)
	}
	if err = e.s.ReconcileSleeps(ctx, 100); err == nil {
		t.Fatal("fault absent")
	}
	var state string
	if err = e.s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, sleeps[1]).Scan(&state); err != nil || state != "woken" {
		t.Fatal("first error starved other scope", state, err)
	}
	if _, err = e.s.Pool.Exec(ctx, `DROP TRIGGER reject_one_sleep ON agent_sleeps;DROP FUNCTION reject_one_sleep()`); err != nil {
		t.Fatal(err)
	}
	if err = e.s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// Only internal, unclaimed timer work is disposable by lifecycle actions.
	if err = e.s.SoftDeleteAgent(ctx, e.agent); err != nil {
		t.Fatal("idle sleep timer prevented delete", err)
	}
	if _, err = e.s.ResetSession(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	for i, id := range sleeps {
		if err = e.s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, id).Scan(&state); err != nil || state != "cancelled" {
			t.Fatal(i, state, err)
		}
	}
	if err = e.s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var queued int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE origin='sleep_wake' AND status='queued'`).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("stale timer survived", queued, err)
	}
}

func TestSleepTaskSubmitCompetesWithRequest(t *testing.T) {
	for i := 0; i < 6; i++ {
		s, project, manager, agent, r, instances := taskFixture(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		inst := instances[0]
		node := taskmodel.NodeRun{ID: uuid.New(), TaskID: r.View.Task.ID, RuntimeID: inst.ID, AgentID: agent.ID, StepID: "a", Status: "queued", RunIDs: []uuid.UUID{}, Inputs: map[string]any{}}
		r.View.Nodes = []taskmodel.NodeRun{node}
		r.View.Task.CurrentNodeRunID = &node.ID
		if _, err := s.CreateTask(ctx, project.ID, manager.ID, store.TaskAction{ActorKey: "fixture", ID: uuid.New(), Operation: "create", Body: []byte("fixture")}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { return r, instances, nil }); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, inst.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.MutateTask(ctx, r.View.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
			m.Record.View.Task.Status = "running"
			_, err := s.EnqueueTaskTurn(ctx, m, "source", "task_node", 1, nil)
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
		turn, ok, err := s.ClaimTaskTurn(ctx, r.View.Task.ID, inst.ID)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if err = s.MarkTaskRunning(ctx, turn, 1); err != nil {
			t.Fatal(err)
		}
		service := tasks.New(s, nil, nil, tasks.Options{}, nil)
		p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: r.View.Task.ID, RuntimeID: inst.ID}
		done := make(chan error, 2)
		start := make(chan struct{})
		go func() {
			<-start
			_, e := s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: turn.ID, Seconds: 120}, 100)
			done <- e
		}()
		go func() {
			<-start
			_, e := service.Submit(ctx, p, r.View.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}})
			done <- e
		}()
		close(start)
		wins, conflicts := 0, 0
		for j := 0; j < 2; j++ {
			e := <-done
			if e == nil {
				wins++
			} else if apperr.Is(e, apperr.CodeSleepConflict) {
				conflicts++
			} else {
				t.Fatal(e)
			}
		}
		cancel()
		if wins != 1 || conflicts != 1 {
			t.Fatal("sleep/submit did not serialize", wins, conflicts)
		}
	}
}

func TestSleepSameAgentNormalAndTwoTasksAreIndependent(t *testing.T) {
	e := newRecEnv(t)
	ctx := e.ctx
	if _, err := e.s.SetProjectManager(ctx, e.dc.Project.ID, e.agent); err != nil {
		t.Fatal(err)
	}
	action := uuid.New()
	normal := e.send(true, "")
	if err := e.s.SetRunRunning(ctx, normal.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.s.RecordSessionFile(ctx, e.agent, "normal.jsonl"); err != nil {
		t.Fatal(err)
	}
	pNormal := auth.Principal{Kind: auth.KindAgent, ID: e.agent}
	if _, err := e.s.RequestSleep(ctx, pNormal, sleepmodel.Input{ActionID: action, RunID: normal.ID, Seconds: 120}, 100); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.s.FinishRun(ctx, normal.ID, store.RunEnd{Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	principals := []auth.Principal{}
	sleepIDs := []uuid.UUID{}
	for i := 0; i < 2; i++ {
		id, runtime, nodeID := uuid.New(), uuid.New(), uuid.New()
		now := time.Now().UTC()
		n := taskmodel.NodeRun{ID: nodeID, TaskID: id, RuntimeID: runtime, AgentID: e.agent, StepID: "work", Status: "queued", RunIDs: []uuid.UUID{}, Inputs: map[string]any{}}
		r := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: e.dc.Project.ID, Title: "scope", Status: "preparing", Version: 1, CreatedAt: now, UpdatedAt: now, CurrentNodeRunID: &nodeID}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: e.agent}}, Edges: []workflow.Edge{}}, Nodes: []taskmodel.NodeRun{n}, Request: map[string]any{}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
		inst := store.TaskInstance{ID: runtime, TaskID: id, AgentID: e.agent, AgentName: "A", Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}
		if _, err := e.s.CreateTask(ctx, e.dc.Project.ID, e.agent, store.TaskAction{ActorKey: "scope", ID: uuid.New(), Operation: "create", Body: []byte("fixture")}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {
			return r, []store.TaskInstance{inst}, nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='active',session_file='task.jsonl' WHERE id=$1`, runtime); err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			m.Record.View.Task.Status = "running"
			_, err := e.s.EnqueueTaskTurn(ctx, m, "scope source", "task_node", 1, nil)
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
		turn, ok, err := e.s.ClaimTaskTurn(ctx, id, runtime)
		if err != nil || !ok {
			t.Fatal(err)
		}
		if err = e.s.MarkTaskRunning(ctx, turn, 1); err != nil {
			t.Fatal(err)
		}
		p := auth.Principal{Kind: auth.KindAgent, ID: e.agent, TaskID: id, RuntimeID: runtime}
		principals = append(principals, p)
		receipt, err := e.s.RequestSleep(ctx, p, sleepmodel.Input{ActionID: action, RunID: turn.ID, Seconds: 120}, 100)
		if err != nil {
			t.Fatal(err)
		}
		sleepIDs = append(sleepIDs, receipt.Sleep.ID)
		if _, err = e.s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			if _, err := e.s.UpdateTaskTurnTx(ctx, m, turn.ID, "completed", "", nil, nil); err != nil {
				return nil, err
			}
			_, err := e.s.FinishSleepTx(ctx, m.Tx, m, turn.ID, "completed")
			m.Record.Current().Status = "waiting"
			m.Record.View.Task.Status = "waiting"
			return nil, err
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := e.s.SleepState(ctx, principals[1])
	if err != nil || before.Sleep == nil {
		t.Fatal(err)
	}
	wake := *before.Sleep.WakeAt
	e.send(false, "queue")
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, sleepIDs[0]); err != nil {
		t.Fatal(err)
	}
	if err = e.s.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	state, err := e.s.SleepState(ctx, principals[1])
	if err != nil || state.Sleep == nil || state.Sleep.ID != sleepIDs[1] || !state.Sleep.WakeAt.Equal(wake) {
		t.Fatal("another scope disturbed second task", err)
	}
	if normal, err := e.s.SleepState(ctx, pNormal); err != nil || normal.Sleep != nil {
		t.Fatal("normal did not wake independently", err)
	}
	var timers int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, principals[0].RuntimeID).Scan(&timers); err != nil || timers != 1 {
		t.Fatal("first task timer", timers, err)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, principals[1].RuntimeID).Scan(&timers); err != nil || timers != 0 {
		t.Fatal("second task gained first timer", timers, err)
	}
}

// A persistent first-candidate failure must not monopolize batch=1 across
// calls. All data and failure injection live in this test's private PG schema.
func TestSleepBatchOnePersistentFailureIsFair(t *testing.T) {
	e := newRecEnv(t)
	ctx := e.ctx
	createSleep := func(agent uuid.UUID) sleepmodel.Record {
		t.Helper()
		_, run, err := e.s.InsertMessageAndRun(ctx, store.NewRun{AgentID: agent, RunID: uuid.New(), Text: "source", Start: true, InstanceID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.s.SetRunRunning(ctx, run.ID, 1); err != nil {
			t.Fatal(err)
		}
		if err = e.s.RecordSessionFile(ctx, agent, "offline.jsonl"); err != nil {
			t.Fatal(err)
		}
		receipt, err := e.s.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err = e.s.FinishRun(ctx, run.ID, store.RunEnd{Status: "completed"}); err != nil {
			t.Fatal(err)
		}
		return receipt.Sleep
	}
	other, err := e.s.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "healthy", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	bad := createSleep(e.agent)
	healthy := createSleep(other.ID)
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '10 seconds' WHERE id=$1;`, bad.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '5 seconds' WHERE id=$1;`, healthy.ID); err != nil {
		t.Fatal(err)
	}
	var originalWake time.Time
	if err = e.s.Pool.QueryRow(ctx, `SELECT wake_at FROM agent_sleeps WHERE id=$1`, bad.ID).Scan(&originalWake); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.Pool.Exec(ctx, `CREATE FUNCTION reject_earliest_sleep() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='`+bad.ID.String()+`'::uuid THEN RAISE EXCEPTION 'offline persistent first candidate failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_earliest_sleep BEFORE UPDATE ON agent_sleeps FOR EACH ROW EXECUTE FUNCTION reject_earliest_sleep()`); err != nil {
		t.Fatal(err)
	}
	failures := 0
	if err = e.s.ReconcileSleeps(ctx, 1); err == nil {
		t.Fatal("first persistent failure was not exercised")
	}
	failures++
	// A newly eligible scope joins a subsequent finite round; it cannot reset
	// progress to the failed first owner or cause the old healthy carrier twice.
	lateAgent, err := e.s.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "late-healthy", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	late := createSleep(lateAgent.ID)
	if _, err = e.s.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, late.ID); err != nil {
		t.Fatal(err)
	}

	// The new, higher ID must not extend the first round. The original
	// healthy item runs next, then the failed owner is retried before late.
	if err = e.s.ReconcileSleeps(ctx, 1); err != nil {
		t.Fatal("batch=1 first failure starved the next healthy owner", err)
	}
	if err = e.s.ReconcileSleeps(ctx, 1); err == nil {
		t.Fatal("new arrival extended the round instead of retrying failed owner")
	}
	failures++
	if err = e.s.ReconcileSleeps(ctx, 1); err != nil {
		t.Fatal("late owner did not join the next round", err)
	}
	var state string
	if err = e.s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, healthy.ID).Scan(&state); err != nil || state != "woken" {
		t.Fatalf("batch=1 persistent earliest failure starved healthy scope: state=%s error=%v", state, err)
	}
	if failures < 2 {
		t.Fatal("failed candidate was not retried on later rounds", failures)
	}
	var unchanged time.Time
	var badTimers int
	if err = e.s.Pool.QueryRow(ctx, `SELECT wake_at FROM agent_sleeps WHERE id=$1 AND state='sleeping'`, bad.ID).Scan(&unchanged); err != nil || !unchanged.Equal(originalWake) {
		t.Fatal("failure changed original deadline", err)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE wake_sleep_id=$1`, bad.ID).Scan(&badTimers); err != nil || badTimers != 0 {
		t.Fatal("failed transaction leaked timer", badTimers, err)
	}
	for i := 0; i < 4; i++ {
		_ = e.s.ReconcileSleeps(ctx, 1)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, late.ID).Scan(&state); err != nil || state != "woken" {
		t.Fatal("new due scope starved", state, err)
	}
	if _, err = e.s.Pool.Exec(ctx, `DROP TRIGGER reject_earliest_sleep ON agent_sleeps; DROP FUNCTION reject_earliest_sleep()`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err = e.s.ReconcileSleeps(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{bad.ID, healthy.ID, late.ID} {
		var count int
		var carrier, timer uuid.UUID
		if err = e.s.Pool.QueryRow(ctx, `SELECT wake_run_id,timer_run_id FROM agent_sleeps WHERE id=$1 AND state='woken'`, id).Scan(&carrier, &timer); err != nil || carrier != timer {
			t.Fatal("stable carrier not recovered", id, err)
		}
		if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE wake_sleep_id=$1`, id).Scan(&count); err != nil || count != 1 {
			t.Fatal("fair scan duplicated/lost carrier", id, count, err)
		}
	}
	t.Logf("batch=1 badSleepId=%s healthySleepId=%s lateSleepId=%s persistentFailureScans=%d stableCarriers=3 originalBadWakeAt=%s", bad.ID, healthy.ID, late.ID, failures, originalWake.Format(time.RFC3339Nano))
}
