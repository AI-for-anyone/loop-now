package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/taskmodel"
)

type TaskTurn struct {
	ID, TaskID, RuntimeID, NodeRunID, UserMessageID, AssistantMessageID uuid.UUID
	ExecutionMode, Status, Origin, Text, Reply                          string
	Priority                                                            int
	QueueSeq                                                            int64
	ErrorCode, ErrorMessage                                             *string
	QueuedAt                                                            time.Time
	StartedAt, EndedAt                                                  *time.Time
	TriggerInboxID                                                      *uuid.UUID
	WakeSleepID                                                         *uuid.UUID
	RequestID                                                           *uuid.UUID
	VoidReason                                                          *string
}

const taskTurnColumns = `id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,queue_seq,text,reply,error_code,error_message,queued_at,started_at,ended_at,trigger_inbox_message_id,request_id,wake_sleep_id,execution_mode,void_reason`

func scanTaskTurn(r pgx.Row) (TaskTurn, error) {
	var t TaskTurn
	var node *uuid.UUID
	e := r.Scan(&t.ID, &t.TaskID, &t.RuntimeID, &node, &t.UserMessageID, &t.AssistantMessageID, &t.Status, &t.Origin, &t.Priority, &t.QueueSeq, &t.Text, &t.Reply, &t.ErrorCode, &t.ErrorMessage, &t.QueuedAt, &t.StartedAt, &t.EndedAt, &t.TriggerInboxID, &t.RequestID, &t.WakeSleepID, &t.ExecutionMode, &t.VoidReason)
	if node != nil {
		t.NodeRunID = *node
	}
	return t, e
}

func (s *Store) EnqueueTaskTurn(ctx context.Context, m *TaskMutation, text, origin string, priority int, trigger *uuid.UUID) (TaskTurn, error) {
	node := m.Record.Current()
	if node == nil || m.Record.Ended() {
		return TaskTurn{}, apperr.Conflict(apperr.CodeTaskConflict, "没有可接收消息的当前任务节点")
	}
	if origin == "task_node" {
		if e := s.cancelUnboundInboxQueue(ctx, m, node.RuntimeID); e != nil {
			return TaskTurn{}, e
		}
	}
	if origin == "inbox_reminder" {
		if e := s.cancelUnboundInboxQueue(ctx, m, node.RuntimeID); e != nil {
			return TaskTurn{}, e
		}
		old, err := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE runtime_id=$1 AND node_run_id=$2 AND status='queued' AND origin='inbox_reminder' AND inbox_scope_node_id IS NULL AND priority=$3`, node.RuntimeID, node.ID, priority))
		if err == nil {
			if err = s.WakeSleepForWorkTx(ctx, m.Tx, m, node.AgentID, node.RuntimeID, old.ID); err != nil {
				return old, err
			}
			return scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, old.ID))
		}
		if err != pgx.ErrNoRows {
			return TaskTurn{}, err
		}
	}
	id, user, assistant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	t, err := scanTaskTurn(m.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,trigger_inbox_message_id,request_id) VALUES($1,$2,$3,$4,$5,$6,'queued',$7,$8,$9,$10,$11) RETURNING `+taskTurnColumns, id, m.Record.View.Task.ID, node.RuntimeID, node.ID, user, assistant, origin, priority, text, trigger, taskRequestID(ctx, m, origin)))
	if err != nil {
		return t, err
	}
	if err = s.taskLifecycle(ctx, m.Tx, t, EventRunQueued, map[string]any{"origin": t.Origin}); err != nil {
		return t, err
	}
	if origin != "sleep_wake" {
		if err = s.WakeSleepForWorkTx(ctx, m.Tx, m, node.AgentID, node.RuntimeID, t.ID); err != nil {
			return t, err
		}
	}
	t, err = scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, t.ID))
	if err != nil {
		return t, err
	}
	node.RunIDs = append(node.RunIDs, t.ID)
	if err = m.Emit("message.created", taskMessage(t, false)); err != nil {
		return t, err
	}
	return t, nil
}
func taskMessage(t TaskTurn, assistant bool) taskmodel.Message {
	m := taskmodel.Message{ID: t.UserMessageID, RunID: t.ID, NodeRunID: t.NodeRunID, Role: "user", Text: t.Text, Origin: t.Origin, WakeSleepID: t.WakeSleepID, VoidReason: t.VoidReason, Status: t.Status, CreatedAt: t.QueuedAt}
	if assistant {
		m.ID = t.AssistantMessageID
		m.Role = "assistant"
		m.ErrorCode = t.ErrorCode
		m.ErrorMessage = taskmodel.PublicError(t.ErrorCode, t.ErrorMessage)
		m.Text = t.Reply
		if t.StartedAt != nil {
			m.CreatedAt = *t.StartedAt
		}
	}
	return m
}

