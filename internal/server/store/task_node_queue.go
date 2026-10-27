package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// TaskNodeQueueResult 是节点排队动作的结果。
type TaskNodeQueueResult struct {
	Item        *QueueItem  // 消息动作针对的排队项（动作前）
	Turn        TaskTurn    // 转为打断：现在承载消息的打断轮次；取消：原承载轮次；停止：被停止的轮次
	Changed     bool        // false：已经是请求的状态（already_*）
	Interrupted []uuid.UUID // 提交后要通知执行器打断的轮次
}

// taskNodeMessage 是节点排队中的一条消息及其承载轮次。
type taskNodeMessage struct {
	msg     InboxMessage
	turn    *TaskTurn
	strict  bool // 控制台发给节点的严格消息（task_node_inbox_messages）
	mode    string
	expired bool
}

// lockTaskNodeMessageTx 锁住节点的一条排队消息：严格消息按绑定定位，其他 Agent 发来的消息按承载它的轮次定位。
// 不属于这个节点时返回 not_found，目标 Agent 或实例不一致时返回 target_mismatch。
func (s *Store) lockTaskNodeMessageTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, message uuid.UUID) (taskNodeMessage, error) {
	var out taskNodeMessage
	var task, node, agent, runtime uuid.UUID
	var carrier *uuid.UUID
	e := m.Tx.QueryRow(ctx, `SELECT b.task_id,b.node_run_id,b.agent_id,b.runtime_id,b.execution_mode,b.reminder_run_id,b.not_executed_at IS NOT NULL FROM task_node_inbox_messages b WHERE b.message_id=$1 FOR UPDATE OF b`, message).
		Scan(&task, &node, &agent, &runtime, &out.mode, &carrier, &out.expired)
	switch {
	case e == nil:
		if task != target.TaskID || node != target.NodeRunID {
			return out, apperr.NotFound("节点Inbox消息")
		}
		if agent != target.AgentID || runtime != target.RuntimeID {
			return out, TaskConsoleConflict(m, target, "target_mismatch")
		}
		out.strict = true
	case !errors.Is(e, pgx.ErrNoRows):
		return out, e
	}
	if out.msg, e = scanInbox(m.Tx.QueryRow(ctx, inboxSelect(200)+` WHERE m.id=$1 FOR UPDATE OF m`, message)); e != nil {
		return out, notFoundIfNoRows(e, "节点Inbox消息")
	}
	if !out.strict {
		to := out.msg.To
		if to.Kind != "agent" || to.RuntimeID == uuid.Nil {
			return out, apperr.NotFound("节点Inbox消息")
		}
		if to.ID != target.AgentID || to.RuntimeID != target.RuntimeID {
			return out, TaskConsoleConflict(m, target, "target_mismatch")
		}
		carrier = out.msg.ReminderTurnID
	}
	if carrier != nil {
		t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 FOR UPDATE`, *carrier))
		if e != nil {
			return out, e
		}
		if !out.strict && t.NodeRunID != target.NodeRunID {
			return out, apperr.NotFound("节点Inbox消息")
		}
		out.turn = &t
	}
	return out, nil
}

// queuedTurn 返回承载消息、仍在排队的轮次；消息已取消、已读、已失效或轮次已开始时返回对应的 task_conflict。
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

// taskNodeTargetGuard 只确认目标仍是当前节点与实例：取消与停止不要求节点对话可用。
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

// PromoteTaskNodeMessageTx 把节点排队中的一条消息（任何发件人）转为打断，然后打断节点当前的回复。
//   - 控制台发给节点的严格消息：按发送打断时的合并规则（同节点、同执行模式、图片总数不超上限）并入已在排队的打断轮次，没有就新建；
//   - 其他 Agent 发来的消息：由 newTurn 并入或新建该节点的打断提醒轮次（与投递时同一规则）。
//
// 原轮次没有其他待处理消息即作废（merged_into_interrupt）。已经由打断轮次承载时 Changed=false。
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

// CancelTaskNodeMessageTx 由人取消节点排队中的一条消息（任何发件人）：记 cancelled_at，严格消息另记 not_executed_reason=cancelled；
// 带选项的问题同时变为 cancelled。承载轮次没有其他待处理消息即作废（user_cancelled）。已经取消时 Changed=false。
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

// StopTaskNodeRunTx 停止节点这一轮进行中的回复（abort_requested 带 stop:true），不发送新消息。
// 已在停止或打断中时 Changed=false；这一轮已结束时返回 task_conflict，reason=run_not_active。
func (s *Store) StopTaskNodeRunTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, run uuid.UUID) (TaskNodeQueueResult, error) {
	var out TaskNodeQueueResult
	t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 AND node_run_id=$3 FOR UPDATE`, run, target.TaskID, target.NodeRunID))
	if e != nil {
		return out, notFoundIfNoRows(e, "任务轮次")
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

// TaskTurnStoppedTx 报告这一轮是否被人停止（abort_requested 带 stop:true）。
func TaskTurnStoppedTx(ctx context.Context, tx pgx.Tx, turn uuid.UUID) (bool, error) {
	var stopped bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.abort_requested' AND data->>'stop'='true')`, turn).Scan(&stopped)
	return stopped, err
}

// voidReadTaskTurnTx 在严格节点消息被读取后调用：承载它的轮次仍在排队且已没有待处理消息时作废（no_pending_messages）。
func (s *Store) voidReadTaskTurnTx(ctx context.Context, m *TaskMutation, message uuid.UUID) error {
	var turn *uuid.UUID
	if e := m.Tx.QueryRow(ctx, `SELECT reminder_run_id FROM task_node_inbox_messages WHERE message_id=$1`, message).Scan(&turn); e != nil || turn == nil {
		if errors.Is(e, pgx.ErrNoRows) {
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
