package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskmodel"
)

type TaskInstance struct {
	ID, TaskID, AgentID uuid.UUID
	AgentName           string
	Snapshot            []byte
	Sources             []TaskFrozenSource
	SessionFile         *string
	SessionState        string
	ForkSource          *string
	Prepared, Retired   bool
}
type TaskFrozenSource struct {
	Source FolderSource
	Files  []SyncFile
}
type TaskAction struct {
	ActorKey  string
	ID        uuid.UUID
	Operation string
	Body      []byte
	Authorize func(*TaskMutation) error
}
type TaskEvent struct {
	ID                int64
	TaskID, ProjectID uuid.UUID
	Type              string
	Data              json.RawMessage
	CreatedAt         time.Time
}
type TaskMutation struct {
	Tx        pgx.Tx
	Record    *taskmodel.Record
	ManagerID *uuid.UUID
	events    []TaskEvent
	unchanged bool
}

// Unchanged 表示仅检查/读状态，不产生版本和通知风暴。
func (m *TaskMutation) Unchanged() { m.unchanged = true }
func (m *TaskMutation) Changed()   { m.unchanged = false }

func (m *TaskMutation) Emit(kind string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	m.events = append(m.events, TaskEvent{TaskID: m.Record.View.Task.ID, ProjectID: m.Record.View.Task.ProjectID, Type: kind, Data: b})
	return nil
}

func actionLock(ctx context.Context, tx pgx.Tx, a TaskAction) (json.RawMessage, bool, error) {
	if a.ID == uuid.Nil || a.ActorKey == "" || a.Operation == "" {
		return nil, false, apperr.Invalid("actionId", "必须提供有效动作ID")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, a.ActorKey+":"+a.ID.String()); err != nil {
		return nil, false, err
	}
	var op string
	var hash, body []byte
	err := tx.QueryRow(ctx, `SELECT operation,body_hash,response FROM task_actions WHERE actor_key=$1 AND action_id=$2`, a.ActorKey, a.ID).Scan(&op, &hash, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	want := sha256.Sum256(a.Body)
	if op != a.Operation || string(hash) != string(want[:]) {
		return nil, false, apperr.Conflict(apperr.CodeTaskConflict, "动作ID已用于其他请求")
	}
	return body, true, nil
}
func recordAction(ctx context.Context, tx pgx.Tx, a TaskAction, taskID uuid.UUID, value any) (json.RawMessage, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(a.Body)
	var stored []byte
	err = tx.QueryRow(ctx, `INSERT INTO task_actions(actor_key,action_id,operation,task_id,body_hash,response) VALUES($1,$2,$3,$4,$5,$6) RETURNING response`, a.ActorKey, a.ID, a.Operation, taskID, hash[:], b).Scan(&stored)
	return stored, err
}
func saveTask(ctx context.Context, tx pgx.Tx, r *taskmodel.Record) error {
	r.View.Task.Version++
	r.View.Task.UpdatedAt = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	var project uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE task_records SET status=$2,document=$3,version=$4,updated_at=$5,ended_at=$6 WHERE id=$1 RETURNING project_id`, r.View.Task.ID, r.View.Task.Status, b, r.View.Task.Version, r.View.Task.UpdatedAt, r.View.Task.EndedAt).Scan(&project)
	if err != nil || r.View.Task.EndedAt == nil {
		return err
	}
	// Every terminal path saves here: wipe what deleted accounts kept for it.
	return settleProjectWipesTx(ctx, tx, project)
}
func emitTaskEvents(ctx context.Context, tx pgx.Tx, m *TaskMutation) error {
	// 项目事件跨任务续传：序号分配与事务提交必须串行，否则后提交的小序号会被游标跳过。
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x6c6e5f7461736b65)); err != nil {
		return err
	}
	if err := m.Emit("task.updated", map[string]any{"task": m.Record.View.Task}); err != nil {
		return err
	}
	for _, e := range m.events {
		if e.Type == "sleep.updated" {
			var data map[string]any
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return err
			}
			data["taskVersion"] = m.Record.View.Task.Version
			var err error
			e.Data, err = json.Marshal(data)
			if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO task_events(task_id,project_id,type,data) VALUES($1,$2,$3,$4)`, e.TaskID, e.ProjectID, e.Type, e.Data); err != nil {
			return err
		}
	}
	return nil
}

