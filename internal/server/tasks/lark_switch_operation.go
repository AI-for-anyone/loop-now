package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// Actual manager adapters must atomically reject active/unknown native work
// and fence both creation and execution, including runtimes with no consumer.
type TaskLarkNativeManager interface {
	Acquire(context.Context, []uuid.UUID) (TaskLarkNativeLease, error)
}

// Internal adapter sentinel only; the public response remains task_conflict.
var ErrTaskLarkNativeBusy = errors.New("task_lark_native_busy")

type TaskLarkNativeLease interface {
	// All fallible revoke/close/drain happens here, before C. Success seals the
	// lease: Release must afterwards be idempotent memory-only and infallible.
	Seal(context.Context) error
	Release()
}
type larkSwitchKey struct{ task, action uuid.UUID }
type taskLarkHeld struct {
	key                        larkSwitchKey
	actor                      string
	ids                        []uuid.UUID
	local                      *local.RuntimeHandoff
	native                     TaskLarkNativeLease
	ready, attempted, released bool
	mu                         sync.Mutex // serialize original-outcome confirmation, never redo C
}

func larkUnavailable() error {
	return &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "任务凭证交接暂不可确认，请仅查询原动作ID"}
}
func (s *Service) larkRecordTimeout() time.Duration {
	if s.Options.RecordTimeout > 0 {
		return s.Options.RecordTimeout
	}
	return 5 * time.Second
}
func larkActor(p auth.Principal) string { return bodyAction(p, uuid.Nil, "", nil).ActorKey }

func (s *Service) larkDispatchPaused(runtime uuid.UUID) bool {
	s.larkMu.Lock()
	defer s.larkMu.Unlock()
	for _, h := range s.larkHeld {
		for _, id := range h.ids {
			if id == runtime {
				return true
			}
		}
	}
	return false
}

func validateLarkSwitch(in taskmodel.TaskLarkSwitchInput) error {
	if in.ActionID == uuid.Nil || in.ExpectedNodeRunID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil || in.TargetAccountID == uuid.Nil || in.TargetVersionID == uuid.Nil || in.ExpectedTaskVersion < 1 || in.ExpectedAccountRevision < 1 || in.ExpectedLarkBindingRevision < 0 || len(in.ExpectedMembers) != 2 {
		return apperr.Invalid("switch", "原任务、固定目标版本或成员CAS无效")
	}
	publisher := 0
	for i, m := range in.ExpectedMembers {
		if m.AgentID == uuid.Nil || m.RuntimeID == uuid.Nil || m.ExpectedBindingRevision < 0 {
			return apperr.Invalid("expectedMembers", "成员标识或版本无效")
		}
		if i == 1 && (m.AgentID == in.ExpectedMembers[0].AgentID || m.RuntimeID == in.ExpectedMembers[0].RuntimeID) {
			return apperr.Invalid("expectedMembers", "必须明确两个不同成员")
		}
		if m.AgentID == in.ExpectedAgentID && m.RuntimeID == in.ExpectedRuntimeID {
			publisher++
		}
	}
	if publisher != 1 {
		return apperr.Invalid("expectedMembers", "清单必须精确包含当前执行实例")
	}
	return nil
}

