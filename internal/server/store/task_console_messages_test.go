package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
)

// Own schema only. No stale-schema sweep, Redis, helper, Pi or external network.
func taskConsoleStore(t *testing.T) *store.Store { return taskConsoleStoreAt(t, 0) }

// taskConsoleStoreAt stops at ceiling (0: latest); 026 cannot be rolled back.
func taskConsoleStoreAt(t *testing.T, ceiling int64) *store.Store {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("TEST connection unavailable")
	}
	schema := "task_console_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = c.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		c.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	s, err := store.Open(ctx, url, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = migrateFixture(ctx, s, ceiling); err != nil {
		t.Fatal(err)
	}
	t.Logf("isolated schema=%s", schema)
	return s
}

func taskConsoleRecord(t *testing.T, s *store.Store) (uuid.UUID, uuid.UUID, uuid.UUID, store.TaskTurn) {
	t.Helper()
	ctx := context.Background()
	d, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.ProjectScope(d.Project.ID), "node-agent", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectManager(ctx, d.Project.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	id, node, runtime := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	scope, _ := taskruntime.TaskScope(a.ID, id)
	snap, err := taskruntime.Capture(scope, uuid.New(), taskruntime.CaptureInput{Configuration: taskruntime.Configuration{AgentID: a.ID, ConfigVersion: 1, Provider: "openai", ModelID: "fixture", Prompts: &prompts.Layers{Format: prompts.FormatV1, AgentVersion: 1}, Skills: []taskruntime.Skill{}, CLIs: []string{}}, Credentials: taskruntime.Credentials{Model: map[string]string{"OPENAI_API_KEY": "synthetic-model-key"}, CLI: map[string]taskruntime.CredentialPayload{}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	n := taskmodel.NodeRun{ID: node, TaskID: id, StepID: "work", AgentID: a.ID, AgentName: a.Name, RuntimeID: runtime, Visit: 1, Attempt: 1, Status: "running", Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}}
	r := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: d.Project.ID, Status: "preparing", Title: "fixture", Version: 1, CreatedAt: now, UpdatedAt: now, CurrentNodeRunID: &node}, Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: a.ID}}, Edges: []workflow.Edge{}}, Nodes: []taskmodel.NodeRun{n}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	_, err = s.CreateTask(ctx, d.Project.ID, a.ID, store.TaskAction{ActorKey: a.ID.String(), ID: uuid.New(), Operation: "fixture", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return r, []store.TaskInstance{{ID: runtime, TaskID: id, AgentID: a.ID, AgentName: a.Name, Snapshot: encoded, Sources: []store.TaskFrozenSource{}, Prepared: true}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var turn store.TaskTurn
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "running"
		if _, e := m.Tx.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, runtime); e != nil {
			return nil, e
		}
		var e error
		turn, e = s.EnqueueTaskTurn(ctx, m, "synthetic original input", "task_node", 1, nil)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, node, a.ID, turn
}

func TestTaskConsoleMarkerCausalityAndLegacy(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, _, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	claimed, ok, err := s.ClaimTaskTurn(ctx, id, turn.RuntimeID)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	if err = s.MarkTaskRunning(ctx, claimed, 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		// A historical failure + blocked text is insufficient without the marker.
		if _, e := m.Tx.Exec(ctx, `UPDATE task_turns SET status='failed',execution_mode='task_node' WHERE id=$1`, turn.ID); e != nil {
			return nil, e
		}
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		reason := "loop limit / synthetic reason"
		m.Record.Current().Reason = &reason
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, node)
		if eligible {
			t.Fatal("failure history inferred current block cause")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.RecordTaskConsoleEligibilityTx(ctx, m, uuid.New())
	})
	if !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("wrong source accepted", err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if e := s.RecordTaskConsoleEligibilityTx(ctx, m, turn.ID); e != nil {
			return nil, e
		}
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, node)
		if !eligible {
			t.Fatal("explicit causal marker missing")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		next := *m.Record.Current()
		next.ID = uuid.New()
		next.Attempt++
		m.Record.View.Nodes = append(m.Record.View.Nodes, next)
		m.Record.View.Task.CurrentNodeRunID = &next.ID
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, next.ID)
		if eligible {
			t.Fatal("new attempt inherited marker")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MigrateDownTo(ctx, 14); err == nil {
		t.Fatal("Down discarded eligibility history")
	}
}

func TestTaskConsoleRecoveryCapturesActualActiveRun(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, _, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	claimed, ok, err := s.ClaimTaskTurn(ctx, id, turn.RuntimeID)
	if err != nil || !ok {
		t.Fatal("claim fixture", err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		source, e := s.TaskConsoleRecoverySourceTx(ctx, m)
		if e != nil {
			return nil, e
		}
		if source != claimed.ID {
			t.Fatal("did not capture actual active run")
		}
		_, interrupted, e := s.RecoverTaskTurns(ctx, m)
		if e != nil {
			return nil, e
		}
		if !interrupted {
			t.Fatal("active current run not interrupted")
		}
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		if e = s.RecordTaskConsoleEligibilityTx(ctx, m, source); e != nil {
			return nil, e
		}
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, node)
		if !eligible {
			t.Fatal("recovery marker missing")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskConsoleLineageImmutableAndMigration(t *testing.T) {
	s := taskConsoleStoreAt(t, 25)
	ctx := context.Background()
	if err := s.MigrateDownTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	id, _, agent, turn := taskConsoleRecord(t, s)
	_, err := s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		for i := 0; i < 2; i++ {
			if e := s.AttachTaskConsoleLineageTx(ctx, m, turn, agent, "task_conversation"); e != nil {
				return nil, e
			}
		}
		l, found, e := s.TaskConsoleLineageTx(ctx, m.Tx, turn.ID)
		if !found || l.NodeRunID != turn.NodeRunID || l.RuntimeID != turn.RuntimeID || l.ExecutionMode != "task_conversation" {
			t.Fatal("lineage changed")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.AttachTaskConsoleLineageTx(ctx, m, turn, agent, "task_node")
	})
	if !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("lineage mode silently changed", err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.AttachTaskConsoleLineageTx(ctx, m, turn, uuid.New(), "task_conversation")
	})
	if !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("wrong Agent accepted", err)
	}
	if err = s.MigrateDownTo(ctx, 14); err == nil {
		t.Fatal("Down discarded strict lineage")
	}
}

func TestTaskConsoleServiceAtomicIdempotenceAndGuard(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	t.Cleanup(func() { _ = svc.Stop(ctx) })
	p := auth.Principal{Kind: auth.KindConsole}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: turn.RuntimeID, Text: "原节点正文"}
	before, err := s.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cap, err := svc.ConsoleMessageCapability(ctx, p, id, node)
	if err != nil || !cap.CanSend {
		t.Fatal("local captured facts did not allow normal queue", cap, err)
	}
	after, _ := s.GetTask(ctx, id)
	if after.View.Task.Version != before.View.Task.Version {
		t.Fatal("capability GET changed task version")
	}
	first, err := svc.SendConsoleMessage(ctx, p, id, node, in, 4096)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.SendConsoleMessage(ctx, p, id, node, in, 4096)
	if err != nil || string(first) != string(second) {
		t.Fatal("stable action replay changed receipt", err)
	}
	var receipt taskmodel.TaskNodeMessageReceipt
	if err = json.Unmarshal(first, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Message.Text != in.Text || receipt.NodeRunID != node || receipt.Message.Origin != "console_node" || receipt.Status != "queued" {
		t.Fatal("receipt lost original identity")
	}
	var count int
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated queue", err)
	}
	in.Text = "different body"
	if _, err = svc.SendConsoleMessage(ctx, p, id, node, in, 4096); !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("different action body accepted", err)
	}
	in.Text = "原节点正文"
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		next := *m.Record.Current()
		next.ID = uuid.New()
		next.Attempt++
		m.Record.View.Nodes = append(m.Record.View.Nodes, next)
		m.Record.View.Task.CurrentNodeRunID = &next.ID
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// An already committed action returns its old receipt after target change.
	replayed, err := svc.SendConsoleMessage(ctx, p, id, node, in, 4096)
	if err != nil || string(replayed) != string(first) {
		t.Fatal("old receipt rebound or rejected", err)
	}
	in.ActionID = uuid.New()
	if _, err = svc.SendConsoleMessage(ctx, p, id, node, in, 4096); !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("new request accepted historical attempt", err)
	}
	cap, err = svc.ConsoleMessageCapability(ctx, p, id, node)
	if err != nil || cap.CanSend {
		t.Fatal("historical capability writable", err)
	}
	if _, err = svc.SendConsoleMessage(ctx, auth.Principal{Kind: auth.KindAgent}, id, node, in, 4096); !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatal("Agent granted console authority", err)
	}
}

