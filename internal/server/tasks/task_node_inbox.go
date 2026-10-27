package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"strings"
	"unicode/utf8"
)

func (s *Service) SendNodeInboxMessage(ctx context.Context, p auth.Principal, task, node uuid.UUID, in taskmodel.TaskNodeMessageInput, maxBytes int) (json.RawMessage, error) {
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("只有控制台可发送严格节点Inbox")
	}
	if task == uuid.Nil || node == uuid.Nil || in.ActionID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil || !utf8.ValidString(in.Text) || (strings.TrimSpace(in.Text) == "" && len(in.ImageIDs) == 0) {
		return nil, apperr.Invalid("body", "动作、目标和正文必须有效")
	}
	if maxBytes < 1 || len(in.Text) > maxBytes {
		return nil, apperr.TooLarge("Inbox正文超限")
	}
	if in.WhenBusy == "" {
		in.WhenBusy = "queue"
	}
	if in.WhenBusy != "queue" && in.WhenBusy != "interrupt" {
		return nil, apperr.Invalid("whenBusy", "仅支持queue或interrupt")
	}
	target := store.TaskConsoleLineage{TaskID: task, NodeRunID: node, AgentID: in.ExpectedAgentID, RuntimeID: in.ExpectedRuntimeID}
	action := bodyAction(p, in.ActionID, "sendTaskNodeInboxMessage", struct {
		NodeID uuid.UUID
		Input  taskmodel.TaskNodeMessageInput
	}{node, in})
	action.Authorize = func(m *store.TaskMutation) error { _, e := consoleNodeTarget(m, node); return e }
	created := false
	var messageID uuid.UUID
	var interrupted []uuid.UUID
	out, e := s.Store.MutateTask(ctx, task, action, func(m *store.TaskMutation) (any, error) {
		if s.larkDispatchPaused(target.RuntimeID) {
			return nil, store.TaskConsoleConflict(m, target, "chat_unavailable")
		}
		rec, ids, e := s.Store.EnqueueTaskNodeInboxTx(ctx, m, target, in.Text, in.WhenBusy, in.ImageIDs...)
		if e != nil {
			return nil, e
		}
		created = true
		var wire struct {
			ID uuid.UUID `json:"id"`
		}
		if e = json.Unmarshal(rec.Message, &wire); e != nil {
			return nil, e
		}
		messageID = wire.ID
		interrupted = ids
		return taskmodel.TaskNodeInboxReceipt{ActionID: in.ActionID, TaskID: task, NodeRunID: node, AgentID: target.AgentID, RuntimeID: target.RuntimeID, Message: rec.Message, Status: "accepted"}, nil
	})
	if e != nil {
		if a, ok := apperr.As(e); ok && a.Code == apperr.CodeTaskConflict && a.Details == nil {
			if r, re := s.Store.GetTask(ctx, task); re == nil {
				return nil, store.TaskConsoleConflict(&store.TaskMutation{Record: &r}, target, "action_conflict")
			}
		}
		return nil, e
	}
	if created {
		if s.Coord != nil {
			if msg, re := s.Store.GetInboxMessage(ctx, messageID, 160); re == nil {
				_ = s.Store.WithInboxEventLock(ctx, func(unread int) error {
					data, e := json.Marshal(apitypes.SseInboxMessageCreated{Message: apitypes.FromInboxSummary(msg), UnreadTotal: unread})
					if e != nil {
						return e
					}
					_, e = s.Coord.AppendInbox(ctx, apitypes.EventInboxCreated, data)
					return e
				})
			}
		}
		for _, run := range interrupted {
			if s.Coord != nil {
				_ = s.Coord.PublishInterrupt(ctx, target.RuntimeID.String(), run.String())
			}
			if s.Executor != nil {
				_ = s.Executor.Interrupt(ctx, target.RuntimeID, run)
			}
		}
		s.Kick(task)
		s.KickInbox(target.RuntimeID)
	}
	return out, nil
}

