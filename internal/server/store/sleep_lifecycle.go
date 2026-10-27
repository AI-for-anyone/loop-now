package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/sleepmodel"
	"time"
)

var ErrSleepSessionNotSaved = errors.New("sleep_session_not_saved")

// WakeSleepForWorkTx is called only after durable valid work exists, under the
// owner lock. Requested intent waits for its source's successful finalization.
func (s *Store) WakeSleepForWorkTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, agent, runtime, work uuid.UUID) error {
	if m != nil {
		if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, runtime); err != nil || fenced {
			return err
		}
	}
	key := sleepKey(agent, uuid.Nil)
	if m != nil {
		key = sleepKey(agent, m.Record.View.Task.ID)
	}
	r, e := scanSleep(tx.QueryRow(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE scope_key=$1 AND (state='sleeping' OR (state='woken' AND wake_run_id=timer_run_id)) ORDER BY requested_at DESC LIMIT 1 FOR UPDATE`, key))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if m != nil {
		strict, valid, e := s.TaskConsoleSleepValidTx(ctx, tx, m, r)
		if e != nil {
			return e
		}
		if strict && !valid {
			return s.cancelSleepTx(ctx, tx, m, &r, "node_changed")
		}
	}
	if r.WakeRunID != nil && *r.WakeRunID == work {
		return nil
	}
	if r.State == "woken" {
		table := "runs"
		if m != nil {
			table = "task_turns"
		}
		reasonSQL := ""
		if m == nil {
			reasonSQL = ",end_reason='sleep_superseded'"
		}
		tag, e := tx.Exec(ctx, `UPDATE `+table+` SET status='interrupted',ended_at=now()`+reasonSQL+` WHERE id=$1 AND status='queued'`, r.TimerRunID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
	}
	now := time.Now().UTC()
	why := "queued_work"
	r.State = "woken"
	r.EndedAt = &now
	r.EndReason = &why
	r.WakeRunID = &work
	if e = s.attachSleepCarrierTx(ctx, tx, m, r, work); e != nil {
		return e
	}
	return s.saveSleepTx(ctx, tx, m, &r)
}
func (s *Store) attachSleepCarrierTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, r Sleep, work uuid.UUID) error {
	table := "runs"
	if m != nil {
		table = "task_turns"
	}
	_, e := tx.Exec(ctx, `UPDATE `+table+` SET wake_sleep_id=$2 WHERE id=$1`, work, r.ID)
	return e
}

// FinishSleepTx must share the source Run/TaskTurn completion transaction.
func (s *Store) FinishSleepTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, run uuid.UUID, status string) (bool, error) {
	r, e := scanSleep(tx.QueryRow(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE source_run_id=$1 AND state='requested' FOR UPDATE`, run))
	if errors.Is(e, pgx.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if status != "completed" {
		why := "execution_failed"
		if status == "interrupted" {
			why = "interrupted"
		}
		return false, s.cancelSleepTx(ctx, tx, m, &r, why)
	}
	var saved bool
	if m == nil {
		e = tx.QueryRow(ctx, `SELECT state='active' AND session_file IS NOT NULL AND epoch=$2 FROM agent_sessions WHERE agent_id=$1`, r.AgentID, r.Epoch).Scan(&saved)
	} else {
		e = tx.QueryRow(ctx, `SELECT session_state='active' AND session_file IS NOT NULL AND NOT retired FROM task_instances WHERE id=$1`, r.RuntimeID).Scan(&saved)
	}
	if e != nil {
		return false, e
	}
	if !saved {
		return false, ErrSleepSessionNotSaved
	}
	if m != nil && (m.Record.Ended() || r.Mode == "task_node" && (m.Record.Current() == nil || r.NodeRunID == nil || m.Record.Current().ID != *r.NodeRunID || m.Record.Current().Status == "blocked")) {
		why := "node_changed"
		if m.Record.Ended() {
			why = "task_ended"
		}
		return false, s.cancelSleepTx(ctx, tx, m, &r, why)
	}
	strict, valid, e := s.TaskConsoleSleepValidTx(ctx, tx, m, r)
	if e != nil {
		return false, e
	}
	if strict && !valid {
		return false, s.cancelSleepTx(ctx, tx, m, &r, "node_changed")
	}
	if m != nil {
		if e = s.ExpireTaskConsoleTurnsTx(ctx, m, valueUUID(r.RuntimeID)); e != nil {
			return false, e
		}
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); e != nil {
		return false, e
	}
	now = now.UTC()
	wake := now.Add(time.Duration(r.Seconds) * time.Second)
	r.State = "sleeping"
	r.EnteredAt = &now
	r.WakeAt = &wake
	var work uuid.UUID
	if m == nil {
		e = tx.QueryRow(ctx, `SELECT id FROM runs WHERE agent_id=$1 AND status='queued' AND origin<>'sleep_wake' ORDER BY priority,queue_seq LIMIT 1`, r.AgentID).Scan(&work)
	} else {
		e = tx.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='queued' AND origin<>'sleep_wake' ORDER BY priority,queue_seq LIMIT 1`, r.RuntimeID).Scan(&work)
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return false, e
	}
	if e == nil {
		why := "queued_work"
		r.State = "woken"
		r.EndedAt = &now
		r.EndReason = &why
		r.WakeRunID = &work
		if e = s.attachSleepCarrierTx(ctx, tx, m, r, work); e != nil {
			return false, e
		}
	}
	return true, s.saveSleepTx(ctx, tx, m, &r)
}
func (s *Store) cancelSleepTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, r *Sleep, why string) error {
	table := "runs"
	if m != nil {
		table = "task_turns"
	}
	reasonSQL := ""
	if m == nil {
		reasonSQL = ",end_reason='sleep_invalidated'"
	} else {
		reasonSQL = ",error_code=CASE WHEN EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN 'task_conflict' ELSE error_code END,error_message=CASE WHEN EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN '原节点已结束或不再有效，该消息未执行' ELSE error_message END"
	}

	if _, e := tx.Exec(ctx, `UPDATE `+table+` SET status='interrupted',ended_at=now()`+reasonSQL+` WHERE id=$1 AND status='queued'`, r.TimerRunID); e != nil {
		return e
	}
	now := time.Now().UTC()
	r.State = "cancelled"
	r.EndedAt = &now
	r.EndReason = &why
	r.WakeRunID = nil
	return s.saveSleepTx(ctx, tx, m, r)
}
func (s *Store) CancelSleepsTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, agent uuid.UUID, why string) error {
	query := `SELECT ` + sleepColumns + ` FROM agent_sleeps WHERE agent_id=$1 AND task_id IS NULL AND (state IN ('requested','sleeping') OR (state='woken' AND wake_run_id=timer_run_id AND EXISTS(SELECT 1 FROM runs WHERE id=agent_sleeps.timer_run_id AND status='queued'))) ORDER BY id FOR UPDATE`
	arg := agent
	if m != nil {
		query = `SELECT ` + sleepColumns + ` FROM agent_sleeps WHERE task_id=$1 AND (state IN ('requested','sleeping') OR (state='woken' AND wake_run_id=timer_run_id AND EXISTS(SELECT 1 FROM task_turns WHERE id=agent_sleeps.timer_run_id AND status='queued'))) ORDER BY id FOR UPDATE`
		arg = m.Record.View.Task.ID
	}
	rows, e := tx.Query(ctx, query, arg)
	if e != nil {
		return e
	}
	rs, e := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sleep, error) { return scanSleep(row) })
	if e != nil {
		return e
	}
	for _, r := range rs {
		if m != nil && !m.Record.Ended() && r.RuntimeID != nil {
			if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, *r.RuntimeID); err != nil {
				return err
			} else if fenced {
				continue
			}
		}
		if m != nil && why == "node_changed" {
			strict, valid, e := s.TaskConsoleSleepValidTx(ctx, tx, m, r)
			if e != nil {
				return e
			}
			if strict && valid && r.Mode == "task_conversation" {
				continue
			}
			if (!strict || valid) && (r.Mode != "task_node" && !strict || r.Mode == "task_node" && m.Record.Current() != nil && r.NodeRunID != nil && *r.NodeRunID == m.Record.Current().ID && m.Record.Current().Status != "blocked" && m.Record.Current().Output == nil) {
				continue
			}
		}
		cancelWhy := why
		if m != nil && r.Mode == "task_node" && m.Record.Current() != nil && r.NodeRunID != nil && *r.NodeRunID == m.Record.Current().ID && m.Record.Current().Status == "blocked" {
			cancelWhy = "execution_failed"
		}
		if e = s.cancelSleepTx(ctx, tx, m, &r, cancelWhy); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) ActiveSleepProjection(ctx context.Context, agent, runtime uuid.UUID) (*sleepmodel.Record, int64, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var task uuid.UUID
	if runtime == uuid.Nil {
		if e := tx.QueryRow(ctx, `SELECT sleep_revision FROM agent_sessions WHERE agent_id=$1`, agent).Scan(&revision); e != nil {
			return nil, 0, e
		}
	} else {
		if e := tx.QueryRow(ctx, `SELECT sleep_revision,task_id FROM task_instances WHERE id=$1 AND agent_id=$2`, runtime, agent).Scan(&revision, &task); e != nil {
			return nil, 0, e
		}
	}
	r, e := scanSleep(tx.QueryRow(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE scope_key=$1 AND state='sleeping'`, sleepKey(agent, task)))
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, revision, nil
	}
	if e != nil {
		return nil, 0, e
	}
	return &r.Record, revision, nil
}

func (s *Store) ValidateTaskSleepsTx(ctx context.Context, m *TaskMutation) error {
	if m.Record.Ended() {
		return s.CancelSleepsTx(ctx, m.Tx, m, uuid.Nil, "task_ended")
	}
	return s.CancelSleepsTx(ctx, m.Tx, m, uuid.Nil, "node_changed")
}
