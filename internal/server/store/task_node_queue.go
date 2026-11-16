package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// TaskNodeQueueResult is the result of a node queue action.
type TaskNodeQueueResult struct {
	Item        *QueueItem  // the queue item the message action targeted (before the action)
	Turn        TaskTurn    // promote: interrupt turn now carrying the message; cancel: original carrying turn; stop: stopped turn
	Changed     bool        // false: already in the requested state (already_*)
	Interrupted []uuid.UUID // turns the executor must be told to interrupt after commit
}

// taskNodeMessage is one message in the node queue together with its carrying turn.
type taskNodeMessage struct {
	msg     InboxMessage
	turn    *TaskTurn
	strict  bool // a strict message the console sent to the node (task_node_inbox_messages)
	mode    string
	expired bool
}

// lockTaskNodeMessageTx locks one queued message of the node: a strict message is located by its binding, a message
// from another Agent by the turn that carries it.
// Returns not_found when it does not belong to this node, and target_mismatch when the target Agent or instance does
// not match.
func (s *Store) lockTaskNodeMessageTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, message uuid.UUID) (taskNodeMessage, error) {
	var out taskNodeMessage
	var task, node, agent, runtime uuid.UUID
	var carrier *uuid.UUID
	e := m.Tx.QueryRow(ctx, `SELECT b.task_id,b.node_run_id,b.agent_id,b.runtime_id,b.execution_mode,b.reminder_run_id,b.not_executed_at IS NOT NULL FROM task_node_inbox_messages b WHERE b.message_id=$1`, message).
		Scan(&task, &node, &agent, &runtime, &out.mode, &carrier, &out.expired)
	switch {
	case e == nil:
		if task != target.TaskID || node != target.NodeRunID {
			return out, apperr.NotFound("Node Inbox message")
		}
		if agent != target.AgentID || runtime != target.RuntimeID {
			return out, TaskConsoleConflict(m, target, "target_mismatch")
		}
		out.strict = true
	case !errors.Is(e, db.ErrNoRows):
		return out, e
	}
	if out.msg, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(200)+` WHERE m.id=$1`, message)); e != nil {
		return out, notFoundIfNoRows(e, "Node Inbox message")
	}
	if !out.strict {
		to := out.msg.To
		if to.Kind != "agent" || to.RuntimeID == uuid.Nil {
			return out, apperr.NotFound("Node Inbox message")
		}
		if to.ID != target.AgentID || to.RuntimeID != target.RuntimeID {
			return out, TaskConsoleConflict(m, target, "target_mismatch")
		}
		carrier = out.msg.ReminderTurnID
	}
	if carrier != nil {
		t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, *carrier))
		if e != nil {
			return out, e
		}
		if !out.strict && t.NodeRunID != target.NodeRunID {
			return out, apperr.NotFound("Node Inbox message")
		}
		out.turn = &t
	}
	return out, nil
}

// queuedTurn returns the still-queued turn that carries the message; returns the corresponding task_conflict when the
// message is cancelled, read or no longer valid, or the turn has already started.
func (x taskNodeMessage) queuedTurn(m *TaskMutation, target TaskConsoleLineage) (TaskTurn, error) {
	id := x.msg.ID
	switch {
	case x.msg.CancelledAt != nil:
		return TaskTurn{}, taskNodeMessageConflict(m, target, "message_cancelled", id, x.turn)
	case x.msg.ReadAt != nil:
		return TaskTurn{}, taskNodeMessageConflict(m, target, "message_read", id, x.turn)
	case x.expired:
		return TaskTurn{}, taskNodeMessageConflict(m, target, "message_not_executed", id, nil)
	case x.turn == nil || x.turn.Status != "queued":
		return TaskTurn{}, taskNodeMessageConflict(m, target, "message_dispatched", id, x.turn)
	}
	return *x.turn, nil
}

// taskNodeTargetGuard only confirms that the target is still the current node and instance: cancel and stop do not
// require the node conversation to be available.
func taskNodeTargetGuard(m *TaskMutation, target TaskConsoleLineage) string {
	if m.Record.Ended() {
		return "task_ended"
	}
	n := m.Record.Current()
	if n == nil || n.ID != target.NodeRunID {
		return "node_changed"
	}
	if target.TaskID != m.Record.View.Task.ID || n.AgentID != target.AgentID || n.RuntimeID != target.RuntimeID {
		return "target_mismatch"
	}
	return ""
}

