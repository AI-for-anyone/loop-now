package store_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/testutil/sqlitetest"
)

func taskConsoleStore(t *testing.T) *store.Store {
	t.Helper()
	return sqlitetest.NewStore(t)
}

func taskConsoleRecord(t *testing.T, s *store.Store) (uuid.UUID, uuid.UUID, uuid.UUID, store.TaskTurn) {
	t.Helper()
	return taskConsoleRecordWith(t, s, "openai", map[string]string{"OPENAI_API_KEY": "synthetic-model-key"})
}

// taskConsoleRecordWith is taskConsoleRecord for an Agent whose task captured provider and the model environment model.
func taskConsoleRecordWith(t *testing.T, s *store.Store, provider string, model map[string]string) (uuid.UUID, uuid.UUID, uuid.UUID, store.TaskTurn) {
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
	snap, err := taskruntime.Capture(scope, uuid.New(), taskruntime.CaptureInput{Configuration: taskruntime.Configuration{AgentID: a.ID, ConfigVersion: 1, Provider: provider, ModelID: "fixture", Prompts: &prompts.Layers{Format: prompts.FormatV1, AgentVersion: 1}, Skills: []taskruntime.Skill{}, CLIs: []string{}}, Credentials: taskruntime.Credentials{Model: model, CLI: map[string]taskruntime.CredentialPayload{}}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	n := taskmodel.NodeRun{ID: node, TaskID: id, StepID: "work", AgentID: a.ID, AgentName: a.Name, RuntimeID: runtime, Visit: 1, Attempt: 1, Status: "running", Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}}
	r := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: d.Project.ID, Status: "preparing", Title: "fixture", Version: 1, CreatedAt: now, UpdatedAt: now, CurrentNodeRunID: &node}, Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: a.ID}}, Edges: []workflow.Edge{}}, Nodes: []taskmodel.NodeRun{n}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	_, err = s.CreateTask(ctx, d.Project.ID, a.ID, store.TaskAction{ActorKey: a.ID.String(), ID: uuid.New(), Operation: "fixture", Body: []byte(`{}`)}, func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) {
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

func TestTaskConsoleLineageImmutable(t *testing.T) {
	s := taskConsoleStore(t)
	ctx := context.Background()
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
}

// taskConsoleGuard is the reason TaskConsoleGuardTx gives for target, after change (if any) ran in the same transaction.
func taskConsoleGuard(t *testing.T, s *store.Store, target store.TaskConsoleLineage, change func(*store.TaskMutation) error) string {
	t.Helper()
	ctx := context.Background()
	var reason string
	_, err := s.MutateTask(ctx, target.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		if change != nil {
			if e := change(m); e != nil {
				return nil, e
			}
		}
		var e error
		reason, e = s.TaskConsoleGuardTx(ctx, m, target)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	return reason
}

// A task step of an Agent on a CLI that uses its own login on this computer (Claude Code, Codex, a local ACP command)
// captured no model credential, and its node still takes console messages. Every other check still applies to it, and
// a Pi Agent without its captured model credential still cannot be messaged.
func TestTaskConsoleGuardLocalAccountsNeedNoCapturedModelCredential(t *testing.T) {
	acp := uuid.New()
	agentruntime.SetLocalCommands([]agentruntime.Runtime{agentruntime.LocalCommandRuntime(acp, "Mine", agentruntime.LocalCommand{Command: "mine"})})
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	ctx := context.Background()
	for _, provider := range []string{agentruntime.ClaudeCodeProvider, agentruntime.CodexProvider, agentruntime.LocalCommandRuntimeID(acp)} {
		t.Run(provider, func(t *testing.T) {
			s := taskConsoleStore(t)
			id, node, agent, turn := taskConsoleRecordWith(t, s, provider, map[string]string{})
			target := store.TaskConsoleLineage{TaskID: id, NodeRunID: node, AgentID: agent, RuntimeID: turn.RuntimeID}
			if reason := taskConsoleGuard(t, s, target, nil); reason != "" {
				t.Fatalf("running node of a local-login Agent: reason %q, want available", reason)
			}
			svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
			t.Cleanup(func() { _ = svc.Stop(ctx) })
			capability, err := svc.ConsoleMessageCapability(ctx, auth.Principal{Kind: auth.KindConsole}, id, node)
			if err != nil || !capability.CanSend || capability.Reason != nil {
				t.Fatal("console capability of a local-login Agent's running node", capability, err)
			}

			other := target
			other.ExecutionMode = "task_conversation"
			if reason := taskConsoleGuard(t, s, other, nil); reason != "chat_unavailable" {
				t.Fatalf("mode mismatch: reason %q", reason)
			}
			other = target
			other.RuntimeID = uuid.New()
			if reason := taskConsoleGuard(t, s, other, nil); reason != "target_mismatch" {
				t.Fatalf("other runtime: reason %q", reason)
			}
			blocked := func(m *store.TaskMutation) error { m.Record.Current().Status = "blocked"; return nil }
			if reason := taskConsoleGuard(t, s, target, blocked); reason != "chat_unavailable" {
				t.Fatalf("blocked node without a confirmed run failure: reason %q", reason)
			}
			broken := func(m *store.TaskMutation) error {
				_, e := m.Tx.Exec(ctx, `UPDATE task_instances SET snapshot=$1 WHERE id=$2`, []byte(`{}`), turn.RuntimeID)
				return e
			}
			if reason := taskConsoleGuard(t, s, target, broken); reason != "chat_unavailable" {
				t.Fatalf("unreadable captured configuration: reason %q", reason)
			}
			retired := func(m *store.TaskMutation) error {
				_, e := m.Tx.Exec(ctx, `UPDATE task_instances SET retired=true WHERE id=$1`, turn.RuntimeID)
				return e
			}
			if reason := taskConsoleGuard(t, s, target, retired); reason != "instance_unavailable" {
				t.Fatalf("retired instance: reason %q", reason)
			}
		})
	}
	t.Run("pi without its captured model credential", func(t *testing.T) {
		s := taskConsoleStore(t)
		id, node, agent, turn := taskConsoleRecordWith(t, s, "openai", map[string]string{})
		target := store.TaskConsoleLineage{TaskID: id, NodeRunID: node, AgentID: agent, RuntimeID: turn.RuntimeID}
		if reason := taskConsoleGuard(t, s, target, nil); reason != "chat_unavailable" {
			t.Fatalf("Pi Agent without a model credential: reason %q, want chat_unavailable", reason)
		}
	})
}

func TestTaskConsoleServiceAtomicIdempotenceAndGuard(t *testing.T) {
	s := taskConsoleStore(t)
	id, node, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	svc := tasks.New(s, nil, nil, tasks.Options{}, quietLogger())
	t.Cleanup(func() { _ = svc.Stop(ctx) })
	p := auth.Principal{Kind: auth.KindConsole}
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: turn.RuntimeID, Text: "original node message"}
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
	in.Text = "original node message"
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
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: source.RuntimeID, Text: "investigate the original failure"}
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
	in := taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: agent, ExpectedRuntimeID: turn.RuntimeID, Text: "accepted only once", WhenBusy: "queue"}
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
