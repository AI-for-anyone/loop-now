package conversation

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// Queue actions of the console: list items one by one, promote to interrupt, withdraw or cancel, and stop the current
// reply. Every write carries an actionId: replaying the same action returns the first receipt and has no further side
// effects; events and interrupts only run after the commit in which the action actually took effect.

// Queue returns the queue list in the actual processing order.
func (s *Service) Queue(ctx context.Context, agentID uuid.UUID) (apitypes.AgentQueue, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return apitypes.AgentQueue{}, err
	}
	items, err := s.st.AgentQueue(ctx, agentID)
	if err != nil {
		return apitypes.AgentQueue{}, err
	}
	return apitypes.AgentQueue{AgentID: agentID, Items: apitypes.FromQueueItems(items)}, nil
}

// queueAction performs a queue action in one transaction: fn returns the receipt (everything except status), and this
// function adds the action information and the queue list after the action.
func (s *Service) queueAction(ctx context.Context, agentID, actionID uuid.UUID, op string, target uuid.UUID,
	fn func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error)) (json.RawMessage, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"target": target})
	action := store.AgentAction{ActorKey: "console", ID: actionID, Operation: op, Body: body}
	out, _, err := s.st.AgentQueueAction(ctx, agentID, action, func(tx *db.Tx) (any, error) {
		r, err := fn(tx)
		if err != nil {
			return nil, err
		}
		items, err := s.st.AgentQueueTx(ctx, tx, agentID)
		if err != nil {
			return nil, err
		}
		r.ActionID, r.AgentID, r.Operation, r.Queue = actionID, agentID, op, apitypes.FromQueueItems(items)
		return r, nil
	})
	return out, err
}

func receiptItem(it store.QueueItem) *apitypes.QueueItem {
	v := apitypes.FromQueueItem(it)
	return &v
}

func receiptRun(r store.Run) *apitypes.Run {
	v := apitypes.FromRun(r)
	return &v
}

// PromoteToInterrupt turns a run sent from the console that is still in the normal queue into an interrupt, with the
// same effect as choosing interrupt when sending: in the database it moves after existing interrupts and before all
// normal queued runs, and then the in-flight reply is interrupted.
func (s *Service) PromoteToInterrupt(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	var run store.Run
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueuePromoteRun, runID, func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, item, ch, err := s.st.PromoteQueuedRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		run, changed = r, ch
		status := "accepted"
		if !ch {
			status = "already_interrupt"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(item), Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		s.emitRun(ctx, agentID, run.ID)
		s.emitQueue(ctx, agentID)
		s.interruptActive(ctx, agentID, run.ID)
		s.kick(ctx, agentID)
	}
	return out, err
}

// CancelRun withdraws a message sent from the console that is still queued: the Agent will not receive it. The
// receipt's item carries the original text and images so “编辑” ("Edit") can put them back.
func (s *Service) CancelRun(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueCancelRun, runID, func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, item, ch, err := s.st.CancelQueuedRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		changed = ch
		status := "accepted"
		if !ch {
			status = "already_cancelled"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(item), Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		s.dequeued(ctx, agentID, runID)
		s.emitQueue(ctx, agentID)
	}
	return out, err
}

// StopRun only stops this one in-flight reply without sending a new message: it reuses the interrupt path (clear the Pi
// queue, abort, force the end after a timeout), and queued messages after it are processed as usual.
func (s *Service) StopRun(ctx context.Context, agentID, runID, actionID uuid.UUID) (json.RawMessage, error) {
	changed := false
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueStopRun, runID, func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error) {
		r, ch, err := s.st.StopRunTx(ctx, tx, agentID, runID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		changed = ch
		status := "accepted"
		if !ch {
			status = "already_stopping"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Run: receiptRun(r)}, nil
	})
	if err == nil && changed {
		s.emitRun(ctx, agentID, runID)
		if err := s.co.PublishInterrupt(ctx, agentID.String(), runID.String()); err != nil {
			s.log.Warn("failed to publish the stop command; waiting for the driver loop's periodic check", "agentId", agentID, "err", err)
		}
	}
	return out, err
}

// PromoteInboxMessage turns one queued Inbox message (from any sender) into an interrupt: it is moved into the queued
// interrupt reminder (created if there is none), the original reminder is voided if it becomes empty, and then the
// in-flight reply is interrupted.
func (s *Service) PromoteInboxMessage(ctx context.Context, agentID, messageID, actionID uuid.UUID) (json.RawMessage, error) {
	var res store.InboxPromotion
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueuePromoteInboxMessage, messageID, func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error) {
		var err error
		res, err = s.st.PromoteInboxMessageTx(ctx, tx, agentID, messageID, s.o.InstanceID)
		if err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		status := "accepted"
		if !res.Changed {
			status = "already_interrupt"
		}
		return apitypes.AgentQueueActionReceipt{Status: status, Item: receiptItem(res.Item), Run: receiptRun(res.Target)}, nil
	})
	if err == nil && res.Changed {
		if res.Created {
			if m, err := s.st.GetMessage(ctx, res.Target.UserMessageID); err == nil {
				s.emitMessage(ctx, agentID, m, res.Target)
			}
		}
		if res.Voided != nil {
			s.dequeued(ctx, agentID, *res.Voided)
		}
		s.emitRun(ctx, agentID, res.Target.ID)
		s.emitQueue(ctx, agentID)
		s.interruptActive(ctx, agentID, res.Target.ID)
		s.kick(ctx, agentID)
	}
	return out, err
}

// CancelInboxMessage lets a person cancel one queued Inbox message (from any sender): the recipient Agent no longer
// handles it, and the reminder carrying it is voided if it becomes empty.
func (s *Service) CancelInboxMessage(ctx context.Context, agentID, messageID, actionID uuid.UUID) (json.RawMessage, error) {
	var res store.InboxCancellation
	out, err := s.queueAction(ctx, agentID, actionID, apitypes.QueueCancelInboxMessage, messageID, func(tx *db.Tx) (apitypes.AgentQueueActionReceipt, error) {
		var err error
		if res, err = s.st.CancelInboxMessageTx(ctx, tx, agentID, messageID); err != nil {
			return apitypes.AgentQueueActionReceipt{}, err
		}
		r := apitypes.AgentQueueActionReceipt{Status: "accepted", Item: receiptItem(res.Item)}
		if !res.Changed {
			r.Status = "already_cancelled"
		}
		if res.Run != nil {
			r.Run = receiptRun(*res.Run)
		}
		return r, nil
	})
	if err == nil && res.Changed {
		if res.Voided && res.Run != nil {
			s.dequeued(ctx, agentID, res.Run.ID)
		}
		s.emitQueue(ctx, agentID)
		if s.InboxCancelled != nil {
			s.InboxCancelled(ctx, res.Message)
		}
	}
	return out, err
}

// InboxRead is called by the Inbox service after the recipient Agent has read Inbox messages: messages that were read
// leave the queue list, so the new state of the voided reminders is pushed.
func (s *Service) InboxRead(ctx context.Context, eff store.InboxReadEffect) {
	if !eff.QueueChanged || eff.AgentID == uuid.Nil {
		return
	}
	for _, id := range eff.Voided {
		s.dequeued(ctx, eff.AgentID, id)
	}
	s.emitQueue(ctx, eff.AgentID)
}

// dequeued pushes the new state of a withdrawn or voided run (the queue lives in the database; the run is no longer
// queued and will not be picked up again).
func (s *Service) dequeued(ctx context.Context, agentID, runID uuid.UUID) {
	s.emitRun(ctx, agentID, runID)
}
