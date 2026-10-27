package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// Start 对账持久队列；重启时执行中的轮次只阻塞，绝不重放工具。
func (s *Service) Start(ctx context.Context) error {
	if s.Options.ReconcileInterval <= 0 {
		return errors.New("任务对账间隔必须为正")
	}
	if s.Coord != nil && (s.Options.LockRenewInterval <= 0 || s.Options.RecordTimeout <= 0 || s.Options.InstanceID == uuid.Nil) {
		return errors.New("任务Redis协调参数未完整配置")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("任务服务已经启动")
	}
	s.mu.Unlock()
	ids, err := s.Store.TaskIDs(ctx, []string{"preparing", "running", "waiting", "blocked"})
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			consoleSource, e := s.Store.TaskConsoleRecoverySourceTx(ctx, m)
			if e != nil {
				return nil, e
			}
			count, currentInterrupted, e := s.Store.RecoverTaskTurns(ctx, m)
			if e != nil {
				return nil, e
			}
			if count == 0 {
				return nil, nil
			}
			m.Changed()
			if !currentInterrupted {
				return nil, nil
			}
			block(m.Record, "服务重启时节点正在执行，请重试当前节点或取消任务")
			if consoleSource != uuid.Nil {
				if e = s.Store.RecordTaskConsoleEligibilityTx(ctx, m, consoleSource); e != nil {
					return nil, e
				}
			}
			if e = s.queueNotice(ctx, m, "blocked"); e != nil {
				return nil, e
			}
			return nil, m.Emit("node.updated", map[string]any{"node": m.Record.Current()})
		})
		if err != nil {
			return err
		}
	}
	if s.Coord != nil {
		s.Store.TaskCommitted = s.mirrorEvents
	}
	s.mu.Lock()
	s.started = true
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		s.reconcile()
		ticker := time.NewTicker(s.Options.ReconcileInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				s.reconcile()
			}
		}
	}()
	return nil
}
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.cancel()
	for _, cancel := range s.cancels {
		cancel()
	}
	for _, cancel := range s.inboxCancels {
		cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) KickTaskResponse(data json.RawMessage) {
	var d taskmodel.Detail
	if json.Unmarshal(data, &d) == nil {
		s.Kick(d.Task.ID)
	}
}
func (s *Service) Kick(id uuid.UUID) {
	s.mu.Lock()
	if !s.started || s.ctx.Err() != nil || s.drivers[id] {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.drivers[id] = true
	s.cancels[id] = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		var driveErr error
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.drivers, id)
			delete(s.cancels, id)
			s.mu.Unlock()
			// 消息在最后一次空队列检查与退出之间提交时，不能丢掉即时唤醒。
			if ctx.Err() == nil && driveErr == nil {
				if r, e := s.Store.GetTask(ctx, id); e == nil && r.Current() != nil && s.larkDispatchPaused(r.Current().RuntimeID) {
					return
				}
				var pending bool
				err := s.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns q JOIN task_records t ON t.id=q.task_id WHERE t.id=$1 AND t.status IN ('running','waiting') AND q.status='queued' AND NOT EXISTS(SELECT 1 FROM task_lark_switch_fences f WHERE f.runtime_id=q.runtime_id) AND NOT EXISTS(SELECT 1 FROM task_turns a WHERE a.runtime_id=q.runtime_id AND a.status IN ('starting','running','aborting')) AND q.node_run_id=(t.document->'view'->'task'->>'currentNodeRunId')::uuid)`, id).Scan(&pending)
				if err == nil && pending {
					s.Kick(id)
				}
			}
		}()
		driveErr = s.drive(ctx, id)
		if driveErr != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Error("任务协调失败", "task_id", id, "error", driveErr)
		}
	}()
}
func (s *Service) reconcile() {
	_ = s.ReconcileLarkSwitchOutcomes(s.ctx)
	s.mirrorEvents()
	ids, err := s.Store.TaskIDs(s.ctx, []string{"preparing", "running", "waiting", "blocked", "completed", "cancelled"})
	if err != nil {
		return
	}
	for _, id := range ids {
		if err := s.ReconcileTaskLarkInbox(s.ctx, id); err != nil && s.Log != nil {
			s.Log.Warn("任务凭证交接延期Inbox对账失败", "task_id", id, "code", "task_lark_inbox_reconcile_failed")
		}
		instances, e := s.Store.TaskInstances(s.ctx, id)
		if e == nil {
			for _, inst := range instances {
				if s.Coord != nil {
					_ = s.mirrorQueue(s.ctx, inst.ID)
				}
				s.KickInbox(inst.ID)
			}
		}
		s.Kick(id)
	}
	if s.QuestionMaintenance != nil {
		if err := s.QuestionMaintenance(s.ctx); err != nil {
			s.Log.Warn("统一问题对账失败", "code", "question_reconcile_failed")
		}
	}
	s.deliverNotices(s.ctx)
}
func (s *Service) drive(ctx context.Context, id uuid.UUID) error {
	for ctx.Err() == nil {
		r, err := s.Store.GetTask(ctx, id)
		if err != nil {
			return err
		}
		if r.Ended() {
			if r.View.Cleanup == "pending" || r.View.Cleanup == "failed" {
				return s.cleanup(ctx, id)
			}
			return nil
		}
		if n := r.Current(); n != nil && s.larkDispatchPaused(n.RuntimeID) {
			return nil
		}
		if r.View.Task.Status == "blocked" {
			return nil
		}
		if r.View.Task.Status == "preparing" {
			if s.Source == nil {
				return errors.New("任务执行源未配置")
			}
			instances, e := s.Store.TaskInstances(ctx, id)
			if e != nil {
				return e
			}
			for _, inst := range instances {
				if e = s.Source.Prepare(ctx, inst); e != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return s.blockTask(ctx, id, "任务分叉或目录准备失败")
				}
			}
			_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
				if m.Record.Ended() || m.Record.View.Task.Status != "preparing" {
					m.Unchanged()
					return nil, nil
				}
				m.Record.View.Task.Status = "running"
				return nil, s.prepareNode(ctx, m, "")
			})
			if e != nil {
				return e
			}
			continue
		}
		n := r.Current()
		if n == nil {
			return errors.New("任务缺少当前节点")
		}
		if e := s.mirrorQueue(ctx, n.RuntimeID); e != nil {
			return e
		}
		if n.Status == "queued" && s.Hooks != nil {
			// node.before_start（plugins/SPEC.md §4）：节点第一次开始前询问插件，不在任务事务里等待；被阻止时任务进入阻塞并写明原因。
			project, agent, task, node := r.View.Task.ProjectID, n.AgentID, id, n.ID
			o, e := s.Hooks.Hook(ctx, pluginhost.Intercept, pluginhost.Event("node.before_start", &project, &agent, &task, nil,
				map[string]any{"nodeRunId": node, "stepId": n.StepID}), &project)
			if e != nil {
				return e
			}
			if o.Blocked {
				reason := o.Reason
				if reason == "" {
					reason = "没有说明原因"
				}
				return s.blockTask(ctx, id, "插件 "+o.Plugin+" 阻止节点开始："+reason)
			}
		}
		turn, found, e := s.Store.ClaimTaskTurn(ctx, id, n.RuntimeID)
		if e != nil {
			return e
		}
		if !found {
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
			result.ErrorMessage = "执行基础设施失败"
		}
		var code, message *string
		if result.ErrorCode != "" {
			code = &result.ErrorCode
		}
		if result.ErrorMessage != "" {
			message = &result.ErrorMessage
		}
		if result.Outcome != "completed" && result.Outcome != "failed" && result.Outcome != "interrupted" {
			result.Outcome = "failed"
		}
		_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			code, message, err := stoppedError(ctx, m, turn.ID, result.Outcome, code, message)
			if err != nil {
				return nil, err
			}
			if _, err := s.Store.UpdateTaskTurnTx(ctx, m, turn.ID, result.Outcome, result.Text, code, message); err != nil {
				return nil, err
			}
			waiting, err := s.Store.FinishSleepTx(ctx, m.Tx, m, turn.ID, result.Outcome)
			if err != nil {
				return nil, err
			}
			n := m.Record.Current()
			if waiting && result.Outcome == "completed" && !m.Record.Ended() && n != nil && n.ID == turn.NodeRunID && n.Output == nil {
				n.Status = "waiting"
				m.Record.View.Task.Status = "waiting"
				return nil, m.Emit("node.updated", map[string]any{"node": n})
			}
			return s.settleTx(ctx, m, turn, result)
		})
		if e != nil {
			return e
		}
		s.KickInbox(turn.RuntimeID)
		if record, e := s.Store.GetTask(ctx, id); e == nil && record.Ended() {
			s.interruptOldTurns(ctx, id)
		}
	}
	return ctx.Err()
}
func (s *Service) blockTask(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if m.Record.Ended() {
			m.Unchanged()
			return nil, nil
		}
		block(m.Record, reason)
		if e := s.queueNotice(ctx, m, "blocked"); e != nil {
			return nil, e
		}
		return nil, m.Emit("node.updated", map[string]any{"node": m.Record.Current()})
	})
	return err
}
func (s *Service) settle(ctx context.Context, t store.TaskTurn, result executor.Result) error {
	_, err := s.Store.MutateTask(ctx, t.TaskID, nil, func(m *store.TaskMutation) (any, error) { return s.settleTx(ctx, m, t, result) })
	return err
}
func (s *Service) settleTx(ctx context.Context, m *store.TaskMutation, t store.TaskTurn, result executor.Result) (any, error) {
	n := m.Record.Current()
	if m.Record.Ended() || n == nil || n.ID != t.NodeRunID || n.Status == "blocked" {
		m.Unchanged()
		return nil, nil
	}
	blocked := func(reason string) (any, error) {
		block(m.Record, reason)
		if e := s.queueNotice(ctx, m, "blocked"); e != nil {
			return nil, e
		}
		return nil, m.Emit("node.updated", map[string]any{"node": n})
	}
	if result.Outcome != executor.OutcomeCompleted {
		// Inbox interrupt是同会话转向：后续提醒已经持久排队，不自动重放原轮。
		// 被人停止（不发新消息）时，节点还有任意优先级的排队项就继续处理下一条，否则转阻塞。
		var next, stopped bool
		if result.Outcome == executor.OutcomeInterrupted {
			var e error
			if stopped, e = store.TaskTurnStoppedTx(ctx, m.Tx, t.ID); e != nil {
				return nil, e
			}
			minPriority := 0
			if stopped {
				minPriority = 1
			}
			if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND node_run_id=$2 AND status='queued' AND (origin='inbox_reminder' AND priority<=$3 OR $3=1 AND origin<>'sleep_wake'))`, t.RuntimeID, n.ID, minPriority).Scan(&next); e != nil {
				return nil, e
			}
		}
		if !next && result.Outcome == executor.OutcomeInterrupted {
			var e error
			next, e = s.Store.HasTaskConsoleInterruptTx(ctx, m, t.RuntimeID, n.ID)
			if e != nil {
				return nil, e
			}
		}
		if next && n.Output == nil {
			n.Status = "queued"
			return nil, m.Emit("node.updated", map[string]any{"node": n})
		}
		if stopped {
			block(m.Record, "已被人停止")
		} else {
			block(m.Record, "节点执行异常结束，已提交的结果保留但不推进")
		}
		var executed bool
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_events WHERE task_turn_id=$1 AND type=$2)`, t.ID, store.EventRunRunning).Scan(&executed); e != nil {
			return nil, e
		}
		if executed && result.ErrorCode != "task_conflict" && t.ExecutionMode == "task_node" {
			if e := s.Store.RecordTaskConsoleEligibilityTx(ctx, m, t.ID); e != nil {
				return nil, e
			}
		}
		if e := s.queueNotice(ctx, m, "blocked"); e != nil {
			return nil, e
		}
		return nil, m.Emit("node.updated", map[string]any{"node": n})
	}
	if n.Output != nil {
		valid, e := s.Store.TaskLarkSubmittedByTurnTx(ctx, m, t)
		if e != nil {
			return nil, e
		}
		if !valid {
			return blocked("恢复回合缺少本轮有效提交，保留已有输出但不推进节点")
		}
	}
	if n.Output == nil {
		if n.Question != nil && (n.Question.Status == "answered" || n.Question.Status == "expired" || n.Question.Status == "closed") {
			var pending bool
			if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND node_run_id=$2 AND status='queued' AND origin='inbox_reminder')`, n.RuntimeID, n.ID).Scan(&pending); e != nil {
				return nil, e
			}
			if pending {
				n.Status = "queued"
				m.Record.View.Task.Status = "running"
				return nil, m.Emit("node.updated", map[string]any{"node": n})
			}
		}
		if n.Question != nil && n.Question.Status == "open" {
			n.Status = "waiting"
			m.Record.View.Task.Status = "waiting"
			return nil, m.Emit("node.updated", map[string]any{"node": n})
		}
		return blocked("节点轮次结束但未提交结果，也没有等待中的问题")
	}
	edge, e := m.Record.View.Workflow.SelectEdge(n.StepID, n.EdgeID, m.Record.EdgeCounts)
	if e != nil {
		return blocked(e.Error())
	}
	// 节点已提交结果时关闭仍未回答的问题，原消息历史保留。
	if e = s.closeQuestion(ctx, m, n, "node_completed"); e != nil {
		return nil, e
	}
	if edge != nil && len(m.Record.View.Nodes) >= s.Options.MaxVisits {
		return blocked("任务节点访问次数达到上限")
	}
	if e = s.Store.InterruptTaskTurns(ctx, m, n.ID); e != nil {
		return nil, e
	}
	now := time.Now().UTC()
	n.Status = "completed"
	n.EndedAt = &now
	if e = m.Emit("node.updated", map[string]any{"node": n}); e != nil {
		return nil, e
	}
	if edge == nil {
		if e := s.Store.InterruptAllTaskConversations(ctx, m); e != nil {
			return nil, e
		}
		m.Record.View.Task.Status = "completed"
		m.Record.View.Task.EndedAt = &now
		m.Record.View.Cleanup = "pending"
		return nil, s.queueNotice(ctx, m, "completed")
	}
	m.Record.EdgeCounts[edge.ID]++
	step, _ := m.Record.View.Workflow.Step(edge.To)
	var inst store.TaskInstance
	e = m.Tx.QueryRow(ctx, `SELECT id,agent_id,agent_name FROM task_instances WHERE task_id=$1 AND agent_id=$2`, t.TaskID, step.AgentID).Scan(&inst.ID, &inst.AgentID, &inst.AgentName)
	if e != nil {
		return nil, e
	}
	visit := 1
	for _, old := range m.Record.View.Nodes {
		if old.StepID == step.ID && old.Visit >= visit {
			visit = old.Visit + 1
		}
	}
	node := freshNode(t.TaskID, step, inst, visit, 1)
	m.Record.View.Nodes = append(m.Record.View.Nodes, node)
	setCurrent(m.Record, node)
	if n.RuntimeID != node.RuntimeID {
		// A queued reminder cancelled with the old node still represents unread mail.
		// Rebind the notification to an unbound Inbox round, never to another node.
		var trigger uuid.UUID
		e := m.Tx.QueryRow(ctx, `SELECT t.trigger_inbox_message_id FROM task_turns t JOIN inbox_messages i ON i.id=t.trigger_inbox_message_id WHERE t.task_id=$1 AND t.node_run_id=$2 AND t.origin='inbox_reminder' AND t.inbox_scope_node_id IS NULL AND t.status='interrupted' AND t.started_at IS NULL AND i.read_at IS NULL AND i.cancelled_at IS NULL ORDER BY t.queue_seq LIMIT 1`, t.TaskID, n.ID).Scan(&trigger)
		if e != nil && !store.IsNotFound(e) {
			return nil, e
		}
		if e == nil {
			old := store.TaskInstance{ID: n.RuntimeID, TaskID: t.TaskID, AgentID: n.AgentID, AgentName: n.AgentName}
			if _, e = s.Store.EnqueueTaskInboxTurn(ctx, m, old, "你有尚未处理的任务Inbox消息，请列摘要后逐条读取并回复；当前只处理对话，不推进其他workflow节点。", 1, &trigger); e != nil {
				return nil, e
			}
		}
	}
	return nil, s.prepareNode(ctx, m, "")
}

