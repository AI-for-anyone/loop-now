package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sync"
	"time"
)

// The cursor is process-local enumeration progress, never persisted sleep state.
// A fixed high-water ID bounds each round, so new arrivals cannot indefinitely
// postpone retrying owners earlier in the round. IDs never change with wakeAt.
type sleepScanProgress struct {
	mu             sync.Mutex
	after, through uuid.UUID
}

const sleepCandidatePredicate = `(state='requested' AND NOT EXISTS(SELECT 1 FROM runs WHERE id=agent_sleeps.source_run_id AND status IN ('starting','running','aborting')) AND NOT EXISTS(SELECT 1 FROM task_turns WHERE id=agent_sleeps.source_run_id AND status IN ('starting','running','aborting'))) OR (state='sleeping' AND wake_at<=now())`

// sleepCandidates returns at most batch rows, including at a round boundary.
// Selection takes no row locks; owner transactions still recheck eligibility.
func (s *Store) sleepCandidates(ctx context.Context, batch int) ([]Sleep, error) {
	for pass := 0; pass < 2; pass++ {
		if s.sleepScan.through == uuid.Nil {
			e := s.Pool.QueryRow(ctx, `SELECT id FROM agent_sleeps WHERE `+sleepCandidatePredicate+` ORDER BY id DESC LIMIT 1`).Scan(&s.sleepScan.through)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil, nil
			}
			if e != nil {
				return nil, e
			}
		}
		rows, e := s.Pool.Query(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE (`+sleepCandidatePredicate+`) AND id>$2 AND id<=$3 ORDER BY id LIMIT $1`, batch, s.sleepScan.after, s.sleepScan.through)
		if e != nil {
			return nil, e
		}
		records, e := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sleep, error) { return scanSleep(row) })
		if e != nil {
			return nil, e
		}
		if len(records) > 0 {
			return records, nil
		}
		s.sleepScan.after, s.sleepScan.through = uuid.Nil, uuid.Nil
	}
	return nil, nil
}

// ReconcileSleeps enumerates without a sleep lock, then rechecks each owner in
// the same lock order as messages. Failed attempts advance enumeration too.
func (s *Store) ReconcileSleeps(ctx context.Context, batch int) error {
	// Serialize only scheduler calls, not message/owner transactions. No other
	// Store path acquires this mutex, so it does not add a reverse owner lock.
	s.sleepScan.mu.Lock()
	defer s.sleepScan.mu.Unlock()
	records, e := s.sleepCandidates(ctx, batch)
	if e != nil {
		return e
	}
	var errs []error
	for _, candidate := range records {
		plan := InboxLockPlan{Parties: []Party{{Kind: "agent", ID: candidate.AgentID}}}
		if candidate.TaskID != nil {
			plan.Parties[0].RuntimeID = *candidate.RuntimeID
		}
		e = s.InboxTransaction(ctx, plan, func(scope *InboxTransaction) error {
			r, err := scanSleep(scope.Tx.QueryRow(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE id=$1 FOR UPDATE`, candidate.ID))
			if err != nil {
				return err
			}
			if r.State != "sleeping" && r.State != "requested" {
				return nil
			}
			var m *TaskMutation
			if r.TaskID != nil {
				m = scope.Tasks[*r.TaskID]
			}
			if m != nil && r.RuntimeID != nil {
				if fenced, e := s.TaskLarkRuntimeFencedTx(ctx, m, *r.RuntimeID); e != nil || fenced {
					return e
				}
			}
			strict := false
			var active bool
			var valid bool
			if m == nil {
				if err = scope.Tx.QueryRow(ctx, `SELECT state='active' AND session_file IS NOT NULL AND epoch=$2 FROM agent_sessions WHERE agent_id=$1`, r.AgentID, r.Epoch).Scan(&valid); err != nil {
					return err
				}
				if err = scope.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE agent_id=$1 AND status IN ('starting','running','aborting'))`, r.AgentID).Scan(&active); err != nil {
					return err
				}
			} else {
				if m.Record.Ended() {
					return s.cancelSleepTx(ctx, scope.Tx, m, &r, "task_ended")
				}
				if err = scope.Tx.QueryRow(ctx, `SELECT prepared AND NOT retired AND session_state='active' AND session_file IS NOT NULL FROM task_instances WHERE id=$1`, r.RuntimeID).Scan(&valid); err != nil {
					return err
				}
				var strictValid bool
				strict, strictValid, err = s.TaskConsoleSleepValidTx(ctx, scope.Tx, m, r)
				if err != nil {
					return err
				}
				if strict && !strictValid {
					return s.cancelSleepTx(ctx, scope.Tx, m, &r, "node_changed")
				}
				if err = s.ExpireTaskConsoleTurnsTx(ctx, m, valueUUID(r.RuntimeID)); err != nil {
					return err
				}
				n := m.Record.Current()
				if r.Mode == "task_node" && (n == nil || r.NodeRunID == nil || n.ID != *r.NodeRunID || n.Status == "blocked" || n.Output != nil) {
					return s.cancelSleepTx(ctx, scope.Tx, m, &r, "node_changed")
				}
				if err = scope.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting'))`, r.RuntimeID).Scan(&active); err != nil {
					return err
				}
			}
			if r.State == "requested" {
				if active {
					return nil
				}
				return s.cancelSleepTx(ctx, scope.Tx, m, &r, "interrupted")
			}
			if !valid {
				return s.cancelSleepTx(ctx, scope.Tx, m, &r, "session_reopened")
			}
			if active || r.WakeAt == nil || r.WakeAt.After(time.Now()) {
				return nil
			}
			var work uuid.UUID
			if m == nil {
				err = scope.Tx.QueryRow(ctx, `SELECT id FROM runs WHERE agent_id=$1 AND status='queued' AND origin<>'sleep_wake' ORDER BY priority,queue_seq LIMIT 1`, r.AgentID).Scan(&work)
			} else {
				err = scope.Tx.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='queued' AND origin<>'sleep_wake' ORDER BY priority,queue_seq LIMIT 1`, r.RuntimeID).Scan(&work)
			}
			if err == nil {
				return s.WakeSleepForWorkTx(ctx, scope.Tx, m, r.AgentID, valueUUID(r.RuntimeID), work)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			text := "平台通知：sleep已到期，请接着原会话处理。sleepId=" + r.ID.String()
			if r.Reason != nil {
				text += "；说明：" + *r.Reason
			}
			if m == nil {
				_, _, err = s.insertMessageAndRunTx(ctx, scope.Tx, NewRun{AgentID: r.AgentID, RunID: r.TimerRunID, Text: text, Origin: "sleep_wake", WakeSleepID: &r.ID})
			} else {
				node := r.NodeRunID
				// Conversation mode remains unbound even when the original blocked Inbox had a node ID.
				if r.Mode == "task_conversation" && !strict {
					node = nil
				}
				var turn TaskTurn
				turn, err = scanTaskTurn(scope.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,wake_sleep_id,execution_mode) VALUES($1,$2,$3,$4,$5,$6,'queued','sleep_wake',1,$7,$8,$9) RETURNING `+taskTurnColumns, r.TimerRunID, r.TaskID, r.RuntimeID, node, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), text, r.ID, r.Mode))
				if err == nil {
					err = s.taskLifecycle(ctx, scope.Tx, turn, EventRunQueued, map[string]any{"origin": "sleep_wake"})
				}
				if err == nil {
					_, err = s.InheritTaskConsoleLineageTx(ctx, m, r.SourceRunID, turn)
				}
				if err == nil && node != nil {
					m.Record.Current().RunIDs = append(m.Record.Current().RunIDs, turn.ID)
					err = m.Emit("message.created", taskMessage(turn, false))
				}
			}
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			why := "deadline"
			r.State = "woken"
			r.EndedAt = &now
			r.EndReason = &why
			r.WakeRunID = &r.TimerRunID
			return s.saveSleepTx(ctx, scope.Tx, m, &r)
		})
		// Advance after every attempted owner, including rollback/failure.
		// Restart may repeat a bounded round; PG rechecks and stable timer IDs
		// remain the authority, so resetting this cursor cannot duplicate work.
		s.sleepScan.after = candidate.ID
		if e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func valueUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
