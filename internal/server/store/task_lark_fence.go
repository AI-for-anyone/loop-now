package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

// TaskLarkFenceMember is an internal, already validated reservation member.
// This is not an HTTP DTO or an alternative to original snapshot validation.
type TaskLarkFenceMember struct {
	AgentID, RuntimeID uuid.UUID
	Role               string
}

type TaskLarkFenceReservation struct {
	ActionID, TaskID, NodeRunID uuid.UUID
	ActorKey                    string
	RequestHash                 [sha256.Size]byte
	ExpectedTaskVersion         int64
	Members                     []TaskLarkFenceMember
	Capture                     *TaskLarkSwitchCapture
}

// TaskLarkSwitchCapture is persisted A-phase provenance, not a public DTO.
type TaskLarkSwitchCapture struct {
	Target                  TaskLarkTarget
	Members                 []TaskLarkMemberCapture
	ExpectedBindingRevision int64
}

// ReserveTaskLarkFenceTx is the A-phase coordination primitive. Its caller must
// validate original credentials and the pinned target grant in this same task
// transaction before calling it. No provider or process calls occur here.
// Caller owns action serialization -> project SHARE -> task aggregate UPDATE;
// no nested transaction or late acquisition of an earlier action lock.
func (s *Store) ReserveTaskLarkFenceTx(ctx context.Context, m *TaskMutation, in TaskLarkFenceReservation) (bool, error) {
	if in.ActionID == uuid.Nil || in.TaskID != m.Record.View.Task.ID || in.ActorKey == "" || len(in.Members) != 2 {
		return false, apperr.Invalid("members", "必须明确两个不同任务实例")
	}
	var existingTask uuid.UUID
	var existingHash []byte
	err := m.Tx.QueryRow(ctx, `SELECT task_id,request_hash FROM task_lark_switch_actions WHERE actor_key=$1 AND action_id=$2`, in.ActorKey, in.ActionID).Scan(&existingTask, &existingHash)
	if err == nil {
		if existingTask != in.TaskID || !bytes.Equal(existingHash, in.RequestHash[:]) {
			return false, apperr.Conflict("task_conflict", "原动作与本次内容不一致")
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	n := m.Record.Current()
	if m.Record.Ended() || m.Record.View.Task.Status != "blocked" || n == nil || n.ID != in.NodeRunID || n.Status != "blocked" || m.Record.View.Task.Version != in.ExpectedTaskVersion {
		return false, apperr.Conflict("task_conflict", "原阻塞节点或任务版本已变化")
	}
	if in.Capture != nil {
		var current int64
		if err := m.Tx.QueryRow(ctx, `SELECT coalesce(max(revision),0) FROM task_lark_credential_bindings WHERE task_id=$1`, in.TaskID).Scan(&current); err != nil {
			return false, err
		}
		if in.Capture.ExpectedBindingRevision != current {
			return false, apperr.Conflict("task_conflict", "任务Lark覆盖版本已变化")
		}
	}
	members := slices.Clone(in.Members)
	slices.SortFunc(members, func(a, b TaskLarkFenceMember) int { return bytes.Compare(a.RuntimeID[:], b.RuntimeID[:]) })
	if members[0].RuntimeID == uuid.Nil || members[0].RuntimeID == members[1].RuntimeID || members[0].AgentID == members[1].AgentID {
		return false, apperr.Invalid("members", "必须明确两个不同任务实例")
	}
	roles := map[string]bool{}
	for _, member := range members {
		if member.AgentID == uuid.Nil || (member.Role != "publisher" && member.Role != "followup") || roles[member.Role] {
			return false, apperr.Invalid("members", "必须明确当前执行与结果跟进实例")
		}
		roles[member.Role] = true
		if member.Role == "publisher" && (n.RuntimeID != member.RuntimeID || n.AgentID != member.AgentID) {
			return false, apperr.Conflict("task_conflict", "当前执行实例不匹配")
		}
		var task, agent uuid.UUID
		var retired bool
		if err := m.Tx.QueryRow(ctx, `SELECT task_id,agent_id,retired FROM task_instances WHERE id=$1 FOR UPDATE`, member.RuntimeID).Scan(&task, &agent, &retired); err != nil {
			return false, notFoundIfNoRows(err, "任务实例")
		}
		if task != in.TaskID || agent != member.AgentID || retired {
			return false, apperr.Conflict("task_conflict", "原任务实例不匹配或不可用")
		}
		if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, member.RuntimeID); err != nil {
			return false, err
		} else if fenced {
			return false, apperr.Conflict("task_conflict", "实例正在交接")
		}
		var busy bool
		if err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns t WHERE runtime_id=$1 AND (status IN ('starting','running','aborting') OR (status='queued' AND NOT (origin='sleep_wake' AND EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.timer_run_id=t.id AND z.state='sleeping')))))`, member.RuntimeID).Scan(&busy); err != nil {
			return false, err
		}
		if busy {
			return false, apperr.Conflict("task_conflict", "实例仍有执行或排队工作")
		}
	}
	capture := in.Capture
	if capture == nil {
		capture = &TaskLarkSwitchCapture{}
	}
	encoded, err := json.Marshal(capture)
	if err != nil {
		return false, err
	}
	_, err = m.Tx.Exec(ctx, `INSERT INTO task_lark_switch_actions(actor_key,action_id,task_id,node_run_id,request_hash,status,reservation_task_version,captured_request) VALUES($1,$2,$3,$4,$5,'pending',$6,$7)`, in.ActorKey, in.ActionID, in.TaskID, in.NodeRunID, in.RequestHash[:], m.Record.View.Task.Version+1, encoded)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		if _, err = m.Tx.Exec(ctx, `INSERT INTO task_lark_switch_fences(runtime_id,task_id,agent_id,actor_key,action_id,member_role) VALUES($1,$2,$3,$4,$5,$6)`, member.RuntimeID, in.TaskID, member.AgentID, in.ActorKey, in.ActionID, member.Role); err != nil {
			return false, err
		}
	}
	m.Changed()
	return true, nil
}

// TaskLarkRuntimeFencedTx is shared by claim, strict Inbox, sleep and Source
// authorization. It must be called while holding the same task aggregate lock.
func (s *Store) TaskLarkRuntimeFencedTx(ctx context.Context, m *TaskMutation, runtime uuid.UUID) (bool, error) {
	var exists bool
	err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_lark_switch_fences WHERE task_id=$1 AND runtime_id=$2)`, m.Record.View.Task.ID, runtime).Scan(&exists)
	return exists, err
}

