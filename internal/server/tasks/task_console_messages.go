package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

func consoleNodeTarget(m *store.TaskMutation, id uuid.UUID) (store.TaskConsoleLineage, error) {
	for _, n := range m.Record.View.Nodes {
		if n.ID == id {
			return store.TaskConsoleLineage{TaskID: m.Record.View.Task.ID, NodeRunID: n.ID, AgentID: n.AgentID, RuntimeID: n.RuntimeID}, nil
		}
	}
	return store.TaskConsoleLineage{}, apperr.NotFound("任务节点")
}

func (s *Service) ConsoleMessageCapability(ctx context.Context, p auth.Principal, task, node uuid.UUID) (taskmodel.TaskNodeMessageCapability, error) {
	var out taskmodel.TaskNodeMessageCapability
	if p.Kind != auth.KindConsole {
		return out, apperr.Forbidden("只有控制台可查询节点消息能力")
	}
	_, err := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		target, e := consoleNodeTarget(m, node)
		if e != nil {
			return nil, e
		}
		out = taskmodel.TaskNodeMessageCapability{TaskID: task, NodeRunID: node, AgentID: target.AgentID, RuntimeID: target.RuntimeID}
		reason, e := s.Store.TaskConsoleGuardTx(ctx, m, target)
		if e != nil {
			return nil, e
		}
		paused := s.larkDispatchPaused(target.RuntimeID)
		if paused {
			reason = "chat_unavailable"
		}
		out.CanSend = reason == ""
		if !out.CanSend {
			message := "原节点当前不能接收消息，可能已切换或原执行条件不可用"
			hint := "保留原草稿，核对当前节点与原会话、捕获模型授权；不要改投或自动重试"
			switch reason {
			case "task_ended":
				message = "任务已结束，节点历史只读"
			case "node_changed":
				message = "所选执行已不是当前节点，历史只读"
			case "instance_unavailable":
				message = "原任务实例尚未准备完成或已经回收"
			case "target_mismatch":
				message = "原节点与运行实例的归属不匹配"
			case "chat_unavailable":
				message = "原捕获的模型配置或授权当前不可用，请核对原执行资源"
				if m.Record.Current() != nil && m.Record.Current().Status == "blocked" {
					eligible, e := s.Store.TaskConsoleEligibleTx(ctx, m, node)
					if e != nil {
						return nil, e
					}
					if !eligible {
						message = "本次阻塞缺少可确认的执行异常来源，不能直接聊天排查"
					} else {
						var state string
						var file *string
						if e := m.Tx.QueryRow(ctx, `SELECT session_state,session_file FROM task_instances WHERE id=$1`, target.RuntimeID).Scan(&state, &file); e != nil {
							return nil, e
						}
						if state != "active" || file == nil || *file == "" {
							message = "原任务会话当前不可接续，不会自动重开或替换上下文"
						}
					}
				}
			}
			out.Reason = &message
			if paused {
				message = "原实例正在确认凭证交接，请等待原动作结果"
			}
			out.HandlingHint = &hint
		}
		return nil, nil
	})
	return out, err
}

func (s *Service) SendConsoleMessage(ctx context.Context, p auth.Principal, task, node uuid.UUID, in taskmodel.TaskNodeMessageInput, maxTextBytes int) (json.RawMessage, error) {
	if in.ImageIDsPresent || len(in.ImageIDs) > 0 {
		return nil, apperr.Invalid("imageIds", "图片仅支持当前节点Inbox发送入口")
	}
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("只有控制台可发送严格节点消息")
	}
	if in.ActionID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil || task == uuid.Nil || node == uuid.Nil || !utf8.ValidString(in.Text) || strings.TrimSpace(in.Text) == "" {
		return nil, apperr.Invalid("body", "动作、目标及正文必须有效")
	}
	if maxTextBytes <= 0 || len(in.Text) > maxTextBytes {
		return nil, apperr.TooLarge("节点消息正文超限")
	}
	if in.WhenBusy == "" {
		in.WhenBusy = "queue"
	}
	if in.WhenBusy != "queue" && in.WhenBusy != "interrupt" {
		return nil, apperr.Invalid("whenBusy", "仅支持queue或interrupt")
	}
	target := store.TaskConsoleLineage{TaskID: task, NodeRunID: node, AgentID: in.ExpectedAgentID, RuntimeID: in.ExpectedRuntimeID}
	action := bodyAction(p, in.ActionID, "sendTaskNodeMessage", struct {
		NodeID uuid.UUID
		Input  taskmodel.TaskNodeMessageInput
	}{node, in})
	action.Authorize = func(m *store.TaskMutation) error { _, e := consoleNodeTarget(m, node); return e }
	created := false
	entered := false
	var interrupted []uuid.UUID
	out, err := s.Store.MutateTask(ctx, task, action, func(m *store.TaskMutation) (any, error) {
		entered = true
		if s.larkDispatchPaused(target.RuntimeID) {
			return nil, store.TaskConsoleConflict(m, target, "chat_unavailable")
		}
		receipt, ids, e := s.Store.EnqueueTaskConsoleMessageTx(ctx, m, target, in.Text, in.WhenBusy)
		if e != nil {
			return nil, e
		}
		created = true
		interrupted = ids
		return receipt, nil
	})
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == apperr.CodeTaskConflict && e.Details == nil {
			if r, readErr := s.Store.GetTask(ctx, task); readErr == nil {
				reason := "action_conflict"
				if entered {
					reason = "chat_unavailable"
				}
				return nil, store.TaskConsoleConflict(&store.TaskMutation{Record: &r}, target, reason)
			}
		}
		return nil, err
	}
	if created {
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
