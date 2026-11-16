package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// AgentAction is the idempotency key and content of one console queue action, with the same contract as task actions:
// replaying the same action ID returns the first receipt, and changing the content returns 409.
type AgentAction struct {
	ActorKey  string
	ID        uuid.UUID
	Operation string
	Body      []byte
}

// AgentQueueAction runs the queue action fn in one transaction and records the receipt it returns. Replaying the same
// action returns the first receipt (replayed=true) without calling fn again; reusing the same action ID with different
// content returns 409 action_conflict. When fn returns an error the transaction is rolled back and no receipt is
// recorded.
func (s *Store) AgentQueueAction(ctx context.Context, agentID uuid.UUID, a AgentAction, fn func(tx *db.Tx) (any, error)) (json.RawMessage, bool, error) {
	if a.ID == uuid.Nil || a.ActorKey == "" || a.Operation == "" {
		return nil, false, apperr.Invalid("actionId", "A valid actionId is required.")
	}
	hash := sha256.Sum256(append(append([]byte(nil), a.Body...), agentID.String()...))
	var out json.RawMessage
	replayed := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		out, replayed = nil, false
		var op string
		var stored, response []byte
		err := tx.QueryRow(ctx, `SELECT operation, body_hash, response FROM agent_actions WHERE actor_key = $1 AND action_id = $2`, a.ActorKey, a.ID).Scan(&op, &stored, &response)
		switch {
		case err == nil:
			if op != a.Operation || !bytes.Equal(stored, hash[:]) {
				return apperr.Conflict(apperr.CodeActionConflict, "This actionId was already used for a different request.")
			}
			out, replayed = response, true
			return nil
		case !errors.Is(err, db.ErrNoRows):
			return err
		}
		value, err := fn(tx)
		if err != nil {
			return err
		}
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		// Return the stored receipt, so the first response and a replay are byte-for-byte identical.
		return tx.QueryRow(ctx, `INSERT INTO agent_actions (actor_key, action_id, operation, agent_id, body_hash, response) VALUES ($1, $2, $3, $4, $5, $6) RETURNING response`,
			a.ActorKey, a.ID, a.Operation, agentID, hash[:], json.RawMessage(b)).Scan(&out)
	})
	return out, replayed, err
}

func lockAgentRunTx(ctx context.Context, tx *db.Tx, agentID, runID uuid.UUID) (Run, error) {
	if err := requireLiveAgent(ctx, tx, agentID); err != nil {
		return Run{}, err
	}
	r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1 AND agent_id = $2`, runID, agentID))
	return r, notFoundIfNoRows(err, "Run")
}

func runNotQueued(r Run) error {
	return queueNotPending(&r.ID, r.Status, r.WhenBusy, nil, "")
}

// PromoteQueuedRunTx turns a run sent from the console that is still in the normal queue into an interrupt: queue_seq
// takes a new value, placing it after the existing interrupts and before all normal queued items.
// When it is already an interrupt, changed=false (nothing is changed). item is the queue item before the conversion.
func (s *Store) PromoteQueuedRunTx(ctx context.Context, tx *db.Tx, agentID, runID uuid.UUID) (Run, QueueItem, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, QueueItem{}, false, err
	}
	if r.Origin != "console" {
		return r, QueueItem{}, false, apperr.Invalid("runId", "Only a queued message sent from the console can be turned into an interrupt as a whole. Convert Inbox messages one at a time.")
	}
	item, err := consoleRunItem(ctx, tx, r)
	if err != nil {
		return r, item, false, err
	}
	if r.Status != "queued" {
		return r, item, false, runNotQueued(r)
	}
	if r.Priority == 0 {
		return r, item, false, nil
	}
	seq, err := tx.NextSeq(ctx, "runs.queue_seq")
	if err != nil {
		return r, item, false, err
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET priority = 0, when_busy = 'interrupt', queue_seq = $2 WHERE id = $1 RETURNING `+runColumns, runID, seq)); err != nil {
		return r, item, false, err
	}
	return r, item, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunPromoted, Data: map[string]any{"from": "queue", "to": "interrupt"}})
}

