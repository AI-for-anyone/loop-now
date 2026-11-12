package httpapi_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

// GET /projects/{projectId}/tasks/counts：未结束的任务按状态计数，响应经契约校验；项目不存在或已删除时 404。
func TestCountProjectTasks(t *testing.T) {
	e := newEnv(t)
	e.api.Tasks = &tasks.Service{Store: e.st, Options: tasks.Options{MaxRequest: 1 << 20}}
	path := "/projects/" + e.dc.Project.ID.String() + "/tasks/counts"
	if got := decode[taskmodel.Counts](t, e.expect(200, "GET", path, nil)); got != (taskmodel.Counts{}) {
		t.Fatalf("没有任务时应全为 0：%+v", got)
	}
	for _, status := range []string{"running", "running", "blocked", "waiting", "preparing", "completed", "cancelled"} {
		doc, _ := json.Marshal(map[string]any{"view": map[string]any{"task": map[string]any{"status": status, "version": 1}}})
		var ended *time.Time
		if status == "completed" || status == "cancelled" {
			now := time.Now()
			ended = &now
		}
		if _, err := e.st.Pool.Exec(t.Context(), `INSERT INTO task_records(id,project_id,status,document,version,ended_at) VALUES($1,$2,$3,$4,1,$5)`, uuid.New(), e.dc.Project.ID, status, json.RawMessage(doc), ended); err != nil {
			t.Fatal(err)
		}
	}
	if got := decode[taskmodel.Counts](t, e.expect(200, "GET", path, nil)); got != (taskmodel.Counts{Preparing: 1, Running: 2, Waiting: 1, Blocked: 1}) {
		t.Fatalf("计数 = %+v", got)
	}
	if bad := decode[apitypes.ErrorBody](t, e.expect(404, "GET", "/projects/"+uuid.New().String()+"/tasks/counts", nil)); bad.Error.Code != "not_found" {
		t.Fatalf("不存在的项目：%+v", bad)
	}
	gone, err := e.st.CreateProject(t.Context(), e.dc.Organization.ID, "已删除")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Pool.Exec(t.Context(), `UPDATE projects SET deleted_at=now() WHERE id=$1`, gone.ID); err != nil {
		t.Fatal(err)
	}
	e.expect(404, "GET", "/projects/"+gone.ID.String()+"/tasks/counts", nil)
}
