package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// mirrorQueue 可随时从PG重建；仅镜像该任务实例，不改常态队列。
func (s *Service) mirrorQueue(ctx context.Context, runtime uuid.UUID) error {
	if s.Coord == nil {
		return nil
	}
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	rows, err := s.Store.Pool.Query(ctx, `SELECT id,priority FROM task_turns WHERE runtime_id=$1 AND status='queued' ORDER BY queue_seq`, runtime)
	if err != nil {
		return err
	}
	interrupt, normal := []string{}, []string{}
	for rows.Next() {
		var id uuid.UUID
		var priority int
		if err = rows.Scan(&id, &priority); err != nil {
			rows.Close()
			return err
		}
		if priority == 0 {
			interrupt = append(interrupt, id.String())
		} else {
			normal = append(normal, id.String())
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return s.Coord.RebuildQueues(ctx, runtime.String(), interrupt, normal)
}
func (s *Service) executeTurn(ctx context.Context, turn store.TaskTurn, sink executor.Sink) (result executor.Result, err error) {
	valid, guardErr := s.Store.GuardTaskConsoleDispatch(ctx, turn)
	if guardErr != nil {
		return executor.Result{}, guardErr
	}
	if !valid {
		return executor.Result{Outcome: executor.OutcomeInterrupted, ErrorCode: "task_conflict", ErrorMessage: "原节点或执行条件已变化，该消息未执行"}, nil
	}
	inst, e := s.Store.TaskInstance(ctx, turn.RuntimeID)
	if e != nil {
		return executor.Result{}, e
	}

	if s.Log != nil {
		log := s.Log.With("agentId", inst.AgentID, "runtimeId", turn.RuntimeID, "taskId", turn.TaskID, "nodeRunId", taskNodeRef(turn.NodeRunID), "runId", turn.ID, "instanceId", s.Store.InstanceID, "inboxMessageId", turn.TriggerInboxID, "requestId", turn.RequestID)
		begin := time.Now()
		queuedMs := int64(0)
		if turn.StartedAt != nil {
			begin = *turn.StartedAt
			queuedMs = turn.StartedAt.Sub(turn.QueuedAt).Milliseconds()
		}
		whenBusy := "queue"
		if turn.Priority == 0 {
			whenBusy = "interrupt"
		}
		log.Info("任务回复开始", "whenBusy", whenBusy, "queuedMs", queuedMs)
		defer func() {
			durationMs := time.Since(begin).Milliseconds()
			timeout := s.Options.RecordTimeout
			if timeout <= 0 {
				timeout = 5 * time.Second
			}
			recordCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			var input, output, read, write int64
			e := s.Store.Pool.QueryRow(recordCtx, `SELECT coalesce(sum(input),0),coalesce(sum(output),0),coalesce(sum(cache_read),0),coalesce(sum(cache_write),0) FROM usage_records WHERE task_turn_id=$1`, turn.ID).Scan(&input, &output, &read, &write)
			outcome, code := result.Outcome, result.ErrorCode
			if err != nil {
				outcome = "failed"
				code = executor.ErrInternal
			}
			if ctx.Err() != nil {
				outcome = "interrupted"
			}
			endReason := ""
			if outcome == "interrupted" {
				endReason = "user_interrupt"
				if ctx.Err() != nil {
					endReason = "server_shutdown"
					if r, e := s.Store.GetTask(recordCtx, turn.TaskID); e == nil {
						if r.View.Task.Status == "cancelled" {
							endReason = "task_cancelled"
						}
						if r.View.Task.Status == "completed" {
							endReason = "task_completed"
						}
					}
				}
			}
			attrs := []any{"outcome", outcome, "status", outcome, "endReason", endReason, "durationMs", durationMs, "errorCode", code}
			if e != nil {
				log.Warn("汇总任务回复用量失败", "error", e)
			}
			if e == nil {
				attrs = append(attrs, "input", input, "output", output, "cacheRead", read, "cacheWrite", write, "total", input+output+read+write, "tokens", input+output+read+write)
			}
			log.Info("任务回复结束", attrs...)
		}()
	}

	images, imageNote, imageErr := s.Store.TaskTurnImageInput(ctx, turn)
	if imageErr != nil {
		return executor.Result{}, imageErr
	}
	if imageNote != "" {
		turn.Text += "\n" + imageNote
	}
	if s.Coord == nil {
		return s.Executor.Execute(ctx, executor.Request{Images: images, AgentID: turn.RuntimeID, RunID: turn.ID, Message: fmt.Sprintf("本轮运行ID：%s。\n%s", turn.ID, taskWakeText(turn)), Origin: turn.Origin, MessageID: turnTrigger(turn)}, sink)
	}
	owner := coord.LockValue(turn.ID.String(), s.Options.InstanceID.String())
	key := turn.RuntimeID.String()
	if e := s.mirrorQueue(ctx, turn.RuntimeID); e != nil {
		return executor.Result{}, e
	}
	ok, e := s.Coord.Renew(ctx, key, owner)
	if e != nil || !ok {
		return executor.Result{}, errors.New("任务实例忙闲锁不可用")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	commands, e := s.Coord.SubscribeInterrupts(child, key)
	if e != nil {
		return executor.Result{}, e
	}
	done := make(chan struct{})
	defer func() {
		cancel()
		<-done
		release, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
		defer stop()
		_, _ = s.Coord.Release(release, key, owner)
	}()
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.Options.LockRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-child.Done():
				return
			case id, ok := <-commands:
				if !ok {
					commands = nil
					continue
				}
				if id == turn.ID.String() {
					_ = s.Executor.Interrupt(child, turn.RuntimeID, turn.ID)
				}
			case <-ticker.C:
				ok, e := s.Coord.Renew(child, key, owner)
				if e != nil || !ok {
					cancel()
					return
				}
				var aborting bool
				if e = s.Store.Pool.QueryRow(child, `SELECT status='aborting' FROM task_turns WHERE id=$1`, turn.ID).Scan(&aborting); e == nil && aborting {
					_ = s.Executor.Interrupt(child, turn.RuntimeID, turn.ID)
				}
			}
		}
	}()
	return s.Executor.Execute(child, executor.Request{Images: images, AgentID: turn.RuntimeID, RunID: turn.ID, Message: fmt.Sprintf("本轮运行ID：%s。\n%s", turn.ID, taskWakeText(turn)), Origin: turn.Origin, MessageID: turnTrigger(turn)}, sink)
}

func taskWakeText(turn store.TaskTurn) string {
	if turn.WakeSleepID != nil && turn.Origin != "sleep_wake" {
		return "平台通知：sleep已因新工作结束。\n" + turn.Text
	}
	return turn.Text
}

// turnTrigger 是触发这一轮的消息：Inbox 提醒为那条 Inbox 消息，其余为这一轮的用户消息。
func turnTrigger(turn store.TaskTurn) uuid.UUID {
	if turn.TriggerInboxID != nil {
		return *turn.TriggerInboxID
	}
	return turn.UserMessageID
}
