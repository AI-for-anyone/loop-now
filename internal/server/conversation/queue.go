package conversation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

// 控制台的排队动作：逐条列出、转为打断、撤回或取消、停止当前回复。写操作都带 actionId：
// 同一动作重放返回首次回执且不再产生副作用；Redis、事件与打断只在动作真正生效的那次提交后执行。

// Queue 返回排队列表，按实际处理顺序。
func (s *Service) Queue(ctx context.Context, agentID uuid.UUID) (apitypes.AgentQueue, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return apitypes.AgentQueue{}, err
	}
	items, err := s.st.AgentQueue(ctx, agentID)
	if err != nil {
		return apitypes.AgentQueue{}, err
	}
	return apitypes.AgentQueue{AgentID: agentID, Items: apitypes.FromQueueItems(items)}, nil
}

// queueAction 在一个事务中执行排队动作：fn 返回回执（status 之外的部分），这里补上动作信息与动作后的排队列表。
func (s *Service) queueAction(ctx context.Context, agentID, actionID uuid.UUID, op string, target uuid.UUID,
	fn func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error)) (json.RawMessage, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"target": target})
	action := store.AgentAction{ActorKey: "console", ID: actionID, Operation: op, Body: body}
	out, _, err := s.st.AgentQueueAction(ctx, agentID, action, func(tx pgx.Tx) (any, error) {
		r, err := fn(tx)
		if err != nil {
			return nil, err
		}
		items, err := s.st.AgentQueueTx(ctx, tx, agentID)
		if err != nil {
			return nil, err
		}
		r.ActionID, r.AgentID, r.Operation, r.Queue = actionID, agentID, op, apitypes.FromQueueItems(items)
		return r, nil
	})
	return out, err
}

func receiptItem(it store.QueueItem) *apitypes.QueueItem {
	v := apitypes.FromQueueItem(it)
	return &v
}

func receiptRun(r store.Run) *apitypes.Run {
	v := apitypes.FromRun(r)
	return &v
}

// PromoteToInterrupt 把控制台发出、仍在普通排队的执行转为打断，效果与发送时选择打断一致：
// 先在数据库里排到已有打断之后、全部普通排队之前，再原子地移动 Redis 队列，然后打断进行中的回复。
func (s *Service) PromoteToInterrupt(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	var run store.Run
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueuePromoteRun, runID, func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, item, ch, err := s.st.PromoteQueuedRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		run, changed = r, ch
		status := "accepted"
		if !ch {
			status = "already_interrupt"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(item), Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		if _, err := s.co.Promote(ctx, agentID.String(), run.ID.String()); err != nil {
			s.log.Warn("移到打断队列失败，等待对账按数据库重建", "agentId", agentID, "runId", run.ID, "err", err)
		}
		s.emitRun(ctx, agentID, run.ID)
		s.emitQueue(ctx, agentID)
		s.interruptActive(ctx, agentID, run.ID)
		s.kick(ctx, agentID)
	}
	return out, err
}

// CancelRun 撤回控制台发出、仍在排队的消息：Agent 不会收到。回执的 item 带原文与图片，供“编辑”回填。
func (s *Service) CancelRun(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueCancelRun, runID, func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, item, ch, err := s.st.CancelQueuedRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		changed = ch
		status := "accepted"
		if !ch {
			status = "already_cancelled"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(item), Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		s.dequeued(ctx, agentID, runID)
		s.emitQueue(ctx, agentID)
	}
	return out, err
}

// StopRun 只停止这一次进行中的回复，不发送新消息：沿用打断路径（清空 Pi 队列、abort、超时强制结束），之后排队的消息照常处理。
func (s *Service) StopRun(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueStopRun, runID, func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, ch, err := s.st.StopRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		changed = ch
		status := "accepted"
		if !ch {
			status = "already_stopping"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		s.emitRun(ctx, agentID, runID)
		if err := s.co.PublishInterrupt(ctx, agentID.String(), runID.String()); err != nil {
			s.log.Warn("发布停止命令失败，等待驱动循环的定时检查", "agentId", agentID, "err", err)
		}
	}
	return out, err
}

// PromoteInboxMessage 把排队中的一条 Inbox 消息（任何发件人）转为打断：移到排队中的打断提醒（没有就新建），
// 原提醒空了即作废，然后打断进行中的回复。
func (s *Service) PromoteInboxMessage(ctx context.Context, agentID, messageID, actionID uuid.UUID) (json.RawMessage, error) {
	var res store.InboxPromotion
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueuePromoteInboxMessage, messageID, func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error) {
		var err error
		res, err = s.st.PromoteInboxMessageTx(ctx, tx, agentID, messageID, s.o.InstanceID, func(r store.Run) error {
			return s.co.PushInterrupt(ctx, agentID.String(), r.ID.String())
		})
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		status := "accepted"
		if !res.Changed {
			status = "already_interrupt"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(res.Item), Run: receiptRun(res.Target)}, nil
	})
	if err == nil && res.Changed {
		if res.Created {
			if m, err := s.st.GetMessage(ctx, res.Target.UserMessageID); err == nil {
				s.emitMessage(ctx, agentID, m, res.Target)
			}
		}
		if res.Voided != nil {
			s.dequeued(ctx, agentID, *res.Voided)
		}
		s.emitRun(ctx, agentID, res.Target.ID)
		s.emitQueue(ctx, agentID)
		s.interruptActive(ctx, agentID, res.Target.ID)
		s.kick(ctx, agentID)
	}
	return out, err
}

// CancelInboxMessage 由人取消排队中的一条 Inbox 消息（任何发件人）：收件 Agent 不再处理，承载它的提醒空了即作废。
func (s *Service) CancelInboxMessage(ctx context.Context, agentID, messageID, actionID uuid.UUID) (json.RawMessage, error) {
	var res store.InboxCancellation
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueCancelInboxMessage, messageID, func(tx pgx.Tx) (apitypes.AgentQueueActionReceipt, error) {
		var err error
		if res, err = s.st.CancelInboxMessageTx(ctx, tx, agentID, messageID); err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		r := apitypes.AgentQueueActionReceipt{Status: "accepted", Item: receiptItem(res.Item)}
		if !res.Changed {
			r.Status = "already_cancelled"
		}
		if res.Run != nil {
			r.Run = receiptRun(*res.Run)
		}
		return r, nil
	})
	if err == nil && res.Changed {
		if res.Voided && res.Run != nil {
			s.dequeued(ctx, agentID, res.Run.ID)
		}
		s.emitQueue(ctx, agentID)
		if s.InboxCancelled != nil {
			s.InboxCancelled(ctx, res.Message)
		}
	}
	return out, err
}

// InboxRead 在收件 Agent 读取 Inbox 消息之后由 Inbox 服务调用：读走的消息离开排队列表，
// 因此作废的提醒从 Redis 队列移除并推送。
func (s *Service) InboxRead(ctx context.Context, eff store.InboxReadEffect) {
	if !eff.QueueChanged || eff.AgentID == uuid.Nil {
		return
	}
	for _, id := range eff.Voided {
		s.dequeued(ctx, eff.AgentID, id)
	}
	s.emitQueue(ctx, eff.AgentID)
}

// dequeued 把撤回或作废的执行从 Redis 队列移除并推送它的新状态。
func (s *Service) dequeued(ctx context.Context, agentID, runID uuid.UUID) {
	if err := s.co.Remove(ctx, agentID.String(), runID.String()); err != nil {
		s.log.Warn("移出 Redis 队列失败，出队时按数据库状态跳过", "agentId", agentID, "runId", runID, "err", err)
	}
	s.emitRun(ctx, agentID, runID)
}
