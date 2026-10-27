package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
)

// TaskLarkPublicationState is a single-snapshot, secret-free confirmation of
// the original action. A query failure is unknown, never proof of rollback.
// It does not retry C, clear coordination or infer the latest task attempt.
type TaskLarkPublicationState struct {
	Status                                    string
	BindingRevision                           *int64
	ContinuationNodeRunID, ContinuationTurnID *uuid.UUID
	DispatchStatus                            *string
	OutstandingFences                         int
	Receipt                                   json.RawMessage
}

func (s *Store) TaskLarkPublication(ctx context.Context, task uuid.UUID, actor string, action uuid.UUID) (TaskLarkPublicationState, error) {
	var out TaskLarkPublicationState
	var members int
	err := s.Pool.QueryRow(ctx, `SELECT a.status,b.revision,b.continuation_node_run_id,b.continuation_turn_id,t.status,(SELECT count(*) FROM task_lark_switch_fences f WHERE f.task_id=a.task_id AND f.actor_key=a.actor_key AND f.action_id=a.action_id),(SELECT count(*) FROM task_lark_binding_members m WHERE m.task_id=b.task_id AND m.revision=b.revision),a.receipt FROM task_lark_switch_actions a LEFT JOIN task_lark_credential_bindings b ON b.task_id=a.task_id AND b.actor_key=a.actor_key AND b.action_id=a.action_id LEFT JOIN task_turns t ON t.id=b.continuation_turn_id AND t.task_id=a.task_id WHERE a.task_id=$1 AND a.actor_key=$2 AND a.action_id=$3`, task, actor, action).Scan(&out.Status, &out.BindingRevision, &out.ContinuationNodeRunID, &out.ContinuationTurnID, &out.DispatchStatus, &out.OutstandingFences, &members, &out.Receipt)
	if err != nil {
		return out, notFoundIfNoRows(err, "Lark切换动作")
	}
	if out.Status == "succeeded" {
		if out.BindingRevision == nil || out.ContinuationNodeRunID == nil || out.ContinuationTurnID == nil || out.DispatchStatus == nil || members != 2 || len(out.Receipt) == 0 {
			return TaskLarkPublicationState{}, errors.New("Lark switch committed publication incomplete")
		}
	} else if out.BindingRevision != nil || members != 0 {
		return TaskLarkPublicationState{}, errors.New("Lark switch unpublished action has effective binding")
	}
	return out, nil
}
