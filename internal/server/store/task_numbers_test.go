package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// Task numbers start at 1 per project and are assigned at creation: concurrent creations never get the same number, a
// failed creation takes no number, replaying the same action gets the original number back, and a number is not reused
// after its task is deleted; later changes keep the number and cannot change it.
func TestTaskNumbersPerProject(t *testing.T) {
	s, p, m, a, template, _ := taskFixture(t)
	ctx := context.Background()
	other, err := s.CreateProject(ctx, p.OrganizationID, "Another project")
	if err != nil {
		t.Fatal(err)
	}
	om, err := s.CreateAgent(ctx, store.ProjectScope(other.ID), "Manager", "example", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	oa, err := s.CreateAgent(ctx, store.ProjectScope(other.ID), "executor", "example", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProjectManager(ctx, other.ID, om.ID); err != nil {
		t.Fatal(err)
	}

	create := func(project uuid.UUID, manager, agent store.Agent, action store.TaskAction) (taskmodel.Detail, error) {
		r := template
		r.View.Task.ID, r.View.Task.ProjectID = uuid.New(), project
		r.View.Workflow.Steps = slices.Clone(r.View.Workflow.Steps)
		r.View.Workflow.Steps[0].AgentID = agent.ID
		instances := []store.TaskInstance{{ID: uuid.New(), TaskID: r.View.Task.ID, AgentID: agent.ID, AgentName: agent.Name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}}}
		out, err := s.CreateTask(ctx, project, manager.ID, action, func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) { return r, instances, nil })
		if err != nil {
			return taskmodel.Detail{}, err
		}
		var d taskmodel.Detail
		err = json.Unmarshal(out, &d)
		return d, err
	}
	newAction := func() store.TaskAction {
		return store.TaskAction{ActorKey: "manager", ID: uuid.New(), Operation: "createTask", Body: []byte(`{}`)}
	}
	mustCreate := func(project uuid.UUID, manager, agent store.Agent) taskmodel.Detail {
		t.Helper()
		d, err := create(project, manager, agent, newAction())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	first := mustCreate(p.ID, m, a)
	replayAction := newAction()
	second, err := create(p.ID, m, a, replayAction)
	if err != nil {
		t.Fatal(err)
	}
	if first.Task.Number != 1 || second.Task.Number != 2 {
		t.Fatalf("numbers should be assigned in order from 1: %d %d", first.Task.Number, second.Task.Number)
	}
	if got := mustCreate(other.ID, om, oa); got.Task.Number != 1 {
		t.Fatalf("another project should start at 1: %d", got.Task.Number)
	}
	if replay, err := create(p.ID, m, a, replayAction); err != nil || replay.Task.ID != second.Task.ID || replay.Task.Number != 2 {
		t.Fatalf("replaying the same action should return the original task and number: %+v %v", replay.Task, err)
	}
	if _, err := s.CreateTask(ctx, p.ID, m.ID, newAction(), func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return taskmodel.Record{}, nil, errors.New("build failed")
	}); err == nil {
		t.Fatal("a creation whose build fails should return an error")
	}
	if got := mustCreate(p.ID, m, a); got.Task.Number != 3 {
		t.Fatalf("replays and failed creations should not take a number: %d", got.Task.Number)
	}

	var wg sync.WaitGroup
	numbers := make(chan int64, 8)
	for range 8 {
		wg.Go(func() {
			d, err := create(p.ID, m, a, newAction())
			if err != nil {
				t.Error(err)
				return
			}
			numbers <- d.Task.Number
		})
	}
	wg.Wait()
	close(numbers)
	var concurrent []int64
	for n := range numbers {
		concurrent = append(concurrent, n)
	}
	slices.Sort(concurrent)
	if !slices.Equal(concurrent, []int64{4, 5, 6, 7, 8, 9, 10, 11}) {
		t.Fatalf("concurrent creations should get distinct consecutive numbers: %v", concurrent)
	}

	// Delete the task with the largest number (there is currently no way to delete a task, so the row is deleted
	// directly); the next number still does not reuse it.
	var last uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM task_records WHERE project_id=$1 AND number=11`, p.ID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM task_events WHERE task_id=$1`, `DELETE FROM task_actions WHERE task_id=$1`, `DELETE FROM task_instances WHERE task_id=$1`, `DELETE FROM task_records WHERE id=$1`} {
		mustExec(t, s, q, last)
	}
	if got := mustCreate(p.ID, m, a); got.Task.Number != 12 {
		t.Fatalf("the number of a deleted task should not be reused: %d", got.Task.Number)
	}

	if _, err := s.MutateTask(ctx, first.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.EdgeCounts["visits"]++
		return tx.Record.View, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTask(ctx, first.Task.ID); err != nil || got.View.Task.Number != 1 || got.View.Task.Version != 2 {
		t.Fatalf("the number should be kept after a change: %+v %v", got.View.Task, err)
	}
	if _, err := s.MutateTask(ctx, first.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.View.Task.Number = 99
		return nil, nil
	}); !db.IsRaise(err, "task_number_is_immutable") {
		t.Fatalf("changing the number should be rejected: %v", err)
	}
	page, err := s.ListTasks(ctx, p.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var listed []int64
	for _, task := range page.Tasks {
		listed = append(listed, task.Number)
	}
	slices.Sort(listed) // tasks in the test have the same creation time, so the list order is undefined
	if !slices.Equal(listed, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12}) {
		t.Fatalf("the list should carry numbers: %v", listed)
	}
}
