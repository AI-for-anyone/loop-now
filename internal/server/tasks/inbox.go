package tasks

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"

	"loop-now/internal/server/db"
)

func (s *Service) ResolveTaskRecipient(ctx context.Context, agentID, taskID uuid.UUID) (store.Party, bool, error) {
	instances, err := s.Store.TaskInstances(ctx, taskID)
	if err != nil {
		return store.Party{}, false, err
	}
	var runtime uuid.UUID
	for _, inst := range instances {
		if inst.AgentID == agentID {
			runtime = inst.ID
		}
	}
	if runtime == uuid.Nil {
		return store.Party{}, false, apperr.NotFound("Agent in this task")
	}
	r, err := s.Store.GetTask(ctx, taskID)
	if err != nil {
		return store.Party{}, false, err
	}
	if !r.Ended() {
		return store.Party{Kind: "agent", ID: agentID, RuntimeID: runtime}, false, nil
	}
	project, err := s.Store.GetProject(ctx, r.View.Task.ProjectID)
	if err != nil {
		return store.Party{}, false, err
	}
	if project.ManagerAgentID == nil {
		return store.Party{}, false, conflict("The task has ended and the project has no Manager yet.")
	}
	return store.AgentParty(*project.ManagerAgentID), true, nil
}

// DeliverInbox saves the message together with the task reminder in the task transaction; a regular message does not
// answer the original question.
func (s *Service) DeliverInbox(ctx context.Context, msg store.NewInboxMessage) error {
	inst, err := s.Store.TaskInstance(ctx, msg.To.RuntimeID)
	if err != nil {
		return err
	}
	if inst.AgentID != msg.To.ID {
		return apperr.NotFound("Agent in this task")
	}
	_, err = s.Store.MutateTask(ctx, inst.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return nil, s.DeliverInboxTx(ctx, m.Tx, m, msg)
	})
	if err == nil {
		s.interruptOldTurns(ctx, inst.TaskID)
		s.Kick(inst.TaskID)
		s.KickInbox(inst.ID)
	}
	return err
}

// DeliverInboxTx reuses the already locked aggregate; it never opens a transaction.
func (s *Service) DeliverInboxTx(ctx context.Context, tx *db.Tx, m *store.TaskMutation, msg store.NewInboxMessage) error {
	inst := store.TaskInstance{ID: msg.To.RuntimeID, AgentID: msg.To.ID, TaskID: m.Record.View.Task.ID}

	if m.Record.Ended() {
		return conflict("The task has ended. Resolve the recipient again.")
	}
	exists, e := store.InboxMessageExistsTx(ctx, m.Tx, msg.ID)
	if e != nil {
		return e
	}
	if !exists {
		if e = store.InsertInboxMessage(ctx, m.Tx, msg); e != nil {
			return e
		}
	}
	if deferred, err := s.Store.DeferTaskLarkInboxTx(ctx, m, msg.ID, inst.ID); err != nil || deferred {
		// Inbox persistence is independent of task version. A deferred receipt
		// must not invalidate the switch's own reservationTaskVersion.
		return err
	}
	m.Changed()
	if m.Record.View.Task.Status == "preparing" && m.Record.ActiveFor(inst.ID) != nil {
		return nil
	}
	priority := 1
	if msg.WhenBusy == "interrupt" {
		priority = 0
	}
	if priority == 0 {
		if e := s.Store.InterruptTaskRuntime(ctx, m, &inst.ID, false); e != nil {
			return e
		}
	}
	turn, e := s.enqueueTaskInboxReminder(ctx, m, inst, priority, msg.ID)
	if e != nil {
		return e
	}
	// This message is carried by this turn; messages still pending in older turns that this interrupt ended are merged
	// into it as well, and the queue list shows them accordingly.
	if e = store.LinkInboxTurnTx(ctx, m.Tx, msg.ID, turn.ID); e != nil {
		return e
	}
	// An answer to the original question, or its expiry notice, is also responsible for waking the node waiting for the
	// question's result: even if the message was read or cancelled first, this turn still runs as usual.
	var resolves bool
	if e = m.Tx.QueryRow(ctx, `SELECT answers_message_id IS NOT NULL OR notice IS NOT NULL FROM inbox_messages WHERE id=$1`, msg.ID).Scan(&resolves); e != nil {
		return e
	}
	if resolves {
		if e = store.MarkQuestionWakeupTx(ctx, m.Tx, turn.ID); e != nil {
			return e
		}
	}
	return store.AdoptTaskInboxOrphansTx(ctx, m.Tx, inst.ID, turn.ID)
}

// enqueueTaskInboxReminder merges a non-strict message addressed to a task instance into a reminder turn, or creates
// one: a node reminder when the instance is the current, unblocked node, otherwise a reminder that only handles the
// conversation.
func (s *Service) enqueueTaskInboxReminder(ctx context.Context, m *store.TaskMutation, inst store.TaskInstance, priority int, trigger uuid.UUID) (store.TaskTurn, error) {
	n := m.Record.ActiveFor(inst.ID)
	if n == nil || n.Status == "blocked" {
		text := fmt.Sprintf("You have new Inbox messages for task %s. Use loopctl to list the summaries first, then read them one by one and reply through the Inbox. There is no workflow node for you to advance right now; only handle the conversation, and do not run or submit any other node. After an interruption, the old execution is not resumed automatically.", inst.TaskID)
		return s.Store.EnqueueTaskInboxTurn(ctx, m, inst, text, priority, &trigger)
	}
	text := fmt.Sprintf("You have new Inbox messages. Use loopctl to list the summaries first, then read them one by one. Task: %s; current node run: %s. After an interruption, continue with the new messages; the interrupted execution is not resumed automatically.", inst.TaskID, n.ID)
	return s.Store.EnqueueTaskTurnFor(ctx, m, n, text, "inbox_reminder", priority, &trigger)
}
func (s *Service) InboxCommitted(ctx context.Context, runtime uuid.UUID) {
	inst, err := s.Store.TaskInstance(ctx, runtime)
	if err != nil {
		return
	}
	s.interruptOldTurns(ctx, inst.TaskID)
	s.Kick(inst.TaskID)
	s.KickInbox(runtime)
}