func (s *Store) ClaimTaskTurn(ctx context.Context, taskID, runtimeID uuid.UUID) (TaskTurn, bool, error) {
	var turn TaskTurn
	found := false
	_, err := s.MutateTask(ctx, taskID, nil, func(m *TaskMutation) (any, error) {
		m.Unchanged()
		if fenced, e := s.TaskLarkRuntimeFencedTx(ctx, m, runtimeID); e != nil || fenced {
			return nil, e
		}
		if e := s.ExpireTaskConsoleTurnsTx(ctx, m, runtimeID); e != nil {
			return nil, e
		}
		if m.Record.Ended() || m.Record.Current() == nil {
			return nil, nil
		}
		node := m.Record.Current()
		if node.RuntimeID != runtimeID || node.Status == "blocked" {
			return nil, nil
		}
		var active bool
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting'))`, runtimeID).Scan(&active); e != nil {
			return nil, e
		}
		if active {
			return nil, nil
		}
		var t TaskTurn
		var e error
		// 提醒轮次领取时已没有待处理消息就作废，接着领取下一轮。
		for {
			t, e = scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE runtime_id=$1 AND node_run_id=$2 AND status='queued' AND (origin<>'sleep_wake' OR EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.id=task_turns.wake_sleep_id AND z.wake_run_id=task_turns.id AND z.state='woken' AND z.mode='task_node')) ORDER BY priority,queue_seq LIMIT 1 FOR UPDATE`, runtimeID, node.ID))
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
			if !valid || strict && t.ExecutionMode != "task_node" {
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
		if e := s.BindTaskLarkTurnTx(ctx, m, t); e != nil {
			return nil, e
		}
		t.Status = "starting"
		t.ExecutionMode = "task_node"
		t.StartedAt = &now
		if _, e = m.Tx.Exec(ctx, `UPDATE task_turns SET status='starting',started_at=$2,execution_mode='task_node' WHERE id=$1`, t.ID, now); e != nil {
			return nil, e
		}
		if e = s.taskLifecycle(ctx, m.Tx, t, EventRunDispatched, nil); e != nil {
			return nil, e
		}
		if node.StartedAt == nil {
			node.StartedAt = &now
		}
		if node.Output == nil {
			node.Status = "running"
		}
		m.Record.View.Task.Status = "running"
		m.Record.View.Task.Reason = nil
		turn, found = t, true
		m.Changed()
		if e = m.Emit("node.updated", map[string]any{"node": node}); e != nil {
			return nil, e
		}
		return nil, m.Emit("message.created", taskMessage(t, false))
	})
	return turn, found, err
}

// UpdateTaskTurn 记录当前轮文字与状态；节点是否推进由协调器在完全结束后决定。
func (s *Store) UpdateTaskTurn(ctx context.Context, taskID, turnID uuid.UUID, status, reply string, errorCode, errorMessage *string) (TaskTurn, error) {
	var turn TaskTurn
	_, err := s.MutateTask(ctx, taskID, nil, func(m *TaskMutation) (any, error) {
		var err error
		turn, err = s.UpdateTaskTurnTx(ctx, m, turnID, status, reply, errorCode, errorMessage)
		return nil, err
	})
	return turn, err
}
func (s *Store) UpdateTaskTurnTx(ctx context.Context, m *TaskMutation, turnID uuid.UUID, status, reply string, errorCode, errorMessage *string) (TaskTurn, error) {
	t, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2 FOR UPDATE`, turnID, m.Record.View.Task.ID))
	if e != nil {
		return TaskTurn{}, notFoundIfNoRows(e, "任务轮次")
	}
	if t.Status == "completed" || t.Status == "failed" || t.Status == "interrupted" {
		m.Unchanged()
		return t, nil
	}
	if m.Record.Ended() {
		status = "interrupted"
	}
	// Text callbacks cannot change starting/aborting state; Running owns that transition.
	if status == "running" && t.Status != "running" {
		status = t.Status
	}
	t.Status, t.Reply, t.ErrorCode, t.ErrorMessage = status, reply, errorCode, errorMessage
	if status == "completed" || status == "failed" || status == "interrupted" {
		now := time.Now().UTC()
		t.EndedAt = &now
	}
	_, e = m.Tx.Exec(ctx, `UPDATE task_turns SET status=$2,reply=$3,error_code=$4,error_message=$5,ended_at=$6 WHERE id=$1`, t.ID, t.Status, t.Reply, errorCode, errorMessage, t.EndedAt)
	if e != nil {
		return TaskTurn{}, e
	}
	if t.EndedAt != nil {
		if e = s.taskEndedEvent(ctx, m, t); e != nil {
			return TaskTurn{}, e
		}
	}
	if t.NodeRunID == uuid.Nil {
		return t, nil
	}
	if e = m.Emit("message.created", taskMessage(t, false)); e != nil {
		return TaskTurn{}, e
	}
	return t, m.Emit("message.created", taskMessage(t, true))
}