// PromoteTaskNodeMessageTx turns one message in the node queue (from any sender) into an interrupt, then interrupts
// the node's current reply.
//   - A strict message the console sent to the node: merged, following the merge rules for sending as an interrupt
//     (same node, same run mode, total images not over the limit), into the interrupt turn already queued, or a new
//     one is created if there is none;
//   - A message from another Agent: newTurn merges it into, or creates, the node's interrupt reminder turn (the same
//     rule as at delivery).
//
// The original turn is voided when it has no other pending messages (merged_into_interrupt). When it is already
// carried by an interrupt turn, Changed=false.
func (s *Store) PromoteTaskNodeMessageTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, message uuid.UUID, newTurn func(trigger uuid.UUID) (TaskTurn, error)) (TaskNodeQueueResult, error) {
	var out TaskNodeQueueResult
	x, e := s.lockTaskNodeMessageTx(ctx, m, target, message)
	if e != nil {
		return out, e
	}
	reason, e := s.TaskConsoleGuardTx(ctx, m, target)
	if e != nil {
		return out, e
	}
	if reason != "" {
		return out, TaskConsoleConflict(m, target, reason)
	}
	current := "task_node"
	if m.Record.Current().Status == "blocked" {
		current = "task_conversation"
	}
	target.ExecutionMode = current
	if x.strict && x.mode != current {
		x.expired = true
	}
	old, e := x.queuedTurn(m, target)
	if e != nil {
		return out, e
	}
	item := inboxItem(x.msg, old.ID, old.Priority)
	out.Item = &item
	if old.Priority == 0 {
		out.Turn = old
		return out, nil
	}
	if x.strict {
		turn, found, e := s.findTaskNodeReminderTx(ctx, m, target, x.mode, 0, len(x.msg.Images))
		if e != nil {
			return out, e
		}
		if !found {
			if turn, e = s.newTaskNodeReminderTx(ctx, m, target, x.mode, 0, message); e != nil {
				return out, e
			}
		}
		if _, e = m.Tx.Exec(ctx, `UPDATE task_node_inbox_messages SET reminder_run_id=$2 WHERE message_id=$1`, message, turn.ID); e != nil {
			return out, e
		}
		out.Turn = turn
	} else {
		if out.Turn, e = newTurn(message); e != nil {
			return out, e
		}
		if e = LinkInboxTurnTx(ctx, m.Tx, message, out.Turn.ID); e != nil {
			return out, e
		}
	}
	if _, e = m.Tx.Exec(ctx, `UPDATE inbox_messages SET when_busy='interrupt' WHERE id=$1`, message); e != nil {
		return out, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, out.Turn, EventRunPromoted, map[string]any{"from": "queue", "to": "interrupt", "messageId": message.String(), "fromRunId": old.ID.String()}); e != nil {
		return out, e
	}
	m.Changed()
	if _, e = s.voidTaskTurnIfEmptyTx(ctx, m, old.ID, "merged_into_interrupt"); e != nil {
		return out, e
	}
	if e = s.WakeSleepForWorkTx(ctx, m.Tx, m, target.AgentID, target.RuntimeID, out.Turn.ID); e != nil {
		return out, e
	}
	if out.Interrupted, e = s.interruptTaskNodeTx(ctx, m, target); e != nil {
		return out, e
	}
	out.Changed = true
	if x.strict {
		return out, s.EmitTaskNodeInboxTx(ctx, m, message)
	}
	return out, nil
}

