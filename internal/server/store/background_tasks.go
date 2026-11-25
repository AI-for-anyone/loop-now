package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// BackgroundTask is work a runtime's CLI started in the background during a reply that outlives the reply (a command, a
// sub-agent), kept in background_tasks. AgentID is the executor's Agent: the regular Agent, or the task instance the
// task ran on. It belongs to the run (RunID) or task turn (TaskTurnID) that started it.
type BackgroundTask struct {
	Key             uuid.UUID // the record's id, made by the executor when the task started
	AgentID         uuid.UUID
	RunID           *uuid.UUID
	TaskTurnID      *uuid.UUID
	NativeID        string // the runtime's own id of the task
	Runtime         string
	Kind            string // command / subagent / other
	Label           string
	ToolCallID      *string
	StartedAt       time.Time
	Status          string // running / completed / failed / stopped / unknown
	EndedAt         *time.Time
	ExitCode        *int64
	StopReason      *string
	StopRequestedAt *time.Time
	Output          *string
	FollowUpRunID   *uuid.UUID // the reply the CLI gave by itself after the task ended
}

const backgroundTaskColumns = `id, agent_id, run_id, task_turn_id, native_id, runtime, kind, label, tool_call_id, started_at, status, ended_at,
	exit_code, stop_reason, stop_requested_at, output, follow_up_run_id`

func scanBackgroundTask(row db.Scanner) (BackgroundTask, error) {
	var t BackgroundTask
	err := row.Scan(&t.Key, &t.AgentID, &t.RunID, &t.TaskTurnID, &t.NativeID, &t.Runtime, &t.Kind, &t.Label, &t.ToolCallID, &t.StartedAt,
		&t.Status, &t.EndedAt, &t.ExitCode, &t.StopReason, &t.StopRequestedAt, &t.Output, &t.FollowUpRunID)
	return t, err
}

// SaveBackgroundTask writes a task's state, by Key: the first write inserts it, later ones update it. Only the first
// recorded end counts: once the task has ended, later writes change nothing (a stop and the task's own end can race).
// A stop asked for is kept while the task runs.
func (s *Store) SaveBackgroundTask(ctx context.Context, t BackgroundTask) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO background_tasks (`+backgroundTaskColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, NULL)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, ended_at = excluded.ended_at, exit_code = excluded.exit_code,
			stop_reason = excluded.stop_reason, output = excluded.output,
			stop_requested_at = coalesce(background_tasks.stop_requested_at, excluded.stop_requested_at)
		WHERE background_tasks.status = 'running'`,
		t.Key, t.AgentID, t.RunID, t.TaskTurnID, t.NativeID, t.Runtime, t.Kind, t.Label, t.ToolCallID, t.StartedAt, t.Status, t.EndedAt,
		t.ExitCode, t.StopReason, t.StopRequestedAt, t.Output)
	return err
}

