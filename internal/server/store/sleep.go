package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/sleepmodel"
	"time"
)

type Sleep struct {
	sleepmodel.Record
	ScopeKey, Mode string
	Epoch          *int
	TimerRunID     uuid.UUID
	Receipt        json.RawMessage
}

const sleepColumns = `id,scope_kind,agent_id,task_id,runtime_id,node_run_id,source_run_id,state,seconds,requested_at,entered_at,wake_at,ended_at,end_reason,wake_run_id,reason,revision,scope_key,mode,session_epoch,timer_run_id,receipt`

func scanSleep(row pgx.Row) (Sleep, error) {
	var s Sleep
	e := row.Scan(&s.ID, &s.ScopeKind, &s.AgentID, &s.TaskID, &s.RuntimeID, &s.NodeRunID, &s.SourceRunID, &s.State, &s.Seconds, &s.RequestedAt, &s.EnteredAt, &s.WakeAt, &s.EndedAt, &s.EndReason, &s.WakeRunID, &s.Reason, &s.Revision, &s.ScopeKey, &s.Mode, &s.Epoch, &s.TimerRunID, &s.Receipt)
	if e == nil {
		s.RequestedAt = s.RequestedAt.UTC()
		for _, at := range []*time.Time{s.EnteredAt, s.WakeAt, s.EndedAt} {
			if at != nil {
				*at = at.UTC()
			}
		}
	}
	return s, e
}
func sleepKey(agent, task uuid.UUID) string {
	if task == uuid.Nil {
		return "normal/" + agent.String()
	}
	return "task/" + task.String() + "/agent/" + agent.String()
}
func sleepConflict() error {
	return apperr.Conflict(apperr.CodeSleepConflict, "当前运行无法登记睡眠或动作内容冲突")
}
func sleepPlan(p auth.Principal) (InboxLockPlan, error) {
	if p.Kind != auth.KindAgent {
		return InboxLockPlan{}, apperr.Forbidden("仅Agent可操作自身sleep")
	}
	if p.ID == uuid.Nil || (p.TaskID == uuid.Nil) != (p.RuntimeID == uuid.Nil) {
		return InboxLockPlan{}, sleepConflict()
	}
	return InboxLockPlan{Parties: []Party{{Kind: "agent", ID: p.ID, RuntimeID: p.RuntimeID}}}, nil
}
func (s *Store) RequestSleep(ctx context.Context, p auth.Principal, in sleepmodel.Input, maxReason int) (sleepmodel.Receipt, error) {
	var out sleepmodel.Receipt
	plan, e := sleepPlan(p)
	if e != nil {
		return out, e
	}
	if in.ActionID == uuid.Nil || in.RunID == uuid.Nil || in.Seconds < 1 || in.Seconds > 2147483647 || in.Reason != nil && len(*in.Reason) > maxReason {
		return out, apperr.Invalid("sleep", "参数无效")
	}
	raw, _ := json.Marshal(in)
	hash := sha256.Sum256(raw)
	e = s.InboxTransaction(ctx, plan, func(scope *InboxTransaction) error {
		tx := scope.Tx
		key := sleepKey(p.ID, p.TaskID)
		var oldHash []byte
		var receipt []byte
		err := tx.QueryRow(ctx, `SELECT body_hash,receipt FROM agent_sleeps WHERE scope_key=$1 AND action_id=$2`, key, in.ActionID).Scan(&oldHash, &receipt)
		if err == nil {
			if !bytes.Equal(oldHash, hash[:]) {
				return sleepConflict()
			}
			return json.Unmarshal(receipt, &out)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_sleeps WHERE scope_key=$1 AND state IN ('requested','sleeping'))`, key).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return sleepConflict()
		}
		rec := Sleep{ScopeKey: key, Mode: "normal", TimerRunID: uuid.Must(uuid.NewV7())}
		rec.ID = uuid.Must(uuid.NewV7())
		rec.AgentID = p.ID
		rec.ScopeKind = "normal"
		rec.SourceRunID = in.RunID
		rec.State = "requested"
		rec.Seconds = in.Seconds
		rec.Reason = in.Reason
		if p.TaskID == uuid.Nil {
			var st string
			var owner uuid.UUID
			var epoch int
			if err = tx.QueryRow(ctx, `SELECT r.agent_id,r.status,a.epoch FROM runs r JOIN agent_sessions a ON a.agent_id=r.agent_id WHERE r.id=$1`, in.RunID).Scan(&owner, &st, &epoch); err != nil {
				return sleepConflict()
			}
			if owner != p.ID || st != "running" {
				return sleepConflict()
			}
			rec.Epoch = &epoch
		} else {
			m := scope.Tasks[p.TaskID]
			if m == nil || m.Record.Ended() {
				return sleepConflict()
			}
			var owner uuid.UUID
			var ready bool
			if err = tx.QueryRow(ctx, `SELECT agent_id,prepared AND NOT retired FROM task_instances WHERE id=$1 AND task_id=$2`, p.RuntimeID, p.TaskID).Scan(&owner, &ready); err != nil {
				return err
			}
			if owner != p.ID || !ready {
				return sleepConflict()
			}
			turn, err := scanTaskTurn(tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 AND runtime_id=$3`, in.RunID, p.TaskID, p.RuntimeID))
			if err != nil || turn.Status != "running" {
				return sleepConflict()
			}
			rec.ScopeKind = "task"
			rec.TaskID = &p.TaskID
			rec.RuntimeID = &p.RuntimeID
			rec.Mode = "task_conversation"
			n := m.Record.Current()
			if turn.ExecutionMode == "task_node" {
				if err := s.CheckTaskLarkRunTx(ctx, m, turn.NodeRunID, p.RuntimeID, in.RunID); err != nil {
					return err
				}
				if n == nil || n.RuntimeID != p.RuntimeID || n.Status == "blocked" || n.ID != turn.NodeRunID || n.Output != nil {
					return sleepConflict()
				}
				rec.Mode = "task_node"
				rec.NodeRunID = &n.ID
			} else if turn.ExecutionMode != "task_conversation" {
				return sleepConflict()
			}
			lineage, strict, e := s.TaskConsoleSleepSourceTx(ctx, m, turn)
			if e != nil {
				return e
			}
			if strict {
				rec.NodeRunID = &lineage.NodeRunID
				rec.Mode = lineage.ExecutionMode
			}

		}
		if err = s.bumpSleepRevisionTx(ctx, tx, &rec); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&rec.RequestedAt); err != nil {
			return err
		}
		rec.RequestedAt = rec.RequestedAt.UTC()
		out = sleepmodel.Receipt{Sleep: rec.Record, EndTurn: true}
		b, _ := json.Marshal(out)
		_, err = tx.Exec(ctx, `INSERT INTO agent_sleeps(id,scope_key,scope_kind,mode,agent_id,task_id,runtime_id,node_run_id,session_epoch,source_run_id,action_id,body_hash,receipt,state,seconds,reason,timer_run_id,revision,requested_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'requested',$14,$15,$16,$17,$18)`, rec.ID, key, rec.ScopeKind, rec.Mode, rec.AgentID, rec.TaskID, rec.RuntimeID, rec.NodeRunID, rec.Epoch, rec.SourceRunID, in.ActionID, hash[:], b, rec.Seconds, rec.Reason, rec.TimerRunID, rec.Revision, rec.RequestedAt)
		if err != nil {
			return err
		}
		return s.sleepEventTx(ctx, tx, scope.Tasks[p.TaskID], rec)
	})
	return out, e
}
func (s *Store) SleepState(ctx context.Context, p auth.Principal) (sleepmodel.State, error) {
	var out sleepmodel.State
	plan, e := sleepPlan(p)
	if e != nil {
		return out, e
	}
	e = s.InboxTransaction(ctx, plan, func(scope *InboxTransaction) error {
		if p.TaskID == uuid.Nil {
			err := scope.Tx.QueryRow(ctx, `SELECT id FROM runs WHERE agent_id=$1 AND status IN ('starting','running','aborting')`, p.ID).Scan(&out.CurrentRunID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		} else {
			var valid bool
			if err := scope.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_instances WHERE id=$1 AND task_id=$2 AND agent_id=$3 AND NOT retired)`, p.RuntimeID, p.TaskID, p.ID).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return sleepConflict()
			}
			err := scope.Tx.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting')`, p.RuntimeID).Scan(&out.CurrentRunID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		rec, err := scanSleep(scope.Tx.QueryRow(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE scope_key=$1 AND state IN ('requested','sleeping')`, sleepKey(p.ID, p.TaskID)))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out.Sleep = &rec.Record
		return nil
	})
	return out, e
}
func (s *Store) bumpSleepRevisionTx(ctx context.Context, tx pgx.Tx, r *Sleep) error {
	if r.ScopeKind == "normal" {
		return tx.QueryRow(ctx, `UPDATE agent_sessions SET sleep_revision=sleep_revision+1 WHERE agent_id=$1 RETURNING sleep_revision`, r.AgentID).Scan(&r.Revision)
	}
	return tx.QueryRow(ctx, `UPDATE task_instances SET sleep_revision=sleep_revision+1 WHERE id=$1 RETURNING sleep_revision`, r.RuntimeID).Scan(&r.Revision)
}
func (s *Store) sleepEventTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, r Sleep) error {
	u := sleepmodel.Updated{ScopeKind: r.ScopeKind, AgentID: r.AgentID, TaskID: r.TaskID, RuntimeID: r.RuntimeID, NodeRunID: r.NodeRunID, SleepRevision: r.Revision}
	if r.State == "sleeping" {
		u.Sleep = &r.Record
	} else if r.State == "woken" || r.State == "cancelled" {
		u.EndedSleepID = &r.ID
	}
	if m != nil {
		m.Changed()
		for i := range m.Record.View.Nodes {
			n := &m.Record.View.Nodes[i]
			if r.RuntimeID != nil && n.RuntimeID == *r.RuntimeID {
				n.SleepRevision = r.Revision
				if r.Mode == "task_node" && r.NodeRunID != nil && n.ID == *r.NodeRunID {
					n.Sleep = u.Sleep
				}
			}
		}
		turn := TaskTurn{ID: r.SourceRunID, TaskID: *r.TaskID, RuntimeID: *r.RuntimeID, NodeRunID: valueUUID(r.NodeRunID)}
		if err := s.taskLifecycle(ctx, tx, turn, "sleep."+r.State, map[string]any{"sleepRecord": r.Record}); err != nil {
			return err
		}
		return m.Emit("sleep.updated", u)
	}
	return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: r.AgentID, RunID: &r.SourceRunID, Type: "sleep." + r.State, Data: map[string]any{"sleep": u, "sleepRecord": r.Record}})
}
func (s *Store) saveSleepTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, r *Sleep) error {
	if err := s.bumpSleepRevisionTx(ctx, tx, r); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE agent_sleeps SET state=$2,entered_at=$3,wake_at=$4,ended_at=$5,end_reason=$6,wake_run_id=$7,revision=$8 WHERE id=$1`, r.ID, r.State, r.EnteredAt, r.WakeAt, r.EndedAt, r.EndReason, r.WakeRunID, r.Revision)
	if err != nil {
		return err
	}
	return s.sleepEventTx(ctx, tx, m, *r)
}