// CancelQueuedRunTx withdraws a run sent from the console that is still queued: it ends as interrupted with
// user_cancelled, and the Agent never receives it.
// When already withdrawn, changed=false. item is this message's content, which "编辑" ("Edit") puts back into the input.
func (s *Store) CancelQueuedRunTx(ctx context.Context, tx *db.Tx, agentID, runID uuid.UUID) (Run, QueueItem, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, QueueItem{}, false, err
	}
	if r.Origin != "console" {
		return r, QueueItem{}, false, apperr.Invalid("runId", "Only a queued message sent from the console can be withdrawn. Cancel Inbox messages one at a time.")
	}
	item, err := consoleRunItem(ctx, tx, r)
	if err != nil {
		return r, item, false, err
	}
	if r.Status == "interrupted" && r.EndReason == "user_cancelled" {
		return r, item, false, nil
	}
	if r.Status != "queued" {
		return r, item, false, runNotQueued(r)
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'interrupted', end_reason = 'user_cancelled', ended_at = $2 WHERE id = $1 RETURNING `+runColumns, runID, tx.Now())); err != nil {
		return r, item, false, err
	}
	return r, item, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunEnded, At: *r.EndedAt,
		Data: map[string]any{"status": r.Status, "endReason": r.EndReason}})
}

// StopRunTx stops this run in progress: sets it to aborting and records the stop (stop_requested; the
// abort_requested event carries stop:true); the end reason is later recorded as user_stopped.
// When it is already stopping or being interrupted, changed=false; returns 409 run_not_active when the run is not in
// progress.
func (s *Store) StopRunTx(ctx context.Context, tx *db.Tx, agentID, runID uuid.UUID) (Run, bool, error) {
	r, err := lockAgentRunTx(ctx, tx, agentID, runID)
	if err != nil {
		return r, false, err
	}
	switch r.Status {
	case "aborting":
		return r, false, nil
	case "starting", "running":
	default:
		e := apperr.Conflict(apperr.CodeRunNotActive, "This reply has already ended and can no longer be stopped.")
		e.Details = RunNotActiveDetails{RunID: r.ID, Status: r.Status}
		return r, false, e
	}
	if r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'aborting', stop_requested = 1, abort_requested_at = coalesce(abort_requested_at, $2) WHERE id = $1 RETURNING `+runColumns, runID, tx.Now())); err != nil {
		return r, false, err
	}
	return r, true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunAbortRequested, Data: map[string]any{"stop": true}})
}

// lockAgentInboxTx reads one Inbox message sent to a regular Agent (write transactions are serialized, so no lock is
// needed); returns not_found when it does not exist or was not sent to this Agent.
func lockAgentInboxTx(ctx context.Context, tx *db.Tx, agentID, messageID uuid.UUID) (InboxMessage, error) {
	m, err := scanInbox(tx.QueryRow(ctx, inboxSelect(200)+` WHERE m.id = $1 AND m.to_kind = 'agent' AND m.to_agent_id = $2 AND m.to_runtime_id IS NULL`, messageID, agentID))
	return m, notFoundIfNoRows(err, "Message")
}

// queuedCarrierTx returns the still-queued reminder that carries this message; returns run_not_queued when the
// message is cancelled or read, has no carrying reminder, or the reminder has already started.
func queuedCarrierTx(ctx context.Context, tx *db.Tx, m InboxMessage) (Run, error) {
	var run *Run
	if m.ReminderRunID != nil {
		r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, *m.ReminderRunID))
		if err != nil {
			return Run{}, err
		}
		run = &r
	}
	fail := func(state string) error {
		if run == nil {
			return queueNotPending(nil, "", "", &m.ID, state)
		}
		return queueNotPending(&run.ID, run.Status, run.WhenBusy, &m.ID, state)
	}
	switch {
	case m.CancelledAt != nil:
		return Run{}, fail("cancelled")
	case m.ReadAt != nil:
		return Run{}, fail("read")
	case run == nil:
		return Run{}, fail("not_queued")
	case run.Status != "queued":
		return Run{}, fail("dispatched")
	}
	return *run, nil
}

// InboxPromotion is the result of turning one Inbox message individually into an interrupt.
type InboxPromotion struct {
	Item    QueueItem  // queue item before the conversion
	Target  Run        // the interrupt reminder that now carries it
	Created bool       // Target was newly created by this call
	Voided  *uuid.UUID // the original reminder, voided because it no longer has pending messages
	Changed bool
}