func (s *Service) interruptOldTurns(ctx context.Context, id uuid.UUID) {
	rows, err := s.Store.Pool.Query(ctx, `SELECT runtime_id,id FROM task_turns WHERE task_id=$1 AND status='aborting'`, id)
	if err != nil {
		return
	}
	type active struct{ runtime, turn uuid.UUID }
	items := []active{}
	for rows.Next() {
		var a active
		if rows.Scan(&a.runtime, &a.turn) == nil {
			items = append(items, a)
		}
	}
	rows.Close()
	for _, a := range items {
		if s.Coord != nil {
			_ = s.Coord.PublishInterrupt(ctx, a.runtime.String(), a.turn.String())
		}
		_ = s.Executor.Interrupt(ctx, a.runtime, a.turn)
	}
	if r, e := s.Store.GetTask(ctx, id); e == nil && r.Ended() {
		s.mu.Lock()
		if cancel := s.cancels[id]; cancel != nil {
			cancel()
		}
		s.mu.Unlock()
		if instances, e := s.Store.TaskInstances(ctx, id); e == nil {
			s.mu.Lock()
			for _, inst := range instances {
				if cancel := s.inboxCancels[inst.ID]; cancel != nil {
					cancel()
				}
			}
			s.mu.Unlock()
		}
	}
}

