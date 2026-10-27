package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// KickInbox独立派发非当前/阻塞实例的Inbox，不把它当作workflow节点执行。
func (s *Service) KickInbox(runtime uuid.UUID) {
	s.mu.Lock()
	if !s.started || s.ctx.Err() != nil || s.inboxDrivers[runtime] {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.inboxDrivers[runtime] = true
	s.inboxCancels[runtime] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		var driveErr error
		defer func() {
			s.mu.Lock()
			delete(s.inboxDrivers, runtime)
			delete(s.inboxCancels, runtime)
			stopped := s.ctx.Err() != nil
			s.mu.Unlock()
			if stopped || driveErr != nil {
				return
			}
			if s.larkDispatchPaused(runtime) {
				return
			}
			timeout := s.Options.RecordTimeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			check, stop := context.WithTimeout(context.Background(), timeout)
			defer stop()
			inst, e := s.Store.TaskInstance(check, runtime)
			if e != nil || !inst.Prepared || inst.Retired {
				return
			}
			r, e := s.Store.GetTask(check, inst.TaskID)
			if e != nil || r.Ended() || r.View.Task.Status == "preparing" {
				return
			}
			n := r.Current()
			if n != nil && n.RuntimeID == runtime && n.Status != "blocked" {
				return
			}
			var pending bool
			e = s.Store.Pool.QueryRow(check, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status='queued' AND (origin IN ('inbox_reminder','sleep_wake') OR (origin='console_node' AND execution_mode='task_conversation'))) AND NOT EXISTS(SELECT 1 FROM task_lark_switch_fences WHERE runtime_id=$1) AND NOT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting'))`, runtime).Scan(&pending)
			if e == nil && pending {
				s.KickInbox(runtime)
			}
		}()
		driveErr = s.driveInbox(ctx, runtime)
		if driveErr != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Error("任务Inbox派发失败", "runtimeId", runtime, "error", driveErr)
		}
	}()
}
func (s *Service) driveInbox(ctx context.Context, runtime uuid.UUID) error {
	inst, e := s.Store.TaskInstance(ctx, runtime)
	if e != nil {
		return e
	}
	for ctx.Err() == nil {
		if s.larkDispatchPaused(runtime) {
			return nil
		}
		turn, ok, e := s.Store.ClaimTaskInboxTurn(ctx, inst.TaskID, runtime)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		sink := &turnSink{service: s, ctx: ctx, turn: turn}
		result, e := s.executeTurn(ctx, turn, sink)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			result.Outcome = executor.OutcomeFailed
			result.ErrorCode = executor.ErrInternal
			result.ErrorMessage = "Inbox执行基础设施失败"
		}
		if result.Outcome != "completed" && result.Outcome != "interrupted" && result.Outcome != "failed" {
			result.Outcome = "failed"
		}
		var code, message *string
		if result.ErrorCode != "" {
			code = &result.ErrorCode
		}
		if result.ErrorMessage != "" {
			message = &result.ErrorMessage
		}
		if _, e = s.Store.MutateTask(ctx, inst.TaskID, nil, func(m *store.TaskMutation) (any, error) {
			var err error
			if code, message, err = stoppedError(ctx, m, turn.ID, result.Outcome, code, message); err != nil {
				return nil, err
			}
			if _, err := s.Store.UpdateTaskTurnTx(ctx, m, turn.ID, result.Outcome, result.Text, code, message); err != nil {
				return nil, err
			}
			_, err = s.Store.FinishSleepTx(ctx, m.Tx, m, turn.ID, result.Outcome)
			return nil, err
		}); e != nil {
			return e
		}
		// 不调用节点settle；普通Inbox回合不会推进/重试workflow。
		s.Kick(inst.TaskID)
	}
	return ctx.Err()
}

// taskNodeRef 是日志里的 nodeRunId：没有节点时为空串（不记指针，否则日志里是地址）。
func taskNodeRef(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
