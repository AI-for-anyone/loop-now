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

// 任务编号按项目从 1 开始，创建时分配：并发创建不重号，失败的创建不占号，重放同一动作拿回原来的号，
// 任务被删掉之后号也不重用；之后的改动保留编号，也不能改掉它。
func TestTaskNumbersPerProject(t *testing.T) {
	s, p, m, a, template, _ := taskFixture(t)
	ctx := context.Background()
	other, err := s.CreateProject(ctx, p.OrganizationID, "另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	om, err := s.CreateAgent(ctx, store.ProjectScope(other.ID), "Manager", "example", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	oa, err := s.CreateAgent(ctx, store.ProjectScope(other.ID), "执行者", "example", "model", "")
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
		t.Fatalf("编号应从 1 起依次分配：%d %d", first.Task.Number, second.Task.Number)
	}
	if got := mustCreate(other.ID, om, oa); got.Task.Number != 1 {
		t.Fatalf("另一个项目应从 1 开始：%d", got.Task.Number)
	}
	if replay, err := create(p.ID, m, a, replayAction); err != nil || replay.Task.ID != second.Task.ID || replay.Task.Number != 2 {
		t.Fatalf("重放同一动作应拿回原来的任务和编号：%+v %v", replay.Task, err)
	}
	if _, err := s.CreateTask(ctx, p.ID, m.ID, newAction(), func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return taskmodel.Record{}, nil, errors.New("build failed")
	}); err == nil {
		t.Fatal("构造失败的创建应报错")
	}
	if got := mustCreate(p.ID, m, a); got.Task.Number != 3 {
		t.Fatalf("重放与失败的创建不应占号：%d", got.Task.Number)
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
		t.Fatalf("并发创建应拿到各不相同的连续编号：%v", concurrent)
	}

	// 删掉编号最大的任务（现在没有删任务的入口，直接删行），下一个编号也不重用它。
	var last uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM task_records WHERE project_id=$1 AND number=11`, p.ID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM task_events WHERE task_id=$1`, `DELETE FROM task_actions WHERE task_id=$1`, `DELETE FROM task_instances WHERE task_id=$1`, `DELETE FROM task_records WHERE id=$1`} {
		mustExec(t, s, q, last)
	}
	if got := mustCreate(p.ID, m, a); got.Task.Number != 12 {
		t.Fatalf("删掉的任务的编号不应重用：%d", got.Task.Number)
	}

	if _, err := s.MutateTask(ctx, first.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.EdgeCounts["visits"]++
		return tx.Record.View, nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTask(ctx, first.Task.ID); err != nil || got.View.Task.Number != 1 || got.View.Task.Version != 2 {
		t.Fatalf("改动后编号应保留：%+v %v", got.View.Task, err)
	}
	if _, err := s.MutateTask(ctx, first.Task.ID, nil, func(tx *store.TaskMutation) (any, error) {
		tx.Record.View.Task.Number = 99
		return nil, nil
	}); !db.IsRaise(err, "task_number_is_immutable") {
		t.Fatalf("改编号应被拒绝：%v", err)
	}
	page, err := s.ListTasks(ctx, p.ID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var listed []int64
	for _, task := range page.Tasks {
		listed = append(listed, task.Number)
	}
	slices.Sort(listed) // 测试里的任务创建时间相同，列表顺序不定
	if !slices.Equal(listed, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12}) {
		t.Fatalf("列表应带编号：%v", listed)
	}
}
