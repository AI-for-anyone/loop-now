package tasks

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/store"
)

// Constructor-only adapters preserve the sidecar owner's canonical record
// without defining a second native schema or exposing it through the API.
// A missing capture capability stays native:null, never proof of absence.
type TaskLarkNativeRecoveryLease interface {
	CaptureRecovery(context.Context, uuid.UUID) (json.RawMessage, error)
}

// Recover must retain the actual gate on an unknown result. consumePreCommit
// is invoked only after actual native death is proven, and its caller must
// already hold the exact local death receipt. It atomically resolves only the
// original pre-C action; C-success/unknown cannot reuse the idle watermark.
type TaskLarkNativeRecoveryManager interface {
	RecoverPendingHandoff(context.Context, uuid.UUID, [2]uuid.UUID, json.RawMessage, func(context.Context) error) (TaskLarkNativeLease, error)
}

type taskLarkRecoveryRecord struct {
	Local  local.RuntimeRecoveryRecord `json:"local"`
	Native json.RawMessage             `json:"native"`
}

func (s *Service) captureLarkSwitchRecovery(ctx context.Context, h *taskLarkHeld, lease *local.RuntimeHandoff) error {
	proof, e := lease.CaptureRecovery(ctx, h.key.action)
	if e != nil {
		return e
	}
	record := taskLarkRecoveryRecord{Local: proof}
	if capture, ok := h.native.(TaskLarkNativeRecoveryLease); ok {
		record.Native, e = capture.CaptureRecovery(ctx, h.key.action)
		if e != nil {
			return e
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(record.Native, &object) != nil || object == nil {
			return larkUnavailable()
		}
	}
	wire, e := json.Marshal(record)
	if e != nil {
		return e
	}
	_, e = s.Store.MutateTask(ctx, h.key.task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return nil, s.Store.SaveTaskLarkRecoveryTx(ctx, m, h.actor, h.key.action, wire)
	})
	return e
}