type turnSink struct {
	service *Service
	ctx     context.Context
	turn    store.TaskTurn
	mu      sync.Mutex
	text    string
}

func (t *turnSink) Phase(phase string) { t.event(store.EventRunPhase, map[string]any{"phase": phase}) }
func (t *turnSink) warn(err error) {
	if err != nil && t.service.Log != nil {
		log := t.service.Log
		if inst, e := t.service.Store.TaskInstance(t.ctx, t.turn.RuntimeID); e == nil {
			log = log.With("agentId", inst.AgentID)
		}
		log.Warn("写入任务运行记录失败", "taskId", t.turn.TaskID, "runtimeId", t.turn.RuntimeID, "nodeRunId", taskNodeRef(t.turn.NodeRunID), "runId", t.turn.ID, "instanceId", t.service.Store.InstanceID, "inboxMessageId", t.turn.TriggerInboxID, "requestId", t.turn.RequestID, "error", err)
	}
}
func (t *turnSink) event(kind string, data map[string]any) {
	t.warn(t.service.Store.RecordTaskObservation(t.ctx, t.turn, kind, data))
}
func (t *turnSink) Running(version int64) {
	t.warn(t.service.Store.MarkTaskRunning(t.ctx, t.turn, version))
}
func (t *turnSink) TextDelta(delta string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.text += delta
	_, err := t.service.Store.UpdateTaskTurn(t.ctx, t.turn.TaskID, t.turn.ID, "running", t.text, nil, nil)
	t.warn(err)
}
func (t *turnSink) Tool(id, name, state string) {
	t.event("tool.changed", map[string]any{"toolCallId": id, "toolName": name, "state": state})
}
func (s *Service) deliverNotices(ctx context.Context) {
	if s.SystemNotice == nil {
		return
	}
	rows, err := s.Store.Pool.Query(ctx, `SELECT n.id,n.message_id,n.text,p.manager_agent_id,n.task_id FROM task_notifications n JOIN task_records t ON t.id=n.task_id JOIN projects p ON p.id=t.project_id WHERE n.sent_at IS NULL AND p.manager_agent_id IS NOT NULL ORDER BY n.id LIMIT 100`)
	if err != nil {
		return
	}
	type notice struct {
		id, message, manager, task uuid.UUID
		text                       string
	}
	items := []notice{}
	for rows.Next() {
		var n notice
		if rows.Scan(&n.id, &n.message, &n.text, &n.manager, &n.task) == nil {
			items = append(items, n)
		}
	}
	rows.Close()
	for _, n := range items {
		record, e := s.Store.GetTask(ctx, n.task)
		if e != nil {
			continue
		}
		text := n.text + "\n" + record.View.Task.Link
		if err = s.SystemNotice(ctx, n.manager, n.message, text); err == nil {
			_, _ = s.Store.Pool.Exec(ctx, `UPDATE task_notifications SET sent_at=now() WHERE id=$1`, n.id)
		}
	}
}

var _ executor.Sink = (*turnSink)(nil)

// stoppedError 给被人停止的轮次记上 user_stopped（不是执行失败，也不是被新消息打断）。
func stoppedError(ctx context.Context, m *store.TaskMutation, turn uuid.UUID, outcome string, code, message *string) (*string, *string, error) {
	if outcome != executor.OutcomeInterrupted {
		return code, message, nil
	}
	stopped, err := store.TaskTurnStoppedTx(ctx, m.Tx, turn)
	if err != nil || !stopped {
		return code, message, err
	}
	c, msg := "user_stopped", "已被人停止"
	return &c, &msg, nil
}