func (s *Service) SwitchLarkAccount(ctx context.Context, p auth.Principal, task uuid.UUID, in taskmodel.TaskLarkSwitchInput) (taskmodel.TaskLarkSwitchReceipt, error) {
	var zero taskmodel.TaskLarkSwitchReceipt
	if p.Kind != auth.KindConsole {
		return zero, apperr.Forbidden("任务凭证切换仅限控制台")
	}
	if task == uuid.Nil {
		return zero, apperr.Invalid("taskId", "任务ID无效")
	}
	if err := validateLarkSwitch(in); err != nil {
		return zero, err
	}
	if s.Store == nil {
		return zero, larkUnavailable()
	}
	r := store.TaskLarkFenceReservation{TaskID: task, ActionID: in.ActionID, NodeRunID: in.ExpectedNodeRunID, ExpectedTaskVersion: in.ExpectedTaskVersion, Capture: &store.TaskLarkSwitchCapture{ExpectedBindingRevision: in.ExpectedLarkBindingRevision, Target: store.TaskLarkTarget{AccountID: in.TargetAccountID, VersionID: in.TargetVersionID, AccountRevision: in.ExpectedAccountRevision}}}
	for _, m := range in.ExpectedMembers {
		role := "followup"
		if m.AgentID == in.ExpectedAgentID && m.RuntimeID == in.ExpectedRuntimeID {
			role = "publisher"
		}
		r.Members = append(r.Members, store.TaskLarkFenceMember{AgentID: m.AgentID, RuntimeID: m.RuntimeID, Role: role})
		r.Capture.Members = append(r.Capture.Members, store.TaskLarkMemberCapture{AgentID: m.AgentID, RuntimeID: m.RuntimeID, Role: role, PreviousRevision: m.ExpectedBindingRevision})
	}
	// Existing actions must remain recoverable without today's runtime assets.
	// Verify the original request hash through its real idempotency lock, but
	// never enter B/C again or recapture account current on an accepted replay.
	if _, err := s.Store.TaskLarkAction(ctx, task, larkActor(p), in.ActionID); err == nil {
		if _, _, err = s.ReserveLarkSwitch(ctx, p, r); err != nil {
			return zero, err
		}
		return s.LarkSwitchAction(ctx, p, task, in.ActionID)
	} else if !store.IsNotFound(err) {
		return zero, err
	}
	// No successful default and no NEW reservation if actual managers are absent.
	ex, ok := s.Executor.(*local.Executor)
	if !ok || s.LarkSwitchNative == nil {
		return zero, larkUnavailable()
	}
	_, created, err := s.ReserveLarkSwitch(ctx, p, r)
	if err != nil {
		return zero, err
	}
	if !created {
		return s.LarkSwitchAction(ctx, p, task, in.ActionID)
	} // never repeat B/C
	h := &taskLarkHeld{key: larkSwitchKey{task, in.ActionID}, actor: larkActor(p)}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range r.Members {
		h.ids = append(h.ids, m.RuntimeID)
	}
	slices.SortFunc(h.ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	s.larkMu.Lock()
	if s.larkHeld == nil {
		s.larkHeld = map[larkSwitchKey]*taskLarkHeld{}
	}
	s.larkHeld[h.key] = h
	s.larkMu.Unlock()
	_, err = s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		_, e := m.Tx.Exec(ctx, `UPDATE task_lark_switch_actions SET status='handoff' WHERE task_id=$1 AND actor_key=$2 AND action_id=$3 AND status='pending'`, task, h.actor, in.ActionID)
		return nil, e
	})
	if err != nil {
		return zero, larkUnavailable()
	}
	h.native, err = s.LarkSwitchNative.Acquire(ctx, h.ids)
	if err != nil {
		if h.native == nil {
			if e := s.abortLarkSwitch(h, "consumer_busy"); e != nil {
				return zero, e
			}
			if errors.Is(err, ErrTaskLarkNativeBusy) {
				return zero, conflict("原实例存在活动native工作，未切换账号")
			}
			return zero, larkUnavailable()
		}
		return s.LarkSwitchAction(ctx, p, task, in.ActionID)
	}
	h.local, err = acquireTaskLarkHandoffBeforeStop(ctx, ex, h.ids, func(lease *local.RuntimeHandoff) error {
		return s.captureLarkSwitchRecovery(ctx, h, lease)
	})
	if err != nil {
		if h.local == nil {
			if e := s.abortLarkSwitch(h, "runtime_busy"); e != nil {
				return zero, e
			}
			if errors.Is(err, local.ErrRuntimeBusy) {
				return zero, conflict("原实例正在执行或交接，未切换账号")
			}
			return zero, larkUnavailable()
		}
		if errors.Is(err, local.ErrRuntimeSessionMissing) {
			if e := s.abortLarkSwitch(h, "session_unavailable"); e != nil {
				return zero, e
			}
			return zero, conflict("原会话不可接续，未创建替代会话")
		}
		return s.LarkSwitchAction(ctx, p, task, in.ActionID)
	}
	if err = h.native.Seal(ctx); err != nil {
		return s.LarkSwitchAction(ctx, p, task, in.ActionID)
	}
	h.ready = true
	h.attempted = true
	_, err = s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		receipt, e := s.larkSwitchReceiptTx(ctx, m, h.actor, in.ActionID)
		if e != nil {
			return nil, e
		}
		rev := in.ExpectedLarkBindingRevision + 1
		run := uuid.Must(uuid.NewV7())
		next, prompt, e := newTaskLarkContinuation(m.Record, in.ActionID, run, rev, time.Now().UTC())
		if e != nil {
			return nil, e
		}
		receipt.Status = "succeeded"
		receipt.BindingRevision = &rev
		receipt.Continuation = &taskmodel.TaskLarkSwitchContinuation{NodeRunID: next.ID, Attempt: next.Attempt, RunID: run}
		receipt.RecoveryInstruction = &prompt
		queued := "queued"
		receipt.DispatchStatus = &queued
		wire, e := json.Marshal(receipt)
		if e != nil {
			return nil, e
		}
		_, _, e = s.Store.PublishTaskLarkSwitchTx(ctx, m, h.actor, in.ActionID, next, run, prompt, wire)
		return nil, e
	})
	// Whether C returned success or error, confirm its original action under a
	// fresh task lock. Never infer rollback from the call's transport outcome.
	_ = err
	if err = s.confirmLarkSwitch(h); err != nil {
		return zero, err
	}
	return s.LarkSwitchAction(ctx, p, task, in.ActionID)
}