// nodeQueueAction runs one node queue action (promote, cancel, stop) under the
// node write-action contract: same actionId replays the stored receipt, a
// different body under the same actionId is action_conflict. Side effects
// (executor interrupts, kicks, Inbox events) run only for the request that
// actually changed something.
func (s *Service) nodeQueueAction(ctx context.Context, p auth.Principal, task, node, subject uuid.UUID, in taskmodel.TaskNodeQueueActionInput, op, operation string,
	fn func(m *store.TaskMutation, target store.TaskConsoleLineage) (store.TaskNodeQueueResult, error)) (json.RawMessage, error) {
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("只有控制台可调整节点排队")
	}
	if task == uuid.Nil || node == uuid.Nil || subject == uuid.Nil || in.ActionID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil {
		return nil, apperr.Invalid("body", "动作、目标和对象必须有效")
	}
	target := store.TaskConsoleLineage{TaskID: task, NodeRunID: node, AgentID: in.ExpectedAgentID, RuntimeID: in.ExpectedRuntimeID}
	action := bodyAction(p, in.ActionID, op, struct {
		NodeID, Subject uuid.UUID
		Input           taskmodel.TaskNodeQueueActionInput
	}{node, subject, in})
	action.Authorize = func(m *store.TaskMutation) error { _, e := consoleNodeTarget(m, node); return e }
	var res store.TaskNodeQueueResult
	out, e := s.Store.MutateTask(ctx, task, action, func(m *store.TaskMutation) (any, error) {
		var e error
		if res, e = fn(m, target); e != nil {
			return nil, e
		}
		status := "accepted"
		if !res.Changed {
			status = map[string]string{"promote_inbox_message": "already_interrupt", "cancel_inbox_message": "already_cancelled", "stop_run": "already_stopping"}[operation]
		}
		receipt := taskmodel.TaskNodeQueueActionReceipt{ActionID: in.ActionID, TaskID: task, NodeRunID: node, AgentID: target.AgentID, RuntimeID: target.RuntimeID,
			Operation: operation, Status: status, RunID: res.Turn.ID}
		if operation != "stop_run" {
			id := subject
			receipt.MessageID = &id
		}
		if res.Item != nil {
			it := s.Store.TaskQueueItem(*res.Item)
			receipt.Item = &it
		}
		return receipt, nil
	})
	if e != nil {
		if a, ok := apperr.As(e); ok && a.Code == apperr.CodeTaskConflict && a.Details == nil {
			if r, re := s.Store.GetTask(ctx, task); re == nil {
				return nil, store.TaskConsoleConflict(&store.TaskMutation{Record: &r}, target, "action_conflict")
			}
		}
		return nil, e
	}
	if res.Changed {
		for _, run := range res.Interrupted {
			if s.Coord != nil {
				_ = s.Coord.PublishInterrupt(ctx, target.RuntimeID.String(), run.String())
			}
			if s.Executor != nil {
				_ = s.Executor.Interrupt(ctx, target.RuntimeID, run)
			}
		}
		if operation == "cancel_inbox_message" && s.InboxCancelled != nil {
			if m, e := s.Store.GetInboxMessage(ctx, subject, 200); e == nil {
				s.InboxCancelled(ctx, m)
			}
		}
		s.Kick(task)
		s.KickInbox(target.RuntimeID)
	}
	return out, nil
}