func TestTaskConsoleBlockedPredicateAndModeDoNotDrift(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, agent, source := taskConsoleRecord(t, s)
	ctx := context.Background()
	claimed, ok, err := s.ClaimTaskTurn(ctx, id, source.RuntimeID)
	if err != nil || !ok {
		t.Fatal("claim", err)
	}
	if err = s.MarkTaskRunning(ctx, claimed, 1); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	t.Cleanup(func() { _ = svc.Stop(ctx) })
	p := auth.Principal{Kind: auth.KindConsole}
	file := filepath.Join(t.TempDir(), "original-session.jsonl")
	if err := os.WriteFile(file, []byte("{\"type\":\"session\",\"id\":\"synthetic\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if _, e := m.Tx.Exec(ctx, `UPDATE task_turns SET status='failed',execution_mode='task_node' WHERE id=$1`, source.ID); e != nil {
			return nil, e
		}
		if _, e := m.Tx.Exec(ctx, `UPDATE task_instances SET session_state='active',session_file=$2 WHERE id=$1`, source.RuntimeID, file); e != nil {
			return nil, e
		}
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cap, err := svc.ConsoleMessageCapability(ctx, p, id, node)
	if err != nil || cap.CanSend || cap.Reason == nil {
		t.Fatal("unknown block silently opened", err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.RecordTaskConsoleEligibilityTx(ctx, m, source.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	cap, err = svc.ConsoleMessageCapability(ctx, p, id, node)
	if err != nil || !cap.CanSend {
		t.Fatal("causal prepared original session could not chat", cap, err)
	}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: source.RuntimeID, Text: "排查原异常"}
	b, err := svc.SendConsoleMessage(ctx, p, id, node, in, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var receipt taskmodel.TaskNodeMessageReceipt
	_ = json.Unmarshal(b, &receipt)
	r, _ := s.GetTask(ctx, id)
	if r.View.Task.Status != "blocked" || r.Current().Attempt != 1 {
		t.Fatal("chat admission resumed workflow")
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		l, found, e := s.TaskConsoleLineageTx(ctx, m.Tx, receipt.RunID)
		if e != nil {
			return nil, e
		}
		if !found || l.ExecutionMode != "task_conversation" {
			t.Fatal("blocked mode not persisted")
		}
		// Simulate a lifecycle change after acceptance. Claim must not remap mode.
		m.Record.Current().Status = "running"
		m.Record.View.Task.Status = "running"
		turn, e := s.TaskTurn(ctx, receipt.RunID)
		if e != nil {
			return nil, e
		}
		strict, valid, e := s.GuardTaskConsoleTurnTx(ctx, m, turn)
		if !strict || valid {
			t.Fatal("accepted mode silently reinterpreted")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.TaskTurn(ctx, receipt.RunID)
	if err != nil || turn.Status != "interrupted" || turn.ErrorCode == nil || *turn.ErrorCode != "task_conflict" || turn.NodeRunID != node {
		t.Fatal("expired input lost original visible reason", err)
	}
}

func TestTaskConsoleConcurrentReplaySingleReceipt(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	t.Cleanup(func() { _ = svc.Stop(ctx) })
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: turn.RuntimeID, Text: "只接收一次", WhenBusy: "queue"}
	const callers = 8
	results := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			b, e := svc.SendConsoleMessage(ctx, auth.Principal{Kind: auth.KindConsole}, id, node, in, 4096)
			results[i], errs[i] = string(b), e
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil || results[i] != results[0] {
			t.Fatal("concurrent replay diverged", e)
		}
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent replay duplicated run", err)
	}
}

func TestTaskConsoleFailedPreparationCannotMintMarker(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, _, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	_, err := s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if _, e := m.Tx.Exec(ctx, `UPDATE task_turns SET status='failed',execution_mode='task_node' WHERE id=$1`, turn.ID); e != nil {
			return nil, e
		}
		m.Record.Current().Status = "blocked"
		m.Record.View.Task.Status = "blocked"
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		return nil, s.RecordTaskConsoleEligibilityTx(ctx, m, turn.ID)
	})
	if !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("failure before run.running minted eligibility", err)
	}
	_, err = s.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, node)
		if eligible {
			t.Fatal("preparation failure considered executed cause")
		}
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
}
