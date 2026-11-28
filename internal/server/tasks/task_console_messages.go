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
	return store.TaskConsoleLineage{}, apperr.NotFound("Task node")
}

func (s *Service) ConsoleMessageCapability(ctx context.Context, p auth.Principal, task, node uuid.UUID) (taskmodel.TaskNodeMessageCapability, error) {
	var out taskmodel.TaskNodeMessageCapability
	if p.Kind != auth.KindConsole {
		return out, apperr.Forbidden("Only the console can check whether a node accepts messages.")
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
			message := "The original node cannot receive messages right now. It may have changed, or its original run conditions are not available."
			hint := "Keep the draft. Check the current node, the original session and the captured model login. Do not send it elsewhere or retry automatically."
			switch reason {
			case "task_ended":
				message = "The task has ended. The node history is read-only."
			case "node_changed":
				message = "The selected run is no longer the current node. Its history is read-only."
			case "instance_unavailable":
				message = "The original task instance is not prepared yet or has been retired."
			case "target_mismatch":
				message = "The original node does not belong to this runtime instance."
			case "chat_unavailable":
				message = "The originally captured model configuration or login is not available right now. Check the original run resources."
				if n := m.Record.ActiveNode(node); n != nil && n.Status == "blocked" {
					eligible, e := s.Store.TaskConsoleEligibleTx(ctx, m, node)
					if e != nil {
						return nil, e
					}
					if !eligible {
						message = "This block has no confirmed run failure as its source, so chatting with the node to investigate is not available."
					} else {
						var state string
						var file *string
						if e := m.Tx.QueryRow(ctx, `SELECT session_state,session_file FROM task_instances WHERE id=$1`, target.RuntimeID).Scan(&state, &file); e != nil {
							return nil, e
						}
						if state != "active" || file == nil || *file == "" {
							message = "The original task session cannot be continued right now. Its context is not reopened or replaced automatically."
						}
					}
				}
			}
			out.Reason = &message
			if paused {
				message = "The original instance is confirming a credential handoff. Wait for the result of the original action."
			}
			out.HandlingHint = &hint
		}
		return nil, nil
	})
	return out, err
}

func (s *Service) SendConsoleMessage(ctx context.Context, p auth.Principal, task, node uuid.UUID, in taskmodel.TaskNodeMessageInput, maxTextBytes int) (json.RawMessage, error) {
	if in.ImageIDsPresent || len(in.ImageIDs) > 0 {
		return nil, apperr.Invalid("imageIds", "Images can only be sent through the current node's Inbox endpoint.")
	}
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("Only the console can send strict node messages.")
	}
	if in.ActionID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil || task == uuid.Nil || node == uuid.Nil || !utf8.ValidString(in.Text) || strings.TrimSpace(in.Text) == "" {
		return nil, apperr.Invalid("body", "actionId, expectedAgentId and expectedRuntimeId must be non-zero UUIDs, and text must be valid UTF-8 and not blank.")
	}
	if maxTextBytes <= 0 || len(in.Text) > maxTextBytes {
		return nil, apperr.TooLarge("The node message text exceeds the size limit.")
	}
	if in.WhenBusy == "" {
		in.WhenBusy = "queue"
	}
	if in.WhenBusy != "queue" && in.WhenBusy != "interrupt" {
		return nil, apperr.Invalid("whenBusy", "Must be queue or interrupt.")
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