// FailTaskLarkFenceTx is allowed only after the caller established safe process
// handoff. It deliberately does not touch original sleeps, wakeAt or snapshots.
func (s *Store) FailTaskLarkFenceTx(ctx context.Context, m *TaskMutation, actor string, action uuid.UUID, reason string) error {
	tag, err := m.Tx.Exec(ctx, `UPDATE task_lark_switch_actions SET status='failed',failure_reason=$4,finished_at=clock_timestamp() WHERE actor_key=$1 AND action_id=$2 AND task_id=$3 AND status IN ('pending','handoff')`, actor, action, m.Record.View.Task.ID, reason)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = m.Tx.Exec(ctx, `DELETE FROM task_lark_switch_fences WHERE actor_key=$1 AND action_id=$2 AND task_id=$3`, actor, action, m.Record.View.Task.ID)
	if err == nil {
		m.Changed()
	}
	return err
}

// DeferTaskLarkInboxTx preserves a real, already inserted ordinary Inbox
// message while its original runtime is fenced. It creates no synthetic read.
func (s *Store) DeferTaskLarkInboxTx(ctx context.Context, m *TaskMutation, message, runtime uuid.UUID) (bool, error) {
	var actor string
	var action uuid.UUID
	err := m.Tx.QueryRow(ctx, `SELECT actor_key,action_id FROM task_lark_switch_fences WHERE task_id=$1 AND runtime_id=$2`, m.Record.View.Task.ID, runtime).Scan(&actor, &action)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var addressed bool
	if err = m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_messages WHERE id=$1 AND to_runtime_id=$2)`, message, runtime).Scan(&addressed); err != nil {
		return false, err
	}
	if !addressed {
		return false, apperr.NotFound("消息")
	}
	_, err = m.Tx.Exec(ctx, `INSERT INTO task_lark_deferred_inbox(message_id,runtime_id,task_id,actor_key,action_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(message_id) DO NOTHING`, message, runtime, m.Record.View.Task.ID, actor, action)
	return true, err
}
