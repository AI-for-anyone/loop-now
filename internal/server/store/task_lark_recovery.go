package store

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

// TaskLarkRecoveryAction is internal startup coordination, never a public
// receipt. The producer of ProcessRecord must be the actual executor holding
// both original runtime gates; neither absence nor client metadata is proof.
type TaskLarkRecoveryAction struct {
	TaskID        uuid.UUID
	ActionID      uuid.UUID
	ActorKey      string
	RuntimeIDs    []uuid.UUID
	ProcessRecord json.RawMessage
}

func (s *Store) SaveTaskLarkRecoveryTx(ctx context.Context, m *TaskMutation, actor string, action uuid.UUID, record json.RawMessage) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(record, &object) != nil || len(object) == 0 {
		return apperr.Invalid("recovery", "原进程恢复记录无效")
	}
	var old []byte
	var status string
	if e := m.Tx.QueryRow(ctx, `SELECT status,recovery_record FROM task_lark_switch_actions WHERE task_id=$1 AND actor_key=$2 AND action_id=$3 FOR UPDATE`, m.Record.View.Task.ID, actor, action).Scan(&status, &old); e != nil {
		return notFoundIfNoRows(e, "Lark切换动作")
	}
	if status != "pending" && status != "handoff" {
		return apperr.Conflict("task_conflict", "原动作已结束")
	}
	if old != nil {
		var a, b any
		oldDecoder := json.NewDecoder(bytes.NewReader(old))
		oldDecoder.UseNumber()
		newDecoder := json.NewDecoder(bytes.NewReader(record))
		newDecoder.UseNumber()
		if oldDecoder.Decode(&a) != nil || newDecoder.Decode(&b) != nil {
			return apperr.Conflict("task_conflict", "原进程恢复记录无效")
		}
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		if !bytes.Equal(x, y) {
			return apperr.Conflict("task_conflict", "原进程恢复记录不可替换")
		}
		return nil
	}
	_, e := m.Tx.Exec(ctx, `UPDATE task_lark_switch_actions SET recovery_record=$4 WHERE task_id=$1 AND actor_key=$2 AND action_id=$3`, m.Record.View.Task.ID, actor, action, record)
	return e
}

// ConsumeTaskLarkRecoveryTx is the trusted native recovery callback's DB half.
// Its caller MUST first hold the exact local death receipt, and the native
// manager must already have confirmed old-owner/group death before invoking it.
// It atomically fails this pre-C action and removes only its original fences;
// no C, new turn or post-C release phase is performed. Lost acknowledgments may
// only reconfirm this exact recorded recovery failure, never another outcome.
func (s *Store) ConsumeTaskLarkRecoveryTx(ctx context.Context, m *TaskMutation, actor string, action uuid.UUID, runtimes [2]uuid.UUID, expected json.RawMessage) error {
	deny := func() error {
		return apperr.Conflict("task_conflict", "原进程恢复证明已变化或不属于待恢复动作")
	}
	if action == uuid.Nil || actor == "" || runtimes[0] == uuid.Nil || runtimes[1] == uuid.Nil || runtimes[0] == runtimes[1] {
		return deny()
	}
	var record map[string]json.RawMessage
	if json.Unmarshal(expected, &record) != nil || len(record) == 0 {
		return deny()
	}
	for _, key := range []string{"local", "native"} {
		value := bytes.TrimSpace(record[key])
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return deny()
		}
	}
	var status string
	var same bool
	var failure *string
	var captured []byte
	if e := m.Tx.QueryRow(ctx, `SELECT status,coalesce(recovery_record=$4::jsonb,false),failure_reason,captured_request FROM task_lark_switch_actions WHERE task_id=$1 AND actor_key=$2 AND action_id=$3 FOR UPDATE`, m.Record.View.Task.ID, actor, action, expected).Scan(&status, &same, &failure, &captured); e != nil {
		return notFoundIfNoRows(e, "Lark切换动作")
	}
	if !same {
		return deny()
	}
	var capture TaskLarkSwitchCapture
	if json.Unmarshal(captured, &capture) != nil || len(capture.Members) != 2 {
		return deny()
	}
	seen := map[uuid.UUID]bool{}
	roles := map[string]bool{}
	for _, member := range capture.Members {
		if (member.RuntimeID != runtimes[0] && member.RuntimeID != runtimes[1]) || seen[member.RuntimeID] || member.AgentID == uuid.Nil || (member.Role != "publisher" && member.Role != "followup") || roles[member.Role] {
			return deny()
		}
		seen[member.RuntimeID] = true
		roles[member.Role] = true
	}
	var fenceCount int
	var published bool
	if e := m.Tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_switch_fences WHERE task_id=$1 AND actor_key=$2 AND action_id=$3),EXISTS(SELECT 1 FROM task_lark_credential_bindings WHERE task_id=$1 AND actor_key=$2 AND action_id=$3)`, m.Record.View.Task.ID, actor, action).Scan(&fenceCount, &published); e != nil {
		return e
	}
	if published {
		return deny()
	}
	const reason = "startup_handoff_recovered"
	if status == "failed" && failure != nil && *failure == reason && fenceCount == 0 {
		return nil
	}
	if (status != "pending" && status != "handoff") || fenceCount != 2 {
		return deny()
	}
	for _, member := range capture.Members {
		var exists bool
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_lark_switch_fences WHERE task_id=$1 AND actor_key=$2 AND action_id=$3 AND runtime_id=$4 AND agent_id=$5 AND member_role=$6)`, m.Record.View.Task.ID, actor, action, member.RuntimeID, member.AgentID, member.Role).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return deny()
		}
	}
	return s.FailTaskLarkFenceTx(ctx, m, actor, action, reason)
}

// PendingTaskLarkRecovery is also consumed before generic stale-PID cleanup,
// so that owner can exclude precisely these unresolved runtimes. A nil record
// remains unknown; callers must not manufacture an empty/absent generation.
func (s *Store) PendingTaskLarkRecovery(ctx context.Context) ([]TaskLarkRecoveryAction, error) {
	rows, e := s.Pool.Query(ctx, `SELECT a.task_id,a.action_id,a.actor_key,a.recovery_record,array_agg(f.runtime_id ORDER BY f.runtime_id) FROM task_lark_switch_actions a JOIN task_lark_switch_fences f ON f.task_id=a.task_id AND f.actor_key=a.actor_key AND f.action_id=a.action_id WHERE a.status IN ('pending','handoff') GROUP BY a.task_id,a.action_id,a.actor_key,a.recovery_record ORDER BY a.task_id,a.action_id`)
	if e != nil {
		return nil, e
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskLarkRecoveryAction, error) {
		var a TaskLarkRecoveryAction
		e := r.Scan(&a.TaskID, &a.ActionID, &a.ActorKey, &a.ProcessRecord, &a.RuntimeIDs)
		return a, e
	})
}