// emitTaskPluginEventsTx 比较任务前后两份文档（新建时 before 为空），为任务与节点的状态变化写插件的观察类事件。
// task.started 只在第一次从 preparing 进入 running 时写；node.started 在节点新出现或从 queued 进入 running 时写。
func emitTaskPluginEventsTx(ctx context.Context, tx pgx.Tx, before, after *taskmodel.Record) error {
	t := after.View.Task
	project, task := t.ProjectID, t.ID
	old := ""
	if before != nil {
		old = before.View.Task.Status
	}
	if old != t.Status {
		typ := map[string]string{"waiting": "task.waiting", "blocked": "task.blocked", "completed": "task.completed", "cancelled": "task.cancelled"}[t.Status]
		if t.Status == "running" && (old == "" || old == "preparing") {
			typ = "task.started"
		}
		if typ != "" {
			if err := emitPluginEventTx(ctx, tx, typ, &project, t.CurrentAgentID, &task, nil, map[string]any{"title": t.Title, "reason": t.Reason}); err != nil {
				return err
			}
		}
	}
	was := map[uuid.UUID]string{}
	if before != nil {
		for _, n := range before.View.Nodes {
			was[n.ID] = n.Status
		}
	}
	for _, n := range after.View.Nodes {
		prev, existed := was[n.ID]
		if existed && prev == n.Status {
			continue
		}
		typ := map[string]string{"submitted": "node.submitted", "blocked": "node.blocked"}[n.Status]
		if n.Status == "running" && (!existed || prev == "queued") {
			typ = "node.started"
		}
		if typ == "" {
			continue
		}
		agent := n.AgentID
		var run *uuid.UUID
		if len(n.RunIDs) > 0 {
			run = &n.RunIDs[len(n.RunIDs)-1]
		}
		if err := emitPluginEventTx(ctx, tx, typ, &project, &agent, &task, run, map[string]any{"nodeRunId": n.ID, "stepId": n.StepID, "reason": n.Reason}); err != nil {
			return err
		}
	}
	return nil
}

// TaskActionRecorded 报告这个身份的动作 ID 是否已经执行过（重放时直接返回原结果，不再询问插件）。
func (s *Store) TaskActionRecorded(ctx context.Context, actorKey string, id uuid.UUID) (bool, error) {
	var found bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_actions WHERE actor_key=$1 AND action_id=$2)`, actorKey, id).Scan(&found)
	return found, err
}

// MutateTask 锁顺序：动作幂等锁 → 项目SHARE → 任务UPDATE。函数内不得倒锁项目/Agent。
func (s *Store) MutateTask(ctx context.Context, id uuid.UUID, action *TaskAction, fn func(*TaskMutation) (any, error)) (json.RawMessage, error) {
	var out json.RawMessage
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var cached json.RawMessage
		var replay bool
		if action != nil {
			bound := *action
			bound.Body = append(append([]byte(nil), action.Body...), []byte(id.String())...)
			action = &bound
			b, found, err := actionLock(ctx, tx, bound)
			if err != nil {
				return err
			}
			if found {
				cached, replay = b, true
			}
		}
		var pid uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, id).Scan(&pid); err != nil {
			return notFoundIfNoRows(err, "任务")
		}
		var manager *uuid.UUID
		var projectName string
		if err := tx.QueryRow(ctx, `SELECT manager_agent_id,name FROM projects WHERE id=$1 FOR SHARE`, pid).Scan(&manager, &projectName); err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT document FROM task_records WHERE id=$1 FOR UPDATE`, id).Scan(&raw); err != nil {
			return notFoundIfNoRows(err, "任务")
		}
		var record, before taskmodel.Record
		if err := decodeTaskJSON(raw, &record); err != nil {
			return err
		}
		if err := decodeTaskJSON(raw, &before); err != nil {
			return err
		}
		record.View.Task.Link = taskLink(projectName, id)
		m := &TaskMutation{Tx: tx, Record: &record, ManagerID: manager}
		originalStatus := record.View.Task.Status
		if action != nil && action.Authorize != nil {
			if err := action.Authorize(m); err != nil {
				return err
			}
		}
		if replay {
			out = cached
			return nil
		}
		value, err := fn(m)
		if err != nil {
			return err
		}
		if (originalStatus == "cancelled" || originalStatus == "completed") && record.View.Task.Status != originalStatus {
			return apperr.Conflict(apperr.CodeTaskConflict, "已结束任务不能恢复执行")
		}
		if !m.unchanged {
			if err := s.ExpireTaskNodeInboxTx(ctx, m); err != nil {
				return err
			}
			if err := s.ValidateTaskSleepsTx(ctx, m); err != nil {
				return err
			}
			if err := s.emitTaskNodeQueuesTx(ctx, m); err != nil {
				return err
			}
			if err = saveTask(ctx, tx, &record); err != nil {
				return err
			}
			if err = emitTaskEvents(ctx, tx, m); err != nil {
				return err
			}
			if err = emitTaskPluginEventsTx(ctx, tx, &before, &record); err != nil {
				return err
			}
			changed = true
		}
		// 返回视图必须在version/time更新后编码，避免幂等响应里的版本落后一轮。
		switch value.(type) {
		case taskmodel.Detail, *taskmodel.Detail:
			projected := record.View
			if err = s.ProjectTaskSleepsTx(ctx, tx, &projected); err != nil {
				return err
			}
			value = projected
		}
		if action != nil {
			out, err = recordAction(ctx, tx, *action, id, value)
		} else {
			out, err = json.Marshal(value)
		}
		return err
	})
	if err == nil && changed && s.TaskCommitted != nil {
		s.TaskCommitted()
	}
	return out, err
}