func (s *Service) releaseLarkSwitch(h *taskLarkHeld) error {
	if h.released {
		return nil
	}
	if h.local != nil {
		if err := h.local.Close(); err != nil {
			return larkUnavailable()
		}
	}
	if h.native != nil {
		h.native.Release()
	}
	h.released = true
	s.larkMu.Lock()
	delete(s.larkHeld, h.key)
	s.larkMu.Unlock()
	return nil
}

func (s *Service) abortLarkSwitch(h *taskLarkHeld, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.larkRecordTimeout())
	defer cancel()
	_, err := s.Store.MutateTask(ctx, h.key.task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return nil, s.Store.FailTaskLarkFenceTx(ctx, m, h.actor, h.key.action, reason)
	})
	if err != nil {
		return larkUnavailable()
	}
	return s.releaseLarkSwitch(h)
}

func (s *Service) confirmLarkSwitch(h *taskLarkHeld) error {
	if h.released {
		return nil
	}
	if !h.ready || !h.attempted {
		return larkUnavailable()
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.larkRecordTimeout())
	defer cancel()
	var receipt taskmodel.TaskLarkSwitchReceipt
	_, err := s.Store.MutateTask(ctx, h.key.task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var e error
		receipt, e = s.larkSwitchReceiptTx(ctx, m, h.actor, h.key.action)
		return nil, e
	})
	if err != nil {
		return larkUnavailable()
	}
	if receipt.Status == "succeeded" {
		if err = s.releaseLarkSwitch(h); err != nil {
			return err
		}
		s.Kick(h.key.task)
		return nil
	}
	// Acquiring the aggregate lock above also waited for the previous C to end:
	// pending/handoff now proves C did not publish, not a latest-run guess.
	return s.abortLarkSwitch(h, "final_validation_failed")
}

func (s *Service) LarkSwitchAction(ctx context.Context, p auth.Principal, task, action uuid.UUID) (taskmodel.TaskLarkSwitchReceipt, error) {
	var out taskmodel.TaskLarkSwitchReceipt
	if p.Kind != auth.KindConsole {
		return out, apperr.Forbidden("任务凭证切换仅限控制台")
	}
	_, err := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var e error
		out, e = s.larkSwitchReceiptTx(ctx, m, larkActor(p), action)
		return nil, e
	})
	return out, err
}

