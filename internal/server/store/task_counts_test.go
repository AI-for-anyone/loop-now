package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/testutil/sqlitetest"
)

// CountTasks 只数未结束的任务，按状态分组；别的项目、已结束的任务不计入；项目不存在或已删除时 404。
func TestCountTasks(t *testing.T) {
	ctx := context.Background()
	s := sqlitetest.NewStore(t)
	dc, err := s.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateProject(ctx, dc.Organization.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	insert := func(project uuid.UUID, status string) {
		t.Helper()
		doc, _ := json.Marshal(map[string]any{"view": map[string]any{"task": map[string]any{"status": status, "version": 1}}})
		var ended *time.Time
		if status == "completed" || status == "cancelled" {
			now := time.Now()
			ended = &now
		}
		mustExec(t, s, `INSERT INTO task_records(id,project_id,status,document,version,ended_at) VALUES($1,$2,$3,$4,1,$5)`, uuid.New(), project, status, json.RawMessage(doc), ended)
	}

	if got, err := s.CountTasks(ctx, dc.Project.ID); err != nil || got != (taskmodel.Counts{}) {
		t.Fatalf("没有任务时应全为 0：%+v %v", got, err)
	}
	for status, n := range map[string]int{"preparing": 1, "running": 3, "waiting": 2, "completed": 4, "cancelled": 1} {
		for range n {
			insert(dc.Project.ID, status)
		}
	}
	insert(other.ID, "blocked")
	insert(other.ID, "running")
	got, err := s.CountTasks(ctx, dc.Project.ID)
	if err != nil || got != (taskmodel.Counts{Preparing: 1, Running: 3, Waiting: 2, Blocked: 0}) {
		t.Fatalf("计数 = %+v %v", got, err)
	}
	if got, err := s.CountTasks(ctx, other.ID); err != nil || got != (taskmodel.Counts{Running: 1, Blocked: 1}) {
		t.Fatalf("另一个项目 = %+v %v", got, err)
	}

	if _, err := s.CountTasks(ctx, uuid.New()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("不存在的项目应 404：%v", err)
	}
	mustExec(t, s, `UPDATE projects SET deleted_at=now() WHERE id=$1`, other.ID)
	if _, err := s.CountTasks(ctx, other.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("已删除的项目应 404：%v", err)
	}
}

// 计数走部分索引 task_records_unfinished，不扫已结束的任务。
func TestCountTasksUsesUnfinishedIndex(t *testing.T) {
	s := sqlitetest.NewStore(t)
	rows, err := s.Pool.Query(context.Background(), `EXPLAIN QUERY PLAN SELECT status, count(*) FROM task_records WHERE project_id=$1 AND ended_at IS NULL GROUP BY status`, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int64
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if text := strings.Join(plan, "\n"); !strings.Contains(text, "USING INDEX task_records_unfinished") {
		t.Fatalf("应使用 task_records_unfinished：\n%s", text)
	}
}
