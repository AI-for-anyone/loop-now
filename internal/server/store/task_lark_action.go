package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TaskLarkActionState is internal recovery state. Public controllers must use
// the frozen safe DTO; captured_request and any grant secrets are never loaded.
type TaskLarkActionState struct {
	ActionID, TaskID, NodeRunID uuid.UUID
	Status                      string
	ReservationTaskVersion      int64
	Receipt                     json.RawMessage
	FailureReason               *string
	CreatedAt                   time.Time
	FinishedAt                  *time.Time
}

// TaskLarkAction reads the original action even after its node stopped being
// current. Only noRows means absent; transport/database failures stay failures.
func (s *Store) TaskLarkAction(ctx context.Context, task uuid.UUID, actor string, action uuid.UUID) (TaskLarkActionState, error) {
	var out TaskLarkActionState
	err := s.Pool.QueryRow(ctx, `SELECT action_id,task_id,node_run_id,status,reservation_task_version,receipt,failure_reason,created_at,finished_at FROM task_lark_switch_actions WHERE task_id=$1 AND actor_key=$2 AND action_id=$3`, task, actor, action).Scan(&out.ActionID, &out.TaskID, &out.NodeRunID, &out.Status, &out.ReservationTaskVersion, &out.Receipt, &out.FailureReason, &out.CreatedAt, &out.FinishedAt)
	return out, notFoundIfNoRows(err, "Lark切换动作")
}
