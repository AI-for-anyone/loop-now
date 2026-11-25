package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// nodeListEvents returns the node lists put on the task's event stream, in order.
func nodeListEvents(t *testing.T, st *store.Store, task uuid.UUID) []apitypes.TaskNodeBackgroundTasks {
	t.Helper()
	events, err := st.TaskEvents(context.Background(), &task, nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []apitypes.TaskNodeBackgroundTasks
	for _, e := range events {
		if e.Type != EventNodeBackgroundTasks {
			continue
		}
		var v apitypes.TaskNodeBackgroundTasks
		if err := json.Unmarshal(e.Data, &v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// A background task of a task instance is kept on the task turn that started it, and the node's running list goes to
// the task's event stream on every change. At startup the tasks still recorded as running become unknown and the node
// gets an empty list.
func TestTaskBackgroundTasksOnNode(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	tt := turn(t, s, id)
	obs := &Observer{Store: s.Store, Normal: executor.NopObserver{}, Timeout: 5 * time.Second}
	task := executor.BackgroundTask{Key: uuid.Must(uuid.NewV7()), ID: "b1", Runtime: "claude-code", Kind: "command", Label: "npm test", RunID: tt.ID,
		StartedAt: time.Now().UTC(), Status: "running"}
	obs.BackgroundTask(p.RuntimeID, task)
	running, err := s.Store.RunningNodeBackgroundTasks(ctx, tt.NodeRunID)
	if err != nil || len(running) != 1 || running[0].TaskTurnID == nil || *running[0].TaskTurnID != tt.ID || running[0].RunID != nil || running[0].AgentID != p.RuntimeID {
		t.Fatalf("kept on the task turn: %+v %v", running, err)
	}
	lists := nodeListEvents(t, s.Store, id)
	if len(lists) != 1 || lists[0].NodeRunID != tt.NodeRunID || lists[0].RuntimeID != p.RuntimeID || len(lists[0].Tasks) != 1 || lists[0].Tasks[0].ID != "b1" || lists[0].Tasks[0].RunID != tt.ID {
		t.Fatalf("the node's list on the task stream: %+v", lists)
	}
	if err := ReconcileBackgroundTasks(ctx, s.Store, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if running, _ := s.Store.RunningNodeBackgroundTasks(ctx, tt.NodeRunID); len(running) != 0 {
		t.Fatalf("nothing runs after a restart: %+v", running)
	}
	lists = nodeListEvents(t, s.Store, id)
	if len(lists) != 2 || len(lists[1].Tasks) != 0 {
		t.Fatalf("the node's list is emptied: %+v", lists)
	}
	ended, err := s.Store.TaskTurnBackgroundTasks(ctx, tt.ID)
	if err != nil || len(ended) != 1 || ended[0].Status != "unknown" || ended[0].EndedAt == nil {
		t.Fatalf("the turn keeps the task as unknown: %+v %v", ended, err)
	}
}

// stubStopper is an executor whose StopBackground ends the task the way the recorder keeps it.
type stubStopper struct {
	stubExecutor
	st      *store.Store
	running map[string]store.BackgroundTask
	agents  []uuid.UUID
}

func (s *stubStopper) StopBackground(ctx context.Context, agentID uuid.UUID, id, reason string) (executor.BackgroundTask, error) {
	t, ok := s.running[id]
	if !ok {
		return executor.BackgroundTask{}, executor.ErrBackgroundNotFound
	}
	s.agents = append(s.agents, agentID)
	now := time.Now().UTC()
	t.Status, t.EndedAt, t.StopReason = "stopped", &now, &reason
	if err := s.st.SaveBackgroundTask(ctx, t); err != nil {
		return executor.BackgroundTask{}, err
	}
	delete(s.running, id)
	return executor.BackgroundTask{Key: t.Key, ID: id, Status: "stopped"}, nil
}

func (s *stubStopper) StopAllBackground(ctx context.Context, agentID uuid.UUID, reason string) ([]executor.BackgroundTask, error) {
	return nil, errors.New("the node stops its own tasks one by one")
}

// The node's chat lists the background tasks its turns started and stops one or all of them, for the instance the
// person sees; another target is a conflict, an ended task too, and a task of no turn of this node is not found.
func TestNodeBackgroundTasksStop(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	tt := turn(t, s, id)
	keep := func(native, status string) store.BackgroundTask {
		turnID := tt.ID
		b := store.BackgroundTask{Key: uuid.Must(uuid.NewV7()), AgentID: p.RuntimeID, TaskTurnID: &turnID, NativeID: native, Runtime: "claude-code",
			Kind: "command", Label: "npm test", StartedAt: time.Now().UTC(), Status: "running"}
		if status != "running" {
			now := time.Now().UTC()
			b.Status, b.EndedAt = status, &now
		}
		if err := s.Store.SaveBackgroundTask(ctx, b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, c := keep("a", "running"), keep("c", "running")
	keep("b", "completed")
	stopper := &stubStopper{st: s.Store, running: map[string]store.BackgroundTask{"a": a, "c": c}}
	s.Executor = stopper
	console := auth.Principal{Kind: auth.KindConsole}
	list, err := s.NodeBackgroundTasks(ctx, console, id, tt.NodeRunID)
	if err != nil || len(list.Tasks) != 2 || list.RuntimeID != p.RuntimeID {
		t.Fatalf("the node's list: %+v %v", list, err)
	}
	in := taskmodel.TaskNodeQueueActionInput{ActionID: uuid.New(), ExpectedAgentID: p.ID, ExpectedRuntimeID: p.RuntimeID}
	out, err := s.StopNodeBackgroundTask(ctx, console, id, tt.NodeRunID, "a", in)
	if err != nil {
		t.Fatal(err)
	}
	var got apitypes.BackgroundTask
	if err := json.Unmarshal(out, &got); err != nil || got.ID != "a" || got.Status != "stopped" || got.RunID != tt.ID {
		t.Fatalf("stopped: %s %v", out, err)
	}
	if again, err := s.StopNodeBackgroundTask(ctx, console, id, tt.NodeRunID, "a", in); err != nil || string(again) != string(out) {
		t.Fatalf("the same actionId answers the same: %s %v", again, err)
	}
	if len(stopper.agents) != 1 || stopper.agents[0] != p.RuntimeID {
		t.Fatalf("stopped on the node's instance, once: %v", stopper.agents)
	}
	wrong := in
	wrong.ActionID, wrong.ExpectedRuntimeID = uuid.New(), uuid.New()
	if _, err := s.StopNodeBackgroundTask(ctx, console, id, tt.NodeRunID, "c", wrong); !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatalf("another instance is a conflict: %v", err)
	}
	in.ActionID = uuid.New()
	if _, err := s.StopNodeBackgroundTask(ctx, console, id, tt.NodeRunID, "b", in); !apperr.Is(err, apperr.CodeBackgroundTaskEnded) {
		t.Fatalf("an ended task: %v", err)
	}
	in.ActionID = uuid.New()
	if _, err := s.StopNodeBackgroundTask(ctx, console, id, tt.NodeRunID, "zzz", in); !apperr.Is(err, apperr.CodeBackgroundTaskNotFound) {
		t.Fatalf("an unknown task: %v", err)
	}
	in.ActionID = uuid.New()
	all, err := s.StopAllNodeBackgroundTasks(ctx, console, id, tt.NodeRunID, in)
	var stopped apitypes.BackgroundTaskList
	if err != nil || json.Unmarshal(all, &stopped) != nil || len(stopped.Tasks) != 1 || stopped.Tasks[0].ID != "c" || stopped.Tasks[0].Status != "stopped" {
		t.Fatalf("stop all: %s %v", all, err)
	}
	if _, err := s.NodeBackgroundTasks(ctx, p, id, tt.NodeRunID); !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatalf("only the console: %v", err)
	}
}