// PromoteNodeInboxMessage turns one queued node message, from any sender, into
// an interrupt and interrupts the node's current reply.
func (s *Service) PromoteNodeInboxMessage(ctx context.Context, p auth.Principal, task, node, message uuid.UUID, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error) {
	return s.nodeQueueAction(ctx, p, task, node, message, in, "promoteTaskNodeInboxMessage", "promote_inbox_message", func(m *store.TaskMutation, target store.TaskConsoleLineage) (store.TaskNodeQueueResult, error) {
		if s.larkDispatchPaused(target.RuntimeID) {
			return store.TaskNodeQueueResult{}, store.TaskConsoleConflict(m, target, "chat_unavailable")
		}
		return s.Store.PromoteTaskNodeMessageTx(ctx, m, target, message, func(trigger uuid.UUID) (store.TaskTurn, error) {
			return s.enqueueTaskInboxReminder(ctx, m, store.TaskInstance{ID: target.RuntimeID, AgentID: target.AgentID, TaskID: task}, 0, trigger)
		})
	})
}

// CancelNodeQueueMessage cancels one queued node message from any sender; the
// node Agent never processes it.
func (s *Service) CancelNodeQueueMessage(ctx context.Context, p auth.Principal, task, node, message uuid.UUID, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error) {
	return s.nodeQueueAction(ctx, p, task, node, message, in, "cancelTaskNodeInboxMessage", "cancel_inbox_message", func(m *store.TaskMutation, target store.TaskConsoleLineage) (store.TaskNodeQueueResult, error) {
		return s.Store.CancelTaskNodeMessageTx(ctx, m, target, message)
	})
}

// StopNodeRun stops only the node's current turn without sending a message.
func (s *Service) StopNodeRun(ctx context.Context, p auth.Principal, task, node, run uuid.UUID, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error) {
	return s.nodeQueueAction(ctx, p, task, node, run, in, "stopTaskNodeRun", "stop_run", func(m *store.TaskMutation, target store.TaskConsoleLineage) (store.TaskNodeQueueResult, error) {
		return s.Store.StopTaskNodeRunTx(ctx, m, target, run)
	})
}

// NodeQueue is the node's pending messages, one per item, in claim order.
func (s *Service) NodeQueue(ctx context.Context, p auth.Principal, task, node uuid.UUID) (taskmodel.TaskNodeQueue, error) {
	var out taskmodel.TaskNodeQueue
	if p.Kind != auth.KindConsole {
		return out, apperr.Forbidden("只有控制台可读节点Inbox排队")
	}
	_, e := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		if _, e := consoleNodeTarget(m, node); e != nil {
			return nil, e
		}
		var e error
		out, e = s.Store.TaskNodeQueueTx(ctx, m, node)
		return nil, e
	})
	return out, e
}

func (s *Service) NodeInboxMessages(ctx context.Context, p auth.Principal, task, node uuid.UUID, cursor string, limit int) (taskmodel.TaskNodeInboxPage, error) {
	var out taskmodel.TaskNodeInboxPage
	if p.Kind != auth.KindConsole {
		return out, apperr.Forbidden("只有控制台可读节点Inbox历史")
	}
	_, e := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		if _, e := consoleNodeTarget(m, node); e != nil {
			return nil, e
		}
		var e error
		out, e = s.Store.TaskNodeInboxPageTx(ctx, m, node, cursor, limit)
		return nil, e
	})
	return out, e
}

func (s *Service) NodeInboxAction(ctx context.Context, p auth.Principal, task, node, action uuid.UUID) (json.RawMessage, error) {
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("只有控制台可查询节点Inbox动作")
	}
	actor := string(p.Kind) + ":" + p.ID.String() + ":" + p.RuntimeID.String()
	var raw json.RawMessage
	e := s.Store.Pool.QueryRow(ctx, `SELECT response FROM task_actions WHERE actor_key=$1 AND action_id=$2 AND task_id=$3 AND operation='sendTaskNodeInboxMessage'`, actor, action, task).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, apperr.NotFound("节点Inbox动作")
	}
	if e != nil {
		return nil, e
	}
	var receipt taskmodel.TaskNodeInboxReceipt
	if json.Unmarshal(raw, &receipt) != nil || receipt.TaskID != task || receipt.NodeRunID != node {
		return nil, apperr.NotFound("节点Inbox动作")
	}
	return raw, nil
}