// CancelTaskNodeMessageTx lets a person cancel one message in the node queue (from any sender): records cancelled_at,
// and for a strict message also not_executed_reason=cancelled; a question with options becomes cancelled at the same
// time. The carrying turn is voided when it has no other pending messages (user_cancelled). When already cancelled,
// Changed=false.
func (s *Store) CancelTaskNodeMessageTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, message uuid.UUID) (TaskNodeQueueResult, error) {
	var out TaskNodeQueueResult
	x, e := s.lockTaskNodeMessageTx(ctx, m, target, message)
	if e != nil {
		return out, e
	}
	if x.msg.CancelledAt != nil {
		item := inboxItem(x.msg, uuid.Nil, 1)
		if x.turn != nil {
			item = inboxItem(x.msg, x.turn.ID, x.turn.Priority)
			out.Turn = *x.turn
		}
		out.Item = &item
		return out, nil
	}
	if reason := taskNodeTargetGuard(m, target); reason != "" {
		return out, TaskConsoleConflict(m, target, reason)
	}
	turn, e := x.queuedTurn(m, target)
	if e != nil {
		return out, e
	}
	item := inboxItem(x.msg, turn.ID, turn.Priority)
	out.Item, out.Turn = &item, turn
	if e = cancelInboxTx(ctx, m.Tx, message); e != nil {
		return out, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, turn, EventMessageCancelled, map[string]any{"messageId": message.String()}); e != nil {
		return out, e
	}
	m.Changed()
	if x.strict {
		if _, e = m.Tx.Exec(ctx, `UPDATE task_node_inbox_messages SET not_executed_reason='cancelled',not_executed_at=clock_timestamp() WHERE message_id=$1`, message); e != nil {
			return out, e
		}
		if e = s.EmitTaskNodeInboxTx(ctx, m, message); e != nil {
			return out, e
		}
	}
	if _, e = s.voidTaskTurnIfEmptyTx(ctx, m, turn.ID, "user_cancelled"); e != nil {
		return out, e
	}
	out.Changed = true
	return out, nil
}

// StopTaskNodeRunTx stops the reply in progress in this turn of the node (abort_requested carries stop:true), without
// sending a new message.
// When it is already stopping or being interrupted, Changed=false; when this turn has already ended, returns
// task_conflict with reason=run_not_active.
func (s *Store) StopTaskNodeRunTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, run uuid.UUID) (TaskNodeQueueResult, error) {
	var out TaskNodeQueueResult
	t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 AND node_run_id=$3`, run, target.TaskID, target.NodeRunID))
	if e != nil {
		return out, notFoundIfNoRows(e, "Task turn")
	}
	if t.RuntimeID != target.RuntimeID {
		return out, TaskConsoleConflict(m, target, "target_mismatch")
	}
	out.Turn = t
	switch t.Status {
	case "aborting":
		return out, nil
	case "starting", "running":
	default:
		return out, taskNodeMessageConflict(m, target, "run_not_active", uuid.Nil, &t)
	}
	if reason := taskNodeTargetGuard(m, target); reason != "" {
		return out, TaskConsoleConflict(m, target, reason)
	}
	if out.Turn, e = scanTaskTurn(m.Tx.QueryRow(ctx, `UPDATE task_turns SET status='aborting' WHERE id=$1 RETURNING `+taskTurnColumns, run)); e != nil {
		return out, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, out.Turn, EventRunAbortRequested, map[string]any{"stop": true}); e != nil {
		return out, e
	}
	m.Changed()
	out.Changed, out.Interrupted = true, []uuid.UUID{run}
	return out, m.Emit("message.created", taskMessage(out.Turn, false))
}

// TaskTurnStoppedTx reports whether this turn was stopped by a person (abort_requested carries stop:true).
func TaskTurnStoppedTx(ctx context.Context, tx *db.Tx, turn uuid.UUID) (bool, error) {
	var stopped bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.abort_requested' AND data->'stop'='true')`, turn).Scan(&stopped)
	return stopped, err
}

// voidReadTaskTurnTx is called after a strict node message is read: the turn carrying it is voided
// (no_pending_messages) when it is still queued and has no pending messages left.
func (s *Store) voidReadTaskTurnTx(ctx context.Context, m *TaskMutation, message uuid.UUID) error {
	var turn *uuid.UUID
	if e := m.Tx.QueryRow(ctx, `SELECT reminder_run_id FROM task_node_inbox_messages WHERE message_id=$1`, message).Scan(&turn); e != nil || turn == nil {
		if errors.Is(e, db.ErrNoRows) {
			return nil
		}
		return e
	}
	var queued bool
	if e := m.Tx.QueryRow(ctx, `SELECT status='queued' FROM task_turns WHERE id=$1`, *turn).Scan(&queued); e != nil || !queued {
		return e
	}
	m.Changed()
	_, e := s.voidTaskTurnIfEmptyTx(ctx, m, *turn, "no_pending_messages")
	return e
}
