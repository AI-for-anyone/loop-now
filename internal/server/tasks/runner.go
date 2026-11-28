package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"

	"github.com/google/uuid"
)

// Start reconciles the persistent queue; on restart, turns that were running are only blocked, and tools are never
// replayed.
func (s *Service) Start(ctx context.Context) error {
	if s.Options.ReconcileInterval <= 0 {
		return errors.New("the task reconcile interval must be positive")
	}
	if s.Coord != nil && (s.Options.RecordTimeout <= 0 || s.Options.InstanceID == uuid.Nil) {
		return errors.New("the task coordination options are incomplete")
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("the task service is already started")
	}
	s.mu.Unlock()
	ids, err := s.Store.TaskIDs(ctx, []string{"preparing", "running", "waiting", "blocked"})
	if err != nil {
		return err
	}
	for _, id := range ids {
		_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
			m.Unchanged()
			sources, e := s.Store.TaskConsoleRecoverySourcesTx(ctx, m)
			if e != nil {
				return nil, e
			}
			count, interrupted, e := s.Store.RecoverTaskTurnsOf(ctx, m)
			if e != nil {
				return nil, e
			}
			if count == 0 {
				return nil, nil
			}
			m.Changed()
			// Every current node whose turn the restart ended is blocked: the one node, or each branch of a fork.
			for _, id := range interrupted {
				block(m.Record, m.Record.ActiveNode(id), "The node was running when the server restarted. Retry the current node or cancel the task.")
			}
			for _, source := range sources {
				if e = s.Store.RecordTaskConsoleEligibilityTx(ctx, m, source); e != nil {
					return nil, e
				}
			}
			for _, id := range interrupted {
				n := m.Record.ActiveNode(id)
				if e = s.queueNotice(ctx, m, n, "blocked"); e != nil {
					return nil, e
				}
				if e = m.Emit("node.updated", map[string]any{"node": n}); e != nil {
					return nil, e
				}
			}
			return nil, nil
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
	if !s.started || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if s.drivers[id] {
		// A driver inside a fork waits on its branches: it looks again, so a branch with new work (a retry, a message)
		// starts without waiting for the others.
		if poke := s.pokes[id]; poke != nil {
			select {
			case poke <- struct{}{}:
			default:
			}
		}
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.drivers[id] = true
	s.cancels[id] = cancel
	poke := make(chan struct{}, 1)
	if s.pokes == nil {
		s.pokes = map[uuid.UUID]chan struct{}{}
	}
	s.pokes[id] = poke
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
			delete(s.pokes, id)
			s.mu.Unlock()
			// When a message is committed between the last empty-queue check and exiting, its immediate wake-up must
			// not be lost.
			if ctx.Err() == nil && driveErr == nil {
				if r, e := s.Store.GetTask(ctx, id); e == nil && r.Current() != nil && s.larkDispatchPaused(r.Current().RuntimeID) {
					return
				}
				var pending bool
				err := s.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns q JOIN task_records t ON t.id=q.task_id WHERE t.id=$1 AND t.status IN ('running','waiting') AND q.status='queued' AND NOT EXISTS(SELECT 1 FROM task_lark_switch_fences f WHERE f.runtime_id=q.runtime_id) AND NOT EXISTS(SELECT 1 FROM task_turns a WHERE a.runtime_id=q.runtime_id AND a.status IN ('starting','running','aborting')) AND `+store.CurrentNodeSQL("t.document", "q.node_run_id")+`)`, id).Scan(&pending)
				if err == nil && pending {
					s.Kick(id)
				}
			}
		}()
		driveErr = s.drive(ctx, id, poke)
		if driveErr != nil && ctx.Err() == nil && s.Log != nil {
			s.Log.Error("task coordination failed", "task_id", id, "error", driveErr)
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
			s.Log.Warn("failed to reconcile Inbox messages deferred by a task credential handoff", "task_id", id, "code", "task_lark_inbox_reconcile_failed")
		}
		instances, e := s.Store.TaskInstances(s.ctx, id)
		if e == nil {
			for _, inst := range instances {
				s.KickInbox(inst.ID)
			}
		}
		s.Kick(id)
	}
	if s.QuestionMaintenance != nil {
		if err := s.QuestionMaintenance(s.ctx); err != nil {
			s.Log.Warn("unified question reconciliation failed", "code", "question_reconcile_failed")
		}
	}
	s.deliverNotices(s.ctx)
}

// drive advances the task until there is nothing to do: one step at a time outside a fork; inside one, every branch
// at once (driveFork). poke tells it to look again while it waits on branches.
func (s *Service) drive(ctx context.Context, id uuid.UUID, poke <-chan struct{}) error {
	l := newLanes()
	defer l.wait()
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
		if r.Fork != nil {
			more, err := s.driveFork(ctx, id, &r, l, poke)
			if err != nil || !more {
				return err
			}
			continue
		}
		if n := r.Current(); n != nil && s.larkDispatchPaused(n.RuntimeID) {
			return nil
		}
		if r.View.Task.Status == "blocked" {
			return nil
		}
		if r.View.Task.Status == "preparing" {
			if s.Source == nil {
				return errors.New("the task execution source is not configured")
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
					return s.blockTask(ctx, id, uuid.Nil, "Could not prepare the task fork or its directories.")
				}
			}
			created, e := s.prepareWorkspace(ctx, r)
			if e != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				what := "Could not create the task's worktree: "
				if w := r.View.Workspace; w != nil && w.Mode == taskmodel.WorkspaceModeCopy {
					what = "Could not copy the task's folder: "
				}
				return s.blockTask(ctx, id, uuid.Nil, what+e.Error())
			}
			_, e = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
				if m.Record.Ended() || m.Record.View.Task.Status != "preparing" {
					m.Unchanged()
					return nil, nil
				}
				activateWorkspace(m.Record, created)
				m.Record.View.Task.Status = "running"
				return nil, s.prepareNode(ctx, m, m.Record.Current(), "", "")
			})
			if e != nil {
				return e
			}
			continue
		}
		n := r.Current()
		if n == nil {
			return errors.New("the task has no current node")
		}
		ran, e := s.runTurn(ctx, id, r.View.Task.ProjectID, n)
		if e != nil || !ran {
			return e
		}
	}
	return ctx.Err()
}

// runTurn runs the next queued turn of current node n, if it has one, and settles it; ran is false when there was
// none to claim.
func (s *Service) runTurn(ctx context.Context, id, project uuid.UUID, n *taskmodel.NodeRun) (ran bool, err error) {
	if n.Status == "queued" && s.Hooks != nil {
		// node.before_start (plugins/SPEC.md §4): ask plugins before a node first starts, without waiting inside the
		// task transaction; when blocked, the node becomes blocked with the reason written down.
		agent, task, node := n.AgentID, id, n.ID
		o, e := s.Hooks.Hook(ctx, pluginhost.Intercept, pluginhost.Event("node.before_start", &project, &agent, &task, nil,
			map[string]any{"nodeRunId": node, "stepId": n.StepID}), &project)
		if e != nil {
			return false, e
		}
		if o.Blocked {
			reason := o.Reason
			if reason == "" {
				reason = "No reason given."
			}
			return false, s.blockTask(ctx, id, n.ID, "Plugin "+o.Plugin+" blocked the node from starting: "+reason)
		}
	}
	turn, found, e := s.Store.ClaimTaskTurn(ctx, id, n.RuntimeID)
	if e != nil || !found {
		return false, e
	}
	sink := &turnSink{service: s, ctx: ctx, turn: turn}
	result, e := s.executeTurn(ctx, turn, sink)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if e != nil {
		result.Outcome = executor.OutcomeFailed
		result.ErrorCode = executor.ErrInternal
		result.ErrorMessage = "The execution infrastructure failed."
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
		n := m.Record.ActiveNode(turn.NodeRunID)
		if waiting && result.Outcome == "completed" && !m.Record.Ended() && n != nil && n.Output == nil {
			n.Status = "waiting"
			m.Record.View.Task.Status = "waiting"
			return nil, m.Emit("node.updated", map[string]any{"node": n})
		}
		return s.settleTx(ctx, m, turn, result)
	})
	if e != nil {
		return false, e
	}
	s.KickInbox(turn.RuntimeID)
	if record, e := s.Store.GetTask(ctx, id); e == nil && record.Ended() {
		s.interruptOldTurns(ctx, id)
	}
	return true, nil
}

// blockTask blocks the current node node (the one current node when uuid.Nil) with the reason, and the task with it
// (inside a fork only that branch stops).
func (s *Service) blockTask(ctx context.Context, id, node uuid.UUID, reason string) error {
	_, err := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if m.Record.Ended() {
			m.Unchanged()
			return nil, nil
		}
		n := m.Record.Current()
		if node != uuid.Nil {
			n = m.Record.ActiveNode(node)
		}
		block(m.Record, n, reason)
		if e := s.queueNotice(ctx, m, n, "blocked"); e != nil {
			return nil, e
		}
		return nil, m.Emit("node.updated", map[string]any{"node": n})
	})
	return err
}
func (s *Service) settle(ctx context.Context, t store.TaskTurn, result executor.Result) error {
	_, err := s.Store.MutateTask(ctx, t.TaskID, nil, func(m *store.TaskMutation) (any, error) { return s.settleTx(ctx, m, t, result) })
	return err
}
func (s *Service) settleTx(ctx context.Context, m *store.TaskMutation, t store.TaskTurn, result executor.Result) (any, error) {
	n := m.Record.ActiveNode(t.NodeRunID)
	if m.Record.Ended() || n == nil || n.Status == "blocked" {
		m.Unchanged()
		return nil, nil
	}
	blocked := func(reason string) (any, error) {
		block(m.Record, n, reason)
		if e := s.queueNotice(ctx, m, n, "blocked"); e != nil {
			return nil, e
		}
		return nil, m.Emit("node.updated", map[string]any{"node": n})
	}
	if result.Outcome != executor.OutcomeCompleted {
		// An Inbox interrupt is a change of direction within the same session: the follow-up reminder is already
		// durably queued, so the original turn is not replayed automatically. When stopped by a person (without a new
		// message), the node goes on to the next item if it still has queued items of any priority; otherwise it
		// becomes blocked.
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
			block(m.Record, n, "Stopped by a person.")
		} else {
			block(m.Record, n, "The node run ended abnormally. Any submitted result is kept, but the task does not advance.")
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
		if e := s.queueNotice(ctx, m, n, "blocked"); e != nil {
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
			return blocked("The resumed turn has no valid submission of its own. The existing output is kept, but the node does not advance.")
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
		return blocked("The node's turn ended without submitting a result or leaving an open question.")
	}
	def := m.Record.View.Workflow
	exit, e := def.SelectExit(n.StepID, n.EdgeID, m.Record.EdgeCounts)
	if e != nil {
		return blocked(e.Error())
	}
	edge := exit.Edge
	// A branch's edge into its join: the branch has arrived, and the join waits for the others.
	arrival := edge != nil && n.Branch != nil && m.Record.Fork != nil && edge.To == def.JoinOf(*n.Branch) && m.Record.Fork.BranchRun(*n.Branch) != nil
	// Once the node has submitted its result, close any still-unanswered question; the original message history is
	// kept.
	if e = s.closeQuestion(ctx, m, n, "node_completed"); e != nil {
		return nil, e
	}
	added := len(exit.Edges)
	if edge != nil && !arrival {
		added = 1
	}
	if added > 0 && len(m.Record.View.Nodes)+added > s.Options.MaxVisits {
		return blocked("The task reached its limit of node visits.")
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
	if edge == nil && exit.Fork == nil {
		if e := s.Store.InterruptAllTaskConversations(ctx, m); e != nil {
			return nil, e
		}
		m.Record.View.Task.Status = "completed"
		m.Record.View.Task.EndedAt = &now
		m.Record.View.Cleanup = "pending"
		return nil, s.queueNotice(ctx, m, n, "completed")
	}
	if exit.Fork != nil {
		// The branches start as the driver makes their workspaces (taking the limit on branches at once into account).
		f := &taskmodel.ForkRun{ID: exit.Fork.ID, Join: exit.Fork.Join, SourceNodeRunID: n.ID, Branches: []taskmodel.BranchRun{}}
		for _, fe := range exit.Edges {
			m.Record.EdgeCounts[fe.ID]++
			f.Branches = append(f.Branches, taskmodel.BranchRun{Step: fe.To, EdgeID: fe.ID, State: taskmodel.BranchWaiting})
		}
		m.Record.Fork = f
		m.Record.RemoveCurrent(n.ID)
		m.Record.View.Task.Status = "running"
		return nil, s.keepUnreadInbox(ctx, m, n)
	}
	m.Record.EdgeCounts[edge.ID]++
	if arrival {
		b := m.Record.Fork.BranchRun(*n.Branch)
		b.State = taskmodel.BranchArrived
		arrived := n.ID
		b.ArrivedNodeRunID = &arrived
		m.Record.RemoveCurrent(n.ID)
		return nil, s.keepUnreadInbox(ctx, m, n)
	}
	node, e := s.newNode(ctx, m, edge.To)
	if e != nil {
		return nil, e
	}
	m.Record.ReplaceCurrent(n.ID, *node)
	if n.RuntimeID != node.RuntimeID {
		if e = s.keepUnreadInbox(ctx, m, n); e != nil {
			return nil, e
		}
	}
	return nil, s.prepareNode(ctx, m, node, "", "")
}

// newNode adds the next visit of the step to the task's nodes (not yet current) and returns it.
func (s *Service) newNode(ctx context.Context, m *store.TaskMutation, stepID string) (*taskmodel.NodeRun, error) {
	step, _ := m.Record.View.Workflow.Step(stepID)
	var inst store.TaskInstance
	e := m.Tx.QueryRow(ctx, `SELECT id,agent_id,agent_name FROM task_instances WHERE task_id=$1 AND agent_id=$2`, m.Record.View.Task.ID, step.AgentID).Scan(&inst.ID, &inst.AgentID, &inst.AgentName)
	if e != nil {
		return nil, e
	}
	visit := 1
	for _, old := range m.Record.View.Nodes {
		if old.StepID == step.ID && old.Visit >= visit {
			visit = old.Visit + 1
		}
	}
	m.Record.View.Nodes = append(m.Record.View.Nodes, freshNode(m.Record.View.Workflow, m.Record.View.Task.ID, step, inst, visit, 1))
	return &m.Record.View.Nodes[len(m.Record.View.Nodes)-1], nil
}

// keepUnreadInbox is called when node n ends and its instance has no next node: a queued reminder cancelled with the
// old node still represents unread mail, so the notification is rebound to an unbound Inbox round, never to another
// node.
func (s *Service) keepUnreadInbox(ctx context.Context, m *store.TaskMutation, n *taskmodel.NodeRun) error {
	var trigger uuid.UUID
	e := m.Tx.QueryRow(ctx, `SELECT t.trigger_inbox_message_id FROM task_turns t JOIN inbox_messages i ON i.id=t.trigger_inbox_message_id WHERE t.task_id=$1 AND t.node_run_id=$2 AND t.origin='inbox_reminder' AND t.inbox_scope_node_id IS NULL AND t.status='interrupted' AND t.started_at IS NULL AND i.read_at IS NULL AND i.cancelled_at IS NULL ORDER BY t.queue_seq LIMIT 1`, m.Record.View.Task.ID, n.ID).Scan(&trigger)
	if e != nil && !store.IsNotFound(e) {
		return e
	}
	if e == nil {
		old := store.TaskInstance{ID: n.RuntimeID, TaskID: m.Record.View.Task.ID, AgentID: n.AgentID, AgentName: n.AgentName}
		if _, e = s.Store.EnqueueTaskInboxTurn(ctx, m, old, "You have unhandled task Inbox messages. List the summaries, then read and reply to them one by one; for now only handle the conversation and do not advance any other workflow node.", 1, &trigger); e != nil {
			return e
		}
	}
	return nil
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

func (t *turnSink) Phase(phase, runtime string) {
	t.event(store.EventRunPhase, map[string]any{"phase": phase, "runtime": runtime})
}
func (t *turnSink) warn(err error) {
	if err != nil && t.service.Log != nil {
		log := t.service.Log
		if inst, e := t.service.Store.TaskInstance(t.ctx, t.turn.RuntimeID); e == nil {
			log = log.With("agentId", inst.AgentID)
		}
		log.Warn("failed to write the task run record", "taskId", t.turn.TaskID, "runtimeId", t.turn.RuntimeID, "nodeRunId", taskNodeRef(t.turn.NodeRunID), "runId", t.turn.ID, "instanceId", t.service.Store.InstanceID, "inboxMessageId", t.turn.TriggerInboxID, "requestId", t.turn.RequestID, "error", err)
	}
}
func (t *turnSink) event(kind string, data map[string]any) {
	t.warn(t.service.Store.RecordTaskObservation(t.ctx, t.turn, kind, data))
}
func (t *turnSink) Running(version int64, _ executor.Selection) {
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

// stoppedError marks a turn stopped by a person with user_stopped (not a run failure, and not an interrupt by a new
// message).
func stoppedError(ctx context.Context, m *store.TaskMutation, turn uuid.UUID, outcome string, code, message *string) (*string, *string, error) {
	if outcome != executor.OutcomeInterrupted {
		return code, message, nil
	}
	stopped, err := store.TaskTurnStoppedTx(ctx, m.Tx, turn)
	if err != nil || !stopped {
		return code, message, err
	}
	c, msg := "user_stopped", "Stopped by a person."
	return &c, &msg, nil
}