func (s *Service) larkSwitchReceiptTx(ctx context.Context, m *store.TaskMutation, actor string, action uuid.UUID) (taskmodel.TaskLarkSwitchReceipt, error) {
	var out taskmodel.TaskLarkSwitchReceipt
	var captureRaw, receiptRaw []byte
	var failure *string
	err := m.Tx.QueryRow(ctx, `SELECT task_id,action_id,node_run_id,status,reservation_task_version,captured_request,receipt,failure_reason,created_at,finished_at FROM task_lark_switch_actions WHERE task_id=$1 AND actor_key=$2 AND action_id=$3`, m.Record.View.Task.ID, actor, action).Scan(&out.TaskID, &out.ActionID, &out.SourceNodeRunID, &out.Status, &out.ReservationTaskVersion, &captureRaw, &receiptRaw, &failure, &out.CreatedAt, &out.FinishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, apperr.NotFound("Lark切换动作")
		}
		return out, err
	}
	var capture store.TaskLarkSwitchCapture
	if json.Unmarshal(captureRaw, &capture) != nil || len(capture.Members) != 2 {
		return out, errors.New("invalid task Lark capture")
	}
	out.Target = taskmodel.TaskLarkSwitchTarget{AccountID: capture.Target.AccountID, VersionID: capture.Target.VersionID, AccountRevision: capture.Target.AccountRevision, Identity: capture.Target.Identity.Clone(), RuntimeVariantVersion: capture.Target.RuntimeVariantVersion}
	out.Members = []taskmodel.TaskLarkSwitchMember{}
	for _, member := range capture.Members {
		out.Members = append(out.Members, taskmodel.TaskLarkSwitchMember{AgentID: member.AgentID, RuntimeID: member.RuntimeID, Role: member.Role, PreviousBindingRevision: member.PreviousRevision, BaseSnapshotSHA: member.BaseSnapshotSHA})
		if member.Role == "publisher" {
			out.SourceAgentID, out.SourceRuntimeID = member.AgentID, member.RuntimeID
		}
	}
	if out.Status == "succeeded" {
		var saved taskmodel.TaskLarkSwitchReceipt
		if json.Unmarshal(receiptRaw, &saved) != nil || saved.Continuation == nil || saved.BindingRevision == nil {
			return out, errors.New("invalid committed task Lark receipt")
		}
		out.BindingRevision, out.Continuation, out.RecoveryInstruction = saved.BindingRevision, saved.Continuation, saved.RecoveryInstruction
		var status string
		if err = m.Tx.QueryRow(ctx, `SELECT status FROM task_turns WHERE id=$1 AND task_id=$2 AND node_run_id=$3`, out.Continuation.RunID, out.TaskID, out.Continuation.NodeRunID).Scan(&status); err != nil {
			return out, err
		}
		out.DispatchStatus = &status
	}
	if out.Status == "failed" || failure != nil {
		code, message := "task_conflict", "任务凭证交接未发布新绑定，原任务成果保留"
		out.ErrorCode, out.ErrorMessage = &code, &message
	}
	return out, nil
}

func (s *Service) ReconcileLarkSwitchOutcomes(ctx context.Context) error {
	s.larkMu.Lock()
	held := make([]*taskLarkHeld, 0, len(s.larkHeld))
	for _, h := range s.larkHeld {
		held = append(held, h)
	}
	s.larkMu.Unlock()
	var errs []error
	for _, h := range held {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !h.mu.TryLock() {
			continue
		}
		if h.ready && h.attempted {
			if err := s.confirmLarkSwitch(h); err != nil {
				errs = append(errs, err)
			}
		} else if !h.attempted && h.local != nil && h.native != nil {
			// Recheck only this already-owned old generation's cleanup. Once a
			// pre-C wait/Seal failed, later confirmation aborts A; it never
			// automatically resumes QA or repeats publication C.
			if err := s.finishUnconfirmedLarkAbort(h); err != nil {
				errs = append(errs, err)
			}
		}
		h.mu.Unlock()
	}
	return errors.Join(errs...)
}

func (s *Service) finishUnconfirmedLarkAbort(h *taskLarkHeld) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.larkRecordTimeout())
	defer cancel()
	for _, id := range h.ids {
		handle, err := h.local.Handle(id)
		if err != nil {
			return larkUnavailable()
		}
		if err = h.local.StopIdle(ctx, id, handle); err != nil {
			return larkUnavailable()
		}
	}
	if err := h.native.Seal(ctx); err != nil {
		return larkUnavailable()
	}
	return s.abortLarkSwitch(h, "handoff_confirmation_failed")
}