func (s *Store) CreateTask(ctx context.Context, projectID, managerID uuid.UUID, action TaskAction, build func(pgx.Tx) (taskmodel.Record, []TaskInstance, error)) (json.RawMessage, error) {
	var out json.RawMessage
	created := false
	action.Body = append(append([]byte(nil), action.Body...), []byte(projectID.String())...)
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		b, found, err := actionLock(ctx, tx, action)
		if err != nil {
			return err
		}
		var current *uuid.UUID
		var projectName string
		if err = tx.QueryRow(ctx, `SELECT manager_agent_id,name FROM projects WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, projectID).Scan(&current, &projectName); err != nil {
			return notFoundIfNoRows(err, "项目")
		}
		if current == nil || *current != managerID {
			return apperr.Forbidden("只有当前项目Manager可创建任务")
		}
		if found {
			out = b
			return nil
		}
		r, instances, err := build(tx)
		if err != nil {
			return err
		}
		r.View.Task.ProjectID = projectID
		r.View.Task.Link = taskLink(projectName, r.View.Task.ID)
		if r.View.Task.Status != "preparing" || r.View.Task.Version != 1 {
			return apperr.Invalid("task", "任务初始状态无效")
		}
		encoded, err := json.Marshal(r)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_records(id,project_id,status,document,version,created_at,updated_at) VALUES($1,$2,$3,$4,1,$5,$5)`, r.View.Task.ID, projectID, r.View.Task.Status, encoded, r.View.Task.CreatedAt)
		if err != nil {
			return err
		}
		for _, inst := range instances {
			sources, err := json.Marshal(inst.Sources)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,sources,fork_source) VALUES($1,$2,$3,$4,$5,encode(sha256($5),'hex'),$6,$7)`, inst.ID, r.View.Task.ID, inst.AgentID, inst.AgentName, inst.Snapshot, sources, inst.ForkSource)
			if err != nil {
				return err
			}
		}
		m := &TaskMutation{Tx: tx, Record: &r, ManagerID: current}
		if err = emitTaskEvents(ctx, tx, m); err != nil {
			return err
		}
		if err = emitTaskPluginEventsTx(ctx, tx, nil, &r); err != nil {
			return err
		}
		out, err = recordAction(ctx, tx, action, r.View.Task.ID, r.View)
		created = true
		return err
	})
	if err == nil && created && s.TaskCommitted != nil {
		s.TaskCommitted()
	}
	return out, err
}

func taskLink(project string, id uuid.UUID) string {
	return "/p/" + url.PathEscape(project) + "/tasks/" + id.String()
}

func (s *Store) GetTask(ctx context.Context, id uuid.UUID) (taskmodel.Record, error) {
	var r taskmodel.Record
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return r, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	var name string
	if err = tx.QueryRow(ctx, `SELECT t.document,p.name FROM task_records t JOIN projects p ON p.id=t.project_id WHERE t.id=$1`, id).Scan(&raw, &name); err != nil {
		return r, notFoundIfNoRows(err, "任务")
	}
	if err = decodeTaskJSON(raw, &r); err != nil {
		return r, err
	}
	r.View.Task.Link = taskLink(name, id)
	if err = s.ProjectTaskSleepsTx(ctx, tx, &r.View); err != nil {
		return r, err
	}
	return r, tx.Commit(ctx)
}

func taskCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}
func parseTaskCursor(cursor string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, apperr.Invalid("cursor", "游标无效")
	}
	for i, c := range b {
		if c == '|' {
			t, e := time.Parse(time.RFC3339Nano, string(b[:i]))
			id, e2 := uuid.Parse(string(b[i+1:]))
			if e == nil && e2 == nil {
				return t, id, nil
			}
			break
		}
	}
	return time.Time{}, uuid.Nil, apperr.Invalid("cursor", "游标无效")
}
func (s *Store) ListTasks(ctx context.Context, projectID uuid.UUID, cursor string, limit int) (taskmodel.Page, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return taskmodel.Page{}, err
	}
	var at *time.Time
	var after *uuid.UUID
	if cursor != "" {
		t, id, err := parseTaskCursor(cursor)
		if err != nil {
			return taskmodel.Page{}, err
		}
		at, after = &t, &id
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.document,p.name FROM task_records t JOIN projects p ON p.id=t.project_id WHERE t.project_id=$1 AND ($2::timestamptz IS NULL OR (t.created_at,t.id)<($2,$3)) ORDER BY t.created_at DESC,t.id DESC LIMIT $4`, projectID, at, after, limit+1)
	if err != nil {
		return taskmodel.Page{}, err
	}
	defer rows.Close()
	out := taskmodel.Page{Tasks: []taskmodel.Summary{}}
	for rows.Next() {
		var raw []byte
		var name string
		if err = rows.Scan(&raw, &name); err != nil {
			return out, err
		}
		var r taskmodel.Record
		if err = decodeTaskJSON(raw, &r); err != nil {
			return out, err
		}
		r.View.Task.Link = taskLink(name, r.View.Task.ID)
		out.Tasks = append(out.Tasks, r.View.Task)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Tasks) > limit {
		out.Tasks = out.Tasks[:limit]
		last := out.Tasks[len(out.Tasks)-1]
		c := taskCursor(last.CreatedAt, last.ID)
		out.NextCursor = &c
	}
	return out, nil
}

