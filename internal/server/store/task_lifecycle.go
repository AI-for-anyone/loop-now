package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/taskmodel"
)

func (s *Store) taskLifecycle(ctx context.Context, q execer, t TaskTurn, kind string, data map[string]any) error {
	var node *string
	if t.NodeRunID != uuid.Nil {
		v := t.NodeRunID.String()
		node = &v
	}
	if data == nil {
		data = map[string]any{}
	}
	if t.RequestID != nil {
		enriched := make(map[string]any, len(data)+1)
		for k, v := range data {
			enriched[k] = v
		}
		enriched["requestId"] = t.RequestID
		data = enriched
	}
	b, e := json.Marshal(data)
	if e != nil {
		return e
	}
	tag, e := q.Exec(ctx, `INSERT INTO lifecycle_events(id,agent_id,type,data,instance_id,task_id,workflow_node,task_turn_id,inbox_message_id) SELECT $1,agent_id,$2,$3,$4,$5,$6,$7,$8 FROM task_instances WHERE id=$9 AND task_id=$5`, uuid.Must(uuid.NewV7()), kind, b, s.InstanceID, t.TaskID, node, t.ID, t.TriggerInboxID, t.RuntimeID)
	if e == nil && tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return e
}

// RecordTaskObservation records non-state observations. Callers warn on failure.
func (s *Store) RecordTaskObservation(ctx context.Context, t TaskTurn, kind string, data map[string]any) error {
	return s.taskLifecycle(ctx, s.Pool, t, kind, data)
}

// MarkTaskRunning binds the state transition and its event to one transaction.
func (s *Store) MarkTaskRunning(ctx context.Context, t TaskTurn, version int64) error {
	_, e := s.MutateTask(ctx, t.TaskID, nil, func(m *TaskMutation) (any, error) {
		old, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 FOR UPDATE`, t.ID, t.TaskID))
		if e != nil {
			return nil, e
		}
		if old.Status != "starting" {
			m.Unchanged()
			return nil, nil
		}
		if _, e = m.Tx.Exec(ctx, `UPDATE task_turns SET status='running' WHERE id=$1`, t.ID); e != nil {
			return nil, e
		}
		if e = s.taskLifecycle(ctx, m.Tx, old, EventRunRunning, map[string]any{"configVersion": version}); e != nil {
			return nil, e
		}
		old.Status = "running"
		if old.NodeRunID != uuid.Nil {
			return nil, m.Emit("message.created", taskMessage(old, true))
		}
		return nil, nil
	})
	return e
}

func (s *Store) taskEndedEvent(ctx context.Context, m *TaskMutation, t TaskTurn) error {
	return s.taskLifecycle(ctx, m.Tx, t, EventRunEnded, map[string]any{"outcome": t.Status, "errorCode": t.ErrorCode, "errorMessage": taskmodel.PublicError(t.ErrorCode, t.ErrorMessage)})
}

// InterruptTaskRuntime only changes rounds matching the caller's transaction.
func (s *Store) InterruptTaskRuntime(ctx context.Context, m *TaskMutation, runtime *uuid.UUID, onlyUnbound bool) error {
	rows, e := m.Tx.Query(ctx, `UPDATE task_turns SET error_code=CASE WHEN status='queued' AND EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN 'task_conflict' ELSE error_code END,error_message=CASE WHEN status='queued' AND EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN '原节点已结束或不再有效，该消息未执行' ELSE error_message END,status=CASE WHEN status='queued' THEN 'interrupted' ELSE 'aborting' END,ended_at=CASE WHEN status='queued' THEN now() ELSE ended_at END WHERE task_id=$1 AND ($2::uuid IS NULL OR runtime_id=$2) AND (NOT $3 OR node_run_id IS NULL) AND status IN ('queued','starting','running') RETURNING `+taskTurnColumns, m.Record.View.Task.ID, runtime, onlyUnbound)
	if e != nil {
		return e
	}
	turns, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if e != nil {
		return e
	}
	for _, t := range turns {
		if e = s.taskAbortEvent(ctx, m, t); e != nil {
			return e
		}
		if t.NodeRunID != uuid.Nil {
			if e = m.Emit("message.created", taskMessage(t, false)); e != nil {
				return e
			}
			if t.StartedAt != nil {
				if e = m.Emit("message.created", taskMessage(t, true)); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Store) taskAbortEvent(ctx context.Context, m *TaskMutation, t TaskTurn) error {
	if e := s.taskLifecycle(ctx, m.Tx, t, EventRunAbortRequested, map[string]any{}); e != nil {
		return e
	}
	if t.Status == "interrupted" {
		return s.taskEndedEvent(ctx, m, t)
	}
	return nil
}

func (s *Store) RecoverTaskTurns(ctx context.Context, m *TaskMutation) (int, bool, error) {
	rows, e := m.Tx.Query(ctx, `UPDATE task_turns SET status='interrupted',ended_at=now(),error_code='server_restarted',error_message='服务重启，未重放该轮' WHERE task_id=$1 AND status IN ('starting','running','aborting') RETURNING `+taskTurnColumns, m.Record.View.Task.ID)
	if e != nil {
		return 0, false, e
	}
	turns, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if e != nil {
		return 0, false, e
	}
	currentInterrupted := false
	for _, t := range turns {
		if _, e = s.FinishSleepTx(ctx, m.Tx, m, t.ID, "interrupted"); e != nil {
			return 0, false, e
		}
		if n := m.Record.Current(); n != nil && n.ID == t.NodeRunID && n.Status != "blocked" {
			currentInterrupted = true
		}
		if e = s.taskEndedEvent(ctx, m, t); e != nil {
			return 0, false, e
		}
		if t.NodeRunID != uuid.Nil {
			if e = m.Emit("message.created", taskMessage(t, true)); e != nil {
				return 0, false, e
			}
		}
	}
	return len(turns), currentInterrupted, nil
}

func (s *Store) cancelUnboundInboxQueue(ctx context.Context, m *TaskMutation, runtime uuid.UUID) error {
	rows, e := m.Tx.Query(ctx, `UPDATE task_turns SET status='interrupted',ended_at=now() WHERE task_id=$1 AND runtime_id=$2 AND node_run_id IS NULL AND status='queued' AND origin='inbox_reminder' RETURNING `+taskTurnColumns, m.Record.View.Task.ID, runtime)
	if e != nil {
		return e
	}
	turns, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if e != nil {
		return e
	}
	for _, t := range turns {
		if e = s.taskAbortEvent(ctx, m, t); e != nil {
			return e
		}
	}
	return nil
}