// PromoteInboxMessageTx moves one queued Inbox message (from any sender) to a queued interrupt reminder, creating one
// if there is none: queue_seq takes a new value, placing it after the existing interrupts and before all normal queued
// items; the message's whenBusy becomes interrupt, and the original reminder is voided once empty
// (merged_into_interrupt).
// When it is already carried by an interrupt reminder, Changed=false.
func (s *Store) PromoteInboxMessageTx(ctx context.Context, tx *db.Tx, agentID, messageID, instanceID uuid.UUID) (InboxPromotion, error) {
	var out InboxPromotion
	if err := requireLiveAgent(ctx, tx, agentID); err != nil {
		return out, err
	}
	m, err := lockAgentInboxTx(ctx, tx, agentID, messageID)
	if err != nil {
		return out, err
	}
	from, err := queuedCarrierTx(ctx, tx, m)
	if err != nil {
		return out, err
	}
	out.Item = inboxItem(m, from.ID, from.Priority)
	if from.Priority == 0 {
		out.Target = from
		return out, nil
	}
	out.Target, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status = 'queued' AND origin = 'inbox_reminder' AND priority = 0
		ORDER BY queue_seq LIMIT 1`, agentID))
	if errors.Is(err, db.ErrNoRows) {
		_, out.Target, err = s.insertMessageAndRunTx(ctx, tx, NewRun{AgentID: agentID, RunID: uuid.Must(uuid.NewV7()), Text: "Platform reminder", WhenBusy: "interrupt",
			InstanceID: instanceID, TriggerInboxID: &messageID})
		out.Created = true
	}
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE inbox_messages SET reminder_run_id = $2, when_busy = 'interrupt' WHERE id = $1`, messageID, out.Target.ID); err != nil {
		return out, err
	}
	if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &out.Target.ID, Type: EventRunPromoted,
		Data: map[string]any{"from": "queue", "to": "interrupt", "messageId": messageID.String(), "fromRunId": from.ID.String()}}); err != nil {
		return out, err
	}
	voided, err := s.voidRunIfEmptyTx(ctx, tx, from.ID, "merged_into_interrupt")
	if err != nil {
		return out, err
	}
	if voided {
		out.Voided = &from.ID
	}
	out.Changed = true
	return out, nil
}

// InboxCancellation is the result of cancelling one queued Inbox message.
type InboxCancellation struct {
	Message InboxMessage // the message after cancellation
	Item    QueueItem    // the queue item before cancellation, used to refill the input for "编辑" ("Edit")
	Run     *Run         // the reminder that carries it (state after cancellation)
	Voided  bool         // the carrying reminder was voided as a result
	Changed bool
}

// CancelInboxMessageTx lets a person cancel one queued Inbox message (from any sender): records cancelled_at, so it no
// longer counts as unread and the recipient will not handle it; a question with options becomes cancelled at the same
// time. The carrying reminder is voided (user_cancelled) when it has no other pending messages. When already cancelled,
// Changed=false.
func (s *Store) CancelInboxMessageTx(ctx context.Context, tx *db.Tx, agentID, messageID uuid.UUID) (InboxCancellation, error) {
	var out InboxCancellation
	if err := requireLiveAgent(ctx, tx, agentID); err != nil {
		return out, err
	}
	m, err := lockAgentInboxTx(ctx, tx, agentID, messageID)
	if err != nil {
		return out, err
	}
	if m.CancelledAt != nil {
		out.Message = m
		out.Item = inboxItem(m, uuid.Nil, 1)
		if m.ReminderRunID != nil {
			r, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, *m.ReminderRunID))
			if err != nil {
				return out, err
			}
			out.Run, out.Item = &r, inboxItem(m, r.ID, r.Priority)
		}
		return out, nil
	}
	run, err := queuedCarrierTx(ctx, tx, m)
	if err != nil {
		return out, err
	}
	out.Item = inboxItem(m, run.ID, run.Priority)
	if err = cancelInboxTx(ctx, tx, messageID); err != nil {
		return out, err
	}
	if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &run.ID, Type: EventMessageCancelled, Data: map[string]any{"messageId": messageID.String()}}); err != nil {
		return out, err
	}
	if out.Voided, err = s.voidRunIfEmptyTx(ctx, tx, run.ID, "user_cancelled"); err != nil {
		return out, err
	}
	if run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, run.ID)); err != nil {
		return out, err
	}
	out.Run = &run
	if out.Message, err = GetInboxMessageTx(ctx, tx, messageID, 200); err != nil {
		return out, err
	}
	out.Changed = true
	return out, nil
}

// cancelInboxTx records that a person cancelled the message; a question with options that is still open becomes
// cancelled at the same time.
func cancelInboxTx(ctx context.Context, tx *db.Tx, messageID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE inbox_messages SET cancelled_at = $2 WHERE id = $1 AND read_at IS NULL AND cancelled_at IS NULL`, messageID, tx.Now()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE inbox_questions SET state = 'cancelled' WHERE message_id = $1 AND state = 'open'`, messageID)
	return err
}