func (s *Store) TaskTurn(ctx context.Context, id uuid.UUID) (TaskTurn, error) {
	t, err := scanTaskTurn(s.Pool.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, id))
	return t, notFoundIfNoRows(err, "任务轮次")
}
func (s *Store) TaskNodeMessages(ctx context.Context, taskID, nodeID uuid.UUID, cursor string, limit int) (taskmodel.MessagePage, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var at *time.Time
	var after *uuid.UUID
	if cursor != "" {
		t, id, err := parseTaskCursor(cursor)
		if err != nil {
			return taskmodel.MessagePage{}, err
		}
		at, after = &t, &id
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,run_id,node_run_id,role,text,origin,status,created_at,error_code,error_message,wake_sleep_id,void_reason FROM (
	 SELECT user_message_id AS id,id AS run_id,node_run_id,'user' AS role,text,origin,status,queued_at AS created_at,NULL::text AS error_code,NULL::text AS error_message,wake_sleep_id,void_reason FROM task_turns WHERE task_id=$1 AND node_run_id=$2
	 UNION ALL SELECT assistant_message_id,id,node_run_id,'assistant',reply,origin,status,coalesce(started_at,queued_at),error_code,error_message,wake_sleep_id,void_reason FROM task_turns WHERE task_id=$1 AND node_run_id=$2 AND status<>'queued'
	) m WHERE ($3::timestamptz IS NULL OR (created_at,id)>($3,$4)) ORDER BY created_at,id LIMIT $5`, taskID, nodeID, at, after, limit+1)
	if err != nil {
		return taskmodel.MessagePage{}, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (taskmodel.Message, error) {
		var m taskmodel.Message
		e := r.Scan(&m.ID, &m.RunID, &m.NodeRunID, &m.Role, &m.Text, &m.Origin, &m.Status, &m.CreatedAt, &m.ErrorCode, &m.ErrorMessage, &m.WakeSleepID, &m.VoidReason)
		m.ErrorMessage = taskmodel.PublicError(m.ErrorCode, m.ErrorMessage)
		return m, e
	})
	if err != nil {
		return taskmodel.MessagePage{}, err
	}
	out := taskmodel.MessagePage{Messages: items}
	if out.Messages == nil {
		out.Messages = []taskmodel.Message{}
	}
	if len(items) > limit {
		out.Messages = items[:limit]
		last := out.Messages[len(out.Messages)-1]
		c := taskCursor(last.CreatedAt, last.ID)
		out.NextCursor = &c
	}
	return out, nil
}

func (s *Store) InterruptTaskTurns(ctx context.Context, m *TaskMutation, nodeRunID uuid.UUID) error {
	rows, err := m.Tx.Query(ctx, `UPDATE task_turns SET error_code=CASE WHEN status='queued' AND EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN 'task_conflict' ELSE error_code END,error_message=CASE WHEN status='queued' AND EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=task_turns.id) THEN '原节点已结束或不再有效，该消息未执行' ELSE error_message END,status=CASE WHEN status='queued' THEN 'interrupted' ELSE 'aborting' END,ended_at=CASE WHEN status='queued' THEN now() ELSE ended_at END WHERE task_id=$1 AND node_run_id=$2 AND status IN ('queued','starting','running') RETURNING `+taskTurnColumns, m.Record.View.Task.ID, nodeRunID)
	if err != nil {
		return err
	}
	turns, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if err != nil {
		return err
	}
	for _, turn := range turns {
		if err = s.taskAbortEvent(ctx, m, turn); err != nil {
			return err
		}
		if turn.NodeRunID == uuid.Nil {
			continue
		}
		if err = m.Emit("message.created", taskMessage(turn, false)); err != nil {
			return err
		}
		if turn.StartedAt != nil {
			if err = m.Emit("message.created", taskMessage(turn, true)); err != nil {
				return err
			}
		}
	}
	return nil
}

func taskRequestID(ctx context.Context, m *TaskMutation, origin string) *uuid.UUID {
	if id, e := uuid.Parse(reqid.From(ctx)); e == nil {
		return &id
	}
	if n := m.Record.Current(); origin == "task_node" && n != nil && n.StepID == m.Record.View.Workflow.StartStepID && n.Visit == 1 && n.Attempt == 1 {
		return m.Record.CreationRequestID
	}
	return nil
}
