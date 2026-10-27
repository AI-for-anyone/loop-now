package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

// EnqueueTaskInboxTurn给非当前实例或阻塞实例排Inbox回合，不启动workflow节点。
func (s *Store) EnqueueTaskInboxTurn(ctx context.Context, m *TaskMutation, inst TaskInstance, text string, priority int, trigger *uuid.UUID) (TaskTurn, error) {
	if m.Record.Ended() || inst.TaskID != m.Record.View.Task.ID {
		return TaskTurn{}, apperr.Conflict(apperr.CodeTaskConflict, "任务已结束")
	}
	old, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE runtime_id=$1 AND status='queued' AND origin='inbox_reminder' AND inbox_scope_node_id IS NULL AND priority=$2`, inst.ID, priority))
	if e == nil {
		if e = s.WakeSleepForWorkTx(ctx, m.Tx, m, inst.AgentID, inst.ID, old.ID); e != nil {
			return old, e
		}
		return scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, old.ID))
	}
	if e != pgx.ErrNoRows {
		return TaskTurn{}, e
	}
	var nodeID *uuid.UUID
	n := m.Record.Current()
	if n != nil && n.RuntimeID == inst.ID && n.Status == "blocked" {
		nodeID = &n.ID
	}
	t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,trigger_inbox_message_id,request_id) VALUES($1,$2,$3,$4,$5,$6,'queued','inbox_reminder',$7,$8,$9,$10) RETURNING `+taskTurnColumns, uuid.Must(uuid.NewV7()), inst.TaskID, inst.ID, nodeID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), priority, text, trigger, taskRequestID(ctx, m, "inbox_reminder")))
	if e != nil {
		return t, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, t, EventRunQueued, map[string]any{"origin": t.Origin}); e != nil {
		return t, e
	}
	if e = s.WakeSleepForWorkTx(ctx, m.Tx, m, inst.AgentID, inst.ID, t.ID); e != nil {
		return t, e
	}
	t, e = scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, t.ID))
	if e != nil {
		return t, e
	}
	if nodeID != nil {
		n.RunIDs = append(n.RunIDs, t.ID)
		e = m.Emit("message.created", taskMessage(t, false))
	}
	return t, e
}
func (s *Store) ClaimTaskInboxTurn(ctx context.Context, taskID, runtime uuid.UUID) (TaskTurn, bool, error) {
	var turn TaskTurn
	found := false
	_, err := s.MutateTask(ctx, taskID, nil, func(m *TaskMutation) (any, error) {
		m.Unchanged()
		if fenced, e := s.TaskLarkRuntimeFencedTx(ctx, m, runtime); e != nil || fenced {
			return nil, e
		}
		if e := s.ExpireTaskConsoleTurnsTx(ctx, m, runtime); e != nil {
			return nil, e
		}
		if m.Record.Ended() || m.Record.View.Task.Status == "preparing" {
			return nil, nil
		}
		n := m.Record.Current()
		if n != nil && n.RuntimeID == runtime && n.Status != "blocked" {
			return nil, nil
		}
		var ready, active bool
		if e := m.Tx.QueryRow(ctx, `SELECT prepared AND NOT retired FROM task_instances WHERE id=$1 AND task_id=$2`, runtime, taskID).Scan(&ready); e != nil {
			return nil, e
		}
		if !ready {
			return nil, nil
		}
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting'))`, runtime).Scan(&active); e != nil {
			return nil, e
		}
		if active {
			return nil, nil
		}
		var t TaskTurn
		var e error
		// 提醒轮次领取时已没有待处理消息就作废，接着领取下一轮。
		for {
			t, e = scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE runtime_id=$1 AND status='queued' AND (origin='inbox_reminder' OR (origin='console_node' AND execution_mode='task_conversation') OR (origin='sleep_wake' AND EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.id=task_turns.wake_sleep_id AND z.wake_run_id=task_turns.id AND z.mode='task_conversation' AND z.state='woken'))) ORDER BY priority,queue_seq LIMIT 1 FOR UPDATE`, runtime))
			if e == pgx.ErrNoRows {
				return nil, nil
			}
			if e != nil {
				return nil, e
			}
			strict, valid, e := s.GuardTaskConsoleTurnTx(ctx, m, t)
			if e != nil {
				return nil, e
			}
			if !valid || strict && t.ExecutionMode != "task_conversation" {
				return nil, nil
			}
			skip, e := s.prepareTaskReminderClaimTx(ctx, m, &t)
			if e != nil {
				return nil, e
			}
			if !skip {
				break
			}
		}
		now := time.Now().UTC()
		if e := s.BindTaskNodeInboxBatchTx(ctx, m, t); e != nil {
			return nil, e
		}
		conversation := t
		conversation.ExecutionMode = "task_conversation"
		if e := s.BindTaskLarkTurnTx(ctx, m, conversation); e != nil {
			return nil, e
		}
		t.Status = "starting"
		t.ExecutionMode = "task_conversation"
		t.StartedAt = &now
		if _, e = m.Tx.Exec(ctx, `UPDATE task_turns SET status='starting',started_at=$2,execution_mode='task_conversation' WHERE id=$1`, t.ID, now); e != nil {
			return nil, e
		}
		if e = s.taskLifecycle(ctx, m.Tx, t, EventRunDispatched, nil); e != nil {
			return nil, e
		}
		m.Changed()
		turn, found = t, true
		if t.NodeRunID != uuid.Nil {
			return nil, m.Emit("message.created", taskMessage(t, false))
		}
		return nil, nil
	})
	return turn, found, err
}

// InterruptTaskInboxRounds用于重试当前实例时终止没有节点归属的旧Inbox回合。
func (s *Store) InterruptTaskInboxRounds(ctx context.Context, m *TaskMutation, runtime uuid.UUID) error {
	return s.InterruptTaskRuntime(ctx, m, &runtime, true)
}
func (s *Store) InterruptAllTaskConversations(ctx context.Context, m *TaskMutation) error {
	return s.InterruptTaskRuntime(ctx, m, nil, true)
}