func (s *Store) TaskInstance(ctx context.Context, id uuid.UUID) (TaskInstance, error) {
	var i TaskInstance
	var raw []byte
	err := s.Pool.QueryRow(ctx, `SELECT id,task_id,agent_id,agent_name,snapshot,sources,session_file,session_state,fork_source,prepared,retired FROM task_instances WHERE id=$1`, id).Scan(&i.ID, &i.TaskID, &i.AgentID, &i.AgentName, &i.Snapshot, &raw, &i.SessionFile, &i.SessionState, &i.ForkSource, &i.Prepared, &i.Retired)
	if err != nil {
		return i, notFoundIfNoRows(err, "任务实例")
	}
	err = decodeTaskJSON(raw, &i.Sources)
	return i, err
}
func (s *Store) TaskInstances(ctx context.Context, taskID uuid.UUID) ([]TaskInstance, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM task_instances WHERE task_id=$1 ORDER BY agent_id`, taskID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := []TaskInstance{}
	for _, id := range ids {
		i, e := s.TaskInstance(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, nil
}
func (s *Store) TaskIDs(ctx context.Context, statuses []string) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM task_records WHERE status=ANY($1) ORDER BY created_at,id`, statuses)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

func (s *Store) TaskEvents(ctx context.Context, taskID, projectID *uuid.UUID, after int64, limit int) ([]TaskEvent, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,task_id,project_id,type,data,created_at FROM task_events WHERE ($1::uuid IS NULL OR task_id=$1) AND ($2::uuid IS NULL OR project_id=$2) AND id>$3 ORDER BY id LIMIT $4`, taskID, projectID, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskEvent, error) {
		var e TaskEvent
		err := r.Scan(&e.ID, &e.TaskID, &e.ProjectID, &e.Type, &e.Data, &e.CreatedAt)
		return e, err
	})
}
func (s *Store) TaskEventPosition(ctx context.Context, taskID, projectID *uuid.UUID) (int64, error) {
	var pos int64
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(max(id),0) FROM task_events WHERE ($1::uuid IS NULL OR task_id=$1) AND ($2::uuid IS NULL OR project_id=$2)`, taskID, projectID).Scan(&pos)
	return pos, err
}
func TaskEventID(e TaskEvent) string { return strconv.FormatInt(e.ID, 10) }

func decodeTaskJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}
func encodeTaskJSON(value any) ([]byte, error) { return json.Marshal(value) }

// TaskSnapshot 锁住任务聚合读取快照与事件检查点，避免两次独立查询漏掉并发更新。
func (s *Store) TaskSnapshot(ctx context.Context, id uuid.UUID) (taskmodel.Detail, int64, error) {
	var detail taskmodel.Detail
	var position int64
	_, err := s.MutateTask(ctx, id, nil, func(m *TaskMutation) (any, error) {
		m.Unchanged()
		detail = m.Record.View
		if err := s.ProjectTaskSleepsTx(ctx, m.Tx, &detail); err != nil {
			return nil, err
		}
		return nil, m.Tx.QueryRow(ctx, `SELECT coalesce(max(id),0) FROM task_events WHERE task_id=$1`, id).Scan(&position)
	})
	return detail, position, err
}
