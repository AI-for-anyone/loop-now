package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/pgtest"
)

func taskFixture(t *testing.T) (*store.Store, store.Project, store.Agent, store.Agent, taskmodel.Record, []store.TaskInstance) {
	t.Helper()
	ctx := context.Background()
	s := pgtest.NewStore(t)
	dc, err := s.EnsureDefaults(ctx, "组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, dc.Organization.ID, "通用项目")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateAgent(ctx, store.ProjectScope(p.ID), "Manager", "example", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, store.ProjectScope(p.ID), "执行者", "example", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectManager(ctx, p.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	now := time.Now().UTC()
	r := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: p.ID, Title: "通用任务", Status: "preparing", Version: 1, CreatedAt: now, UpdatedAt: now}, Request: map[string]any{"text": "sample"}, Workflow: workflow.Definition{StartStepID: "a", Steps: []workflow.Step{{ID: "a", Title: "业务自定", AgentID: a.ID}}, Edges: []workflow.Edge{}}, Nodes: []taskmodel.NodeRun{}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	instances := []store.TaskInstance{{ID: uuid.New(), TaskID: id, AgentID: a.ID, AgentName: a.Name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}}
	return s, p, m, a, r, instances
}

func TestTaskActionIdempotencyAndDeletionProtection(t *testing.T) {
	s, p, m, a, r, instances := taskFixture(t)
	ctx := context.Background()
	action := store.TaskAction{ActorKey: m.ID.String(), ID: uuid.New(), Operation: "createTask", Body: []byte(`{"title":"通用任务"}`)}
	builds := 0
	build := func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { builds++; return r, instances, nil }
	first, err := s.CreateTask(ctx, p.ID, m.ID, action, build)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateTask(ctx, p.ID, m.ID, action, build)
	if err != nil || string(first) != string(second) || builds != 1 {
		t.Fatal("create replay not stable", err)
	}
	changed := action
	changed.Body = []byte(`{"title":"changed"}`)
	if _, err = s.CreateTask(ctx, p.ID, m.ID, changed, build); !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("action body mismatch accepted", err)
	}
	if err = s.SoftDeleteAgent(ctx, a.ID); !apperr.Is(err, apperr.CodeTaskNotIdle) {
		t.Fatal("unfinished task agent deleted", err)
	}
	if _, err = s.DeleteProject(ctx, p.ID); !apperr.Is(err, apperr.CodeTaskNotIdle) {
		t.Fatal("unfinished task project deleted", err)
	}
	if _, err = s.SetProjectManager(ctx, p.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, p.ID, m.ID, action, build); !apperr.Is(err, apperr.CodeForbidden) {
		t.Fatal("former manager replay bypassed current authorization", err)
	}
}

func TestTaskMutationsAtomicAndConcurrent(t *testing.T) {
	s, p, m, _, r, instances := taskFixture(t)
	ctx := context.Background()
	_, err := s.CreateTask(ctx, p.ID, m.ID, store.TaskAction{ActorKey: "create", ID: uuid.New(), Operation: "create", Body: []byte("body")}, func(pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) { return r, instances, nil })
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			_, e := s.MutateTask(ctx, r.View.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
				tx.Record.EdgeCounts["counter"]++
				return tx.Record.View, nil
			})
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	actual, err := s.GetTask(ctx, r.View.Task.ID)
	if err != nil || actual.EdgeCounts["counter"] != 10 || actual.View.Task.Version != 11 {
		t.Fatal("lost concurrent changes", err)
	}
	_, err = s.MutateTask(ctx, r.View.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.EdgeCounts["counter"] = 99
		return nil, errors.New("rollback")
	})
	if err == nil {
		t.Fatal("rollback error lost")
	}
	actual, _ = s.GetTask(ctx, r.View.Task.ID)
	if actual.EdgeCounts["counter"] != 10 {
		t.Fatal("failed mutation committed")
	}
	action := store.TaskAction{ActorKey: "console", ID: uuid.New(), Operation: "cancel", Body: []byte(`{}`)}
	fn := func(tx *store.TaskMutation) (any, error) {
		now := time.Now().UTC()
		tx.Record.View.Task.Status = "cancelled"
		tx.Record.View.Task.EndedAt = &now
		return tx.Record.View, nil
	}
	first, err := s.MutateTask(ctx, r.View.Task.ID, &action, fn)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.MutateTask(ctx, r.View.Task.ID, &action, func(*store.TaskMutation) (any, error) { t.Error("replayed mutation executed"); return nil, nil })
	if err != nil || string(first) != string(second) {
		t.Fatal("mutation replay changed response", err)
	}
	if _, err = s.MutateTask(ctx, r.View.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.View.Task.Status = "running"
		tx.Record.View.Task.EndedAt = nil
		return tx.Record.View, nil
	}); !apperr.Is(err, apperr.CodeTaskConflict) {
		t.Fatal("late event resurrected terminal task", err)
	}
	var detail taskmodel.Detail
	if json.Unmarshal(first, &detail) != nil || detail.Task.Version != 12 {
		t.Fatal("response version out of sync")
	}
	events, err := s.TaskEvents(ctx, &r.View.Task.ID, nil, 0, 100)
	if err != nil || len(events) != 12 {
		t.Fatal("events not committed with mutations", len(events), err)
	}
	page, err := s.ListTasks(ctx, p.ID, "", 1)
	if err != nil || len(page.Tasks) != 1 {
		t.Fatal("task list failed", err)
	}
}
