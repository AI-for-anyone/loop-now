package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/store"
)

// Only persisted exact membership may exempt old PID records from generic
// cleanup. The private process record is deliberately not interpreted here:
// missing evidence remains protected and must never become an absent receipt.
func larkPendingHandoffs(actions []store.TaskLarkRecoveryAction) ([]local.PendingRuntimeHandoff, error) {
	out := make([]local.PendingRuntimeHandoff, 0, len(actions))
	seen := make(map[uuid.UUID]bool)
	for _, a := range actions {
		if a.TaskID == uuid.Nil || a.ActionID == uuid.Nil || a.ActorKey == "" || len(a.RuntimeIDs) != 2 || a.RuntimeIDs[0] == uuid.Nil || a.RuntimeIDs[1] == uuid.Nil || a.RuntimeIDs[0] == a.RuntimeIDs[1] {
			return nil, errors.New("invalid persisted Lark handoff membership")
		}
		for _, id := range a.RuntimeIDs {
			if seen[id] {
				return nil, errors.New("overlapping persisted Lark handoff membership")
			}
			seen[id] = true
		}
		out = append(out, local.PendingRuntimeHandoff{ActionID: a.ActionID, RuntimeIDs: [2]uuid.UUID{a.RuntimeIDs[0], a.RuntimeIDs[1]}})
	}
	return out, nil
}

// recoverPendingLarkSwitches runs before any conversation/task worker starts.
// An unresolved member remains protected by the persistent fence and any held
// local/native gates. It must not prevent unrelated scopes from starting.
func (a *App) recoverPendingLarkSwitches(ctx context.Context, pending []store.TaskLarkRecoveryAction, native *larkExecution, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	for _, item := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, timeout)
		err := a.recoverPendingLarkSwitch(bounded, item, native)
		cancel()
		if err != nil && a.Log != nil {
			a.Log.Warn("任务凭证交接恢复未确认，保留执行屏障", "taskId", item.TaskID, "actionId", item.ActionID)
		}
	}
	return ctx.Err()
}

func (a *App) recoverPendingLarkSwitch(ctx context.Context, item store.TaskLarkRecoveryAction, native *larkExecution) error {
	if len(item.RuntimeIDs) != 2 {
		return errors.New("invalid persisted Lark handoff membership")
	}
	ids := [2]uuid.UUID{item.RuntimeIDs[0], item.RuntimeIDs[1]}
	var envelope struct {
		Local  json.RawMessage `json:"local"`
		Native json.RawMessage `json:"native"`
	}
	dec := json.NewDecoder(bytes.NewReader(item.ProcessRecord))
	dec.DisallowUnknownFields()
	valid := dec.Decode(&envelope) == nil && dec.Decode(new(any)) == io.EOF
	var record local.RuntimeRecoveryRecord
	if !valid || json.Unmarshal(envelope.Local, &record) != nil {
		record = local.RuntimeRecoveryRecord{}
	}
	held, receipt, err := a.Executor.RecoverPendingHandoff(ctx, local.PendingRuntimeHandoff{ActionID: item.ActionID, RuntimeIDs: ids}, record)
	if err != nil {
		return err
	} // Do not Close a held unknown gate.
	if held == nil || receipt.ActionID != item.ActionID || !receipt.GroupAbsent[0] || !receipt.GroupAbsent[1] {
		return errors.New("local handoff not confirmed")
	}
	if native == nil {
		return errors.New("native recovery unavailable")
	}
	nativeHeld, err := native.RecoverPendingHandoff(ctx, item.ActionID, ids, envelope.Native, func(consumeCtx context.Context) error {
		_, err := a.Store.MutateTask(consumeCtx, item.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			return nil, a.Store.ConsumeTaskLarkRecoveryTx(consumeCtx, m, item.ActorKey, item.ActionID, ids, item.ProcessRecord)
		})
		return err
	})
	if err != nil {
		return err
	} // ACK unknown retains both gates, never redo C.
	if nativeHeld == nil {
		return errors.New("native handoff not confirmed")
	}
	if err = held.Close(); err != nil {
		return err
	}
	nativeHeld.Release() // Only pure-memory release after known committed consume.
	return nil
}