func (s *Store) backgroundTasks(ctx context.Context, where string, args ...any) ([]BackgroundTask, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+backgroundTaskColumns+` FROM background_tasks WHERE `+where+` ORDER BY started_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackgroundTask
	for rows.Next() {
		t, err := scanBackgroundTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RunningBackgroundTasks returns the tasks still running on the Agent's runtime process (agentID is the executor's
// Agent: a regular Agent or a task instance), oldest first.
func (s *Store) RunningBackgroundTasks(ctx context.Context, agentID uuid.UUID) ([]BackgroundTask, error) {
	return s.backgroundTasks(ctx, `agent_id = $1 AND status = 'running'`, agentID)
}

// RunningNodeBackgroundTasks returns the running tasks that turns of a task node started, oldest first.
func (s *Store) RunningNodeBackgroundTasks(ctx context.Context, nodeRunID uuid.UUID) ([]BackgroundTask, error) {
	return s.backgroundTasks(ctx, `status = 'running' AND task_turn_id IN (SELECT id FROM task_turns WHERE node_run_id = $1)`, nodeRunID)
}

// TaskTurnBackgroundTasks returns the tasks a task turn started, oldest first.
func (s *Store) TaskTurnBackgroundTasks(ctx context.Context, turnID uuid.UUID) ([]BackgroundTask, error) {
	return s.backgroundTasks(ctx, `task_turn_id = $1`, turnID)
}

// TaskTurnBackgroundTasksOfNode returns the tasks with the runtime's id nativeID that turns of a task node started,
// oldest first.
func (s *Store) TaskTurnBackgroundTasksOfNode(ctx context.Context, nodeRunID uuid.UUID, nativeID string) ([]BackgroundTask, error) {
	return s.backgroundTasks(ctx, `native_id = $2 AND task_turn_id IN (SELECT id FROM task_turns WHERE node_run_id = $1)`, nodeRunID, nativeID)
}

// RunningBackgroundCounts returns how many tasks run for each of the given Agents; Agents with none are left out.
func (s *Store) RunningBackgroundCounts(ctx context.Context, agentIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	out := map[uuid.UUID]int{}
	if len(agentIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT agent_id, count(*) FROM background_tasks WHERE status = 'running' AND agent_id IN (SELECT value FROM json_each($1)) GROUP BY agent_id`, agentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// BackgroundTaskCount returns how many background tasks run on the Agent's runtime process.
func (s *Store) BackgroundTaskCount(ctx context.Context, agentID uuid.UUID) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM background_tasks WHERE agent_id = $1 AND status = 'running'`, agentID).Scan(&n)
	return n, err
}

// BackgroundTaskByKey returns one task by its record's id.
func (s *Store) BackgroundTaskByKey(ctx context.Context, key uuid.UUID) (BackgroundTask, error) {
	t, err := scanBackgroundTask(s.Pool.QueryRow(ctx, `SELECT `+backgroundTaskColumns+` FROM background_tasks WHERE id = $1`, key))
	return t, notFoundIfNoRows(err, "Background task")
}

// LatestBackgroundTask returns the Agent's most recent task with the runtime's id nativeID; not found when there is none.
func (s *Store) LatestBackgroundTask(ctx context.Context, agentID uuid.UUID, nativeID string) (BackgroundTask, error) {
	t, err := scanBackgroundTask(s.Pool.QueryRow(ctx, `SELECT `+backgroundTaskColumns+` FROM background_tasks
		WHERE agent_id = $1 AND native_id = $2 ORDER BY started_at DESC, id DESC LIMIT 1`, agentID, nativeID))
	return t, notFoundIfNoRows(err, "Background task")
}

// LinkFollowUp records that the run runID is the reply the CLI gave by itself after the tasks keys ended.
func (s *Store) LinkFollowUp(ctx context.Context, runID uuid.UUID, keys []uuid.UUID) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `UPDATE background_tasks SET follow_up_run_id = $1 WHERE id IN (SELECT value FROM json_each($2))`, runID, keys)
	return err
}

// MarkBackgroundTasksUnknown runs at server startup: no runtime process of an earlier server is followed any more, so
// every task still recorded as running becomes unknown, ended at at. It returns those tasks.
func (s *Store) MarkBackgroundTasksUnknown(ctx context.Context, at time.Time) ([]BackgroundTask, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE background_tasks SET status = 'unknown', ended_at = $1 WHERE status = 'running' RETURNING `+backgroundTaskColumns, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackgroundTask
	for rows.Next() {
		t, err := scanBackgroundTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// backgroundFor returns, for each run, the tasks it started and (when it is a follow-up) the tasks whose end it
// followed, oldest first.
func (s *Store) backgroundFor(ctx context.Context, runIDs []uuid.UUID) (started, followed map[uuid.UUID][]BackgroundTask, err error) {
	started, followed = map[uuid.UUID][]BackgroundTask{}, map[uuid.UUID][]BackgroundTask{}
	if len(runIDs) == 0 {
		return started, followed, nil
	}
	tasks, err := s.backgroundTasks(ctx, `run_id IN (SELECT value FROM json_each($1)) OR follow_up_run_id IN (SELECT value FROM json_each($1))`, runIDs)
	if err != nil {
		return nil, nil, err
	}
	want := map[uuid.UUID]bool{}
	for _, id := range runIDs {
		want[id] = true
	}
	for _, t := range tasks {
		if t.RunID != nil && want[*t.RunID] {
			started[*t.RunID] = append(started[*t.RunID], t)
		}
		if t.FollowUpRunID != nil && want[*t.FollowUpRunID] {
			followed[*t.FollowUpRunID] = append(followed[*t.FollowUpRunID], t)
		}
	}
	return started, followed, nil
}

// AppendTaskEvent appends one event of the given type to a task's event stream (outside a task mutation: it does not
// change the task's version).
func (s *Store) AppendTaskEvent(ctx context.Context, taskID uuid.UUID, typ string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO task_events(task_id,project_id,type,data) SELECT id,project_id,$2,$3 FROM task_records WHERE id=$1`, taskID, typ, json.RawMessage(b))
	return err
}
