package conversation

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// ReminderPlaceholder is the body of the user message while the platform reminder turn is queued; it is rewritten to
// the reminder text when the run starts.
const ReminderPlaceholder = "Platform reminder"

// ReminderText builds the reminder text: it names the pending messages carried by this turn (ID, sender and summary)
// and says the remaining unread messages will be reminded about separately later. The command comes from loopctl of the
// same version.
func ReminderText(c store.ReminderCarried) string {
	if len(c.Messages) == 0 {
		return "Platform reminder: there are no Inbox messages to handle this turn."
	}
	text := fmt.Sprintf("Platform reminder: handle the following %d Inbox messages this turn. Read and handle them one by one with `%s <message ID>`; for usage see Skill %s:\n%s",
		len(c.Messages), guide.InboxReadCommand(), guide.SkillName, store.ReminderList(c.Messages))
	if c.Others > 0 {
		text += fmt.Sprintf("\n%d more unread messages are already queued; you will be reminded of them later, so you need not handle them this turn.", c.Others)
	}
	return text
}

// DeliverInbox writes an Inbox message addressed to an Agent and schedules a platform reminder turn in one transaction,
// sharing queueing, the busy lock and the driver loop with console messages: when the Agent is idle the turn starts
// right away; when busy it is queued according to whenBusy, and merged into an already queued reminder of the same
// priority if there is one; an interrupt message still interrupts the current run. When the session is lost only the
// message is saved and no reminder is scheduled; remindUnread adds it after a person confirms reopening the session.
func (s *Service) DeliverInbox(ctx context.Context, msg store.NewInboxMessage) error {
	if msg.To.Kind != "agent" {
		return errors.New("DeliverInbox only handles messages to Agents")
	}
	return s.scheduleReminder(ctx, msg.To.ID, msg.ID, msg.WhenBusy, &msg)
}

// remindUnread is called after a person confirms reopening the session: when the Agent still has unread Inbox messages
// it schedules a reminder turn right away, with the trigger message set to the earliest unread message (it could not
// trigger a reminder while the session was lost). Failures are only logged and do not affect reopening the session.
func (s *Service) remindUnread(ctx context.Context, agentID uuid.UUID) {
	id, ok, err := s.st.EarliestUnreadInbox(ctx, agentID)
	if err == nil && ok {
		err = s.scheduleReminder(ctx, agentID, id, "queue", nil)
	}
	if err != nil {
		s.log.Warn("failed to schedule a platform reminder after the session was reopened", "agentId", agentID, "err", err)
	}
}

// scheduleReminder schedules a platform reminder turn; when msg is non-nil it writes that Inbox message in the same
// transaction.
func (s *Service) scheduleReminder(ctx context.Context, agentID, triggerID uuid.UUID, whenBusy string, msg *store.NewInboxMessage) error {
	if s.ctx.Err() != nil {
		return errors.New("the server is shutting down")
	}
	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.Acquire(ctx, agentID.String(), owner)
	if err != nil {
		return err
	}
	rem := store.NewRun{AgentID: agentID, RunID: runID, Text: ReminderPlaceholder, InstanceID: s.o.InstanceID, RequestID: reqid.From(ctx),
		TriggerInboxID: &triggerID}
	if acquired {
		rem.Start = true
	} else {
		s.queueReminder(ctx, &rem, whenBusy)
	}
	out, err := s.st.InsertInboxWithReminder(ctx, msg, rem)
	if acquired && (err != nil || !out.Created) {
		_, _ = s.co.Release(ctx, agentID.String(), owner)
		if errors.Is(err, store.ErrActiveRunExists) || errors.Is(err, store.ErrQueuedRunsExist) {
			// The lock is free, but the database has an in-flight or queued run (e.g. right after a restart, or between
			// handoffs): restore the lock from the database, then queue as "busy".
			s.restoreLock(ctx, agentID)
			rem.Start, rem.RunID = false, uuid.Must(uuid.NewV7())
			s.queueReminder(ctx, &rem, whenBusy)
			out, err = s.st.InsertInboxWithReminder(ctx, msg, rem)
			acquired = false
		}
	}
	if err != nil {
		return err
	}
	switch {
	case out.SessionMissing:
		s.log.Warn("Agent session is missing; the Inbox message is saved but no reminder was scheduled; it is added once the session is reopened", "agentId", agentID, "inboxMessageId", triggerID)
	case out.Created:
		s.emitMessage(ctx, agentID, *out.Message, *out.Run)
		s.emitRun(ctx, agentID, out.Run.ID)
		if acquired {
			s.startDriver(agentID, *out.Run, owner)
			return nil
		}
		s.emitQueue(ctx, agentID)
		if whenBusy == "interrupt" {
			s.interruptActive(ctx, agentID, out.Run.ID)
		}
	case out.Merged:
		s.emitQueue(ctx, agentID)
		if whenBusy == "interrupt" {
			s.interruptActive(ctx, agentID, out.Run.ID)
		}
	}
	s.kick(ctx, agentID)
	return nil
}

// queueReminder makes the reminder queued: according to whenBusy it goes after the interrupt runs or the normal runs.
func (s *Service) queueReminder(_ context.Context, rem *store.NewRun, whenBusy string) {
	rem.WhenBusy = whenBusy
}

// prepareReminder is called when the platform reminder turn starts running: it first adopts pending messages not
// carried by any reminder, then names the messages carried by this turn in the reminder text, rewrites the user message
// to the reminder text and pushes it, and returns the text to hand to the executor. When this turn has no pending
// messages left and does not carry a sleep wake, it returns void: the turn is voided (no_pending_messages) and Pi is
// not started.
func (s *Service) prepareReminder(ctx context.Context, agentID uuid.UUID, run store.Run) (text string, void bool, err error) {
	c, err := s.st.PrepareReminderStart(ctx, agentID, run.ID)
	if err != nil {
		return "", false, err
	}
	if len(c.Messages) == 0 && run.WakeSleepID == nil {
		return "", true, nil
	}
	text = ReminderText(c)
	m, err := s.st.SetMessageText(ctx, run.UserMessageID, text)
	if err != nil {
		return "", false, err
	}
	s.emitMessage(ctx, agentID, m, run) // push the same message ID again; the console updates the body by ID
	return text, false, nil
}

// outcomeVoided means there were no pending messages left when the reminder started: the turn is voided
// (no_pending_messages) and Pi is not started.
const outcomeVoided = "voided"

// InboxCommitted wakes durable queue work prepared by the shared Inbox transaction.
func (s *Service) InboxCommitted(ctx context.Context, agentID uuid.UUID) {
	s.emitQueue(ctx, agentID)
	s.kick(ctx, agentID)
}
