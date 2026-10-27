package tasks

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/testutil/redistest"
)

type controlledExecutor struct {
	stubExecutor
	execute func(context.Context, executor.Request, executor.Sink) (executor.Result, error)
}

func (e *controlledExecutor) Execute(ctx context.Context, r executor.Request, sink executor.Sink) (executor.Result, error) {
	return e.execute(ctx, r, sink)
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func TestSameAgentTasksHaveIndependentLocksAndRestoreRedisLoss(t *testing.T) {
	s, first, p := fixture(t, false)
	ctx := context.Background()
	r := current(t, s, first)
	second, runtime2 := uuid.New(), uuid.New()
	inst := store.TaskInstance{ID: runtime2, TaskID: second, AgentID: p.ID, AgentName: r.Current().AgentName, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}
	other := r
	other.View.Task.ID = second
	other.View.Task.Status = "preparing"
	other.View.Task.Version = 1
	node := freshNode(second, r.View.Workflow.Steps[0], inst, 1, 1)
	other.View.Nodes = []taskmodel.NodeRun{node}
	setCurrent(&other, node)
	_, e := s.Store.CreateTask(ctx, r.View.Task.ProjectID, p.ID, store.TaskAction{ActorKey: "fixture2", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return other, []store.TaskInstance{inst}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Store.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE id=$1`, runtime2)
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range []uuid.UUID{first, second} {
		_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			m.Record.View.Task.Status = "running"
			return nil, s.prepareNode(ctx, m, "")
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	redis, prefix := redistest.New(t)
	s.Coord = coord.New(redis, coord.Options{Prefix: prefix, LockTTL: 500 * time.Millisecond, StreamMaxLen: 100, RunTextTTL: time.Hour})
	s.Options.InstanceID = uuid.New()
	s.Options.LockRenewInterval = 50 * time.Millisecond
	s.Options.RecordTimeout = time.Second
	s.Options.ReconcileInterval = 50 * time.Millisecond
	starts := make(chan executor.Request, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	calls := map[uuid.UUID]int{}
	s.Executor = &controlledExecutor{execute: func(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
		mu.Lock()
		calls[req.AgentID]++
		mu.Unlock()
		starts <- req
		sink.Running(1)
		sink.Tool("tool", "bash", "started")
		sink.TextDelta("reply")
		inst, e := s.Store.TaskInstance(ctx, req.AgentID)
		if e != nil {
			return executor.Result{}, e
		}
		r, e := s.Store.GetTask(ctx, inst.TaskID)
		if e != nil {
			return executor.Result{}, e
		}
		_, e = s.Submit(ctx, auth.Principal{Kind: auth.KindAgent, ID: inst.AgentID, RuntimeID: inst.ID, TaskID: inst.TaskID}, inst.TaskID, r.Current().ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"answer": "saved"}})
		if e != nil {
			return executor.Result{}, e
		}
		select {
		case <-release:
			return executor.Result{Outcome: "completed", Text: "reply"}, nil
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}}
	if e = s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer s.Stop(context.Background())
	for range 2 {
		select {
		case <-starts:
		case <-time.After(4 * time.Second):
			t.Fatal("tasks did not run concurrently")
		}
	}
	for _, runtime := range []uuid.UUID{p.RuntimeID, runtime2} {
		owner, e := s.Coord.LockOwner(ctx, runtime.String())
		if e != nil || owner == "" {
			t.Fatal("instance lock missing", e)
		}
		if e = redis.Del(ctx, s.Coord.Keys().Lock(runtime.String())).Err(); e != nil {
			t.Fatal(e)
		}
	}
	eventually(t, func() bool {
		for _, runtime := range []uuid.UUID{p.RuntimeID, runtime2} {
			owner, e := s.Coord.LockOwner(ctx, runtime.String())
			if e != nil || owner == "" {
				return false
			}
		}
		return true
	})
	msg := store.NewInboxMessage{ID: uuid.New(), From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}, Text: "queued while busy", WhenBusy: "queue"}
	if e = s.DeliverInbox(ctx, msg); e != nil {
		t.Fatal(e)
	}
	_, normal, e := s.Coord.QueueContents(ctx, p.RuntimeID.String())
	if e != nil || len(normal) != 1 {
		t.Fatal("queued task message not mirrored", e, normal)
	}
	if e = redis.Del(ctx, s.Coord.Keys().QueueNormal(p.RuntimeID.String())).Err(); e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool {
		_, normal, e := s.Coord.QueueContents(ctx, p.RuntimeID.String())
		return e == nil && len(normal) == 1
	})
	mu.Lock()
	if calls[p.RuntimeID] != 1 || calls[runtime2] != 1 {
		t.Fatal("Redis loss replayed tools")
	}
	mu.Unlock()
	close(release)
	eventually(t, func() bool {
		return current(t, s, first).View.Task.Status == "completed" && current(t, s, second).View.Task.Status == "completed"
	})
	events, e := s.Coord.ReadEvents(ctx, coord.ProjectTaskStream(r.View.Task.ProjectID.String()), "0-0", 1000, 0)
	if e != nil || len(events) == 0 {
		t.Fatal("project Redis relay missing", e)
	}
	var previous int64
	for _, event := range events {
		var persistent StreamEvent
		if e = json.Unmarshal(event.Data, &persistent); e != nil || persistent.ID <= previous {
			t.Fatal("project relay lost durable ordering", e)
		}
		previous = persistent.ID
	}
	var tools int
	if e = s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE agent_id=$1 AND task_id=ANY($2) AND task_turn_id IS NOT NULL AND type='tool.changed'`, p.ID, []uuid.UUID{first, second}).Scan(&tools); e != nil || tools != 2 {
		t.Fatal("task tools not associated", e, tools)
	}
}
