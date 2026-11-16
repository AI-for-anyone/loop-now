package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/taskmodel"
)

// unreadCond is the condition "sent to the recipient and still pending": unread and not cancelled by a person.
// Cancelled messages do not count as unread.
func unreadCond(alias string) string {
	return alias + ".read_at IS NULL AND " + alias + ".cancelled_at IS NULL"
}

// queueQuerier is a transaction or a connection pool.
type queueQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (*db.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) *db.Row
	Exec(ctx context.Context, sql string, args ...any) (db.Result, error)
}

// requireLiveAgent confirms the Agent exists and is not deleted. Queue actions all run in write transactions,
// serialized with dequeueing, ending runs and writing messages, so the row does not need to be locked.
func requireLiveAgent(ctx context.Context, tx *db.Tx, agentID uuid.UUID) error {
	var live bool
	err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1 AND deleted_at IS NULL`, agentID).Scan(&live)
	return notFoundIfNoRows(err, "Agent")
}

// QueueItem is one item in the queue list: a message that is still pending.
type QueueItem struct {
	Kind      string // run: a message the console sent in the conversation; inbox: an Inbox message
	RunID     uuid.UUID
	MessageID *uuid.UUID
	From      PartyView
	Text      string
	Images    []messageimage.Ref
	WhenBusy  string
	QueuedAt  time.Time
}

func whenBusyOf(priority int) string {
	if priority == 0 {
		return "interrupt"
	}
	return "queue"
}

func inboxItem(m InboxMessage, run uuid.UUID, priority int) QueueItem {
	id := m.ID
	images := m.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: "inbox", RunID: run, MessageID: &id, From: m.From, Text: m.Text, Images: images, WhenBusy: whenBusyOf(priority), QueuedAt: m.CreatedAt}
}

// AgentQueue returns the queue list of a regular Agent in actual handling order: runs by priority and queue_seq,
// messages within the same reminder in sending order. Sleep wake runs are not listed.
func (s *Store) AgentQueue(ctx context.Context, agentID uuid.UUID) ([]QueueItem, error) {
	return agentQueue(ctx, s.Pool, agentID)
}

// AgentQueueTx reads the queue list in a transaction (see AgentQueue).
func (s *Store) AgentQueueTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID) ([]QueueItem, error) {
	return agentQueue(ctx, tx, agentID)
}

func agentQueue(ctx context.Context, q queueQuerier, agentID uuid.UUID) ([]QueueItem, error) {
	type queued struct {
		id       uuid.UUID
		origin   string
		priority int
		queuedAt time.Time
		text     string
		images   []byte
	}
	rows, err := q.Query(ctx, `SELECT r.id, r.origin, r.priority, r.queued_at, cm.text, cm.images FROM runs r
		JOIN conversation_messages cm ON cm.id = r.user_message_id
		WHERE r.agent_id = $1 AND r.status = 'queued' AND r.origin <> 'sleep_wake' ORDER BY r.priority, r.queue_seq`, agentID)
	if err != nil {
		return nil, err
	}
	runs, err := db.CollectRows(rows, func(r db.CollectableRow) (queued, error) {
		var x queued
		return x, r.Scan(&x.id, &x.origin, &x.priority, &x.queuedAt, &x.text, &x.images)
	})
	if err != nil {
		return nil, err
	}
	reminders := []uuid.UUID{}
	for _, r := range runs {
		if r.origin == "inbox_reminder" {
			reminders = append(reminders, r.id)
		}
	}
	byRun := map[uuid.UUID][]InboxMessage{}
	if len(reminders) > 0 {
		rows, err := q.Query(ctx, inboxSelect(200)+` WHERE m.reminder_run_id IN (SELECT value FROM json_each($1)) AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, reminders)
		if err != nil {
			return nil, err
		}
		msgs, err := db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			byRun[*m.ReminderRunID] = append(byRun[*m.ReminderRunID], m)
		}
	}
	out := []QueueItem{}
	for _, r := range runs {
		if r.origin == "console" {
			it := QueueItem{Kind: "run", RunID: r.id, From: PartyView{Party: ConsoleParty}, Text: r.text, Images: []messageimage.Ref{}, WhenBusy: whenBusyOf(r.priority), QueuedAt: r.queuedAt}
			if err := json.Unmarshal(r.images, &it.Images); err != nil {
				return nil, err
			}
			out = append(out, it)
			continue
		}
		for _, m := range byRun[r.id] {
			out = append(out, inboxItem(m, r.id, r.priority))
		}
	}
	return out, nil
}

// consoleRunItem returns the queue item for a console run (the receipt content on cancellation).
func consoleRunItem(ctx context.Context, q queueQuerier, r Run) (QueueItem, error) {
	m, err := scanMessage(q.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages WHERE id = $1`, r.UserMessageID))
	if err != nil {
		return QueueItem{}, err
	}
	images := m.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: "run", RunID: r.ID, From: PartyView{Party: ConsoleParty}, Text: m.Text, Images: images, WhenBusy: whenBusyOf(r.Priority), QueuedAt: r.QueuedAt}, nil
}

// voidRunIfEmptyTx voids a queued reminder that has no pending messages and does not carry a sleep wake, and writes the
// end event; returns whether it was voided.
func (s *Store) voidRunIfEmptyTx(ctx context.Context, tx *db.Tx, runID uuid.UUID, reason string) (bool, error) {
	r, err := scanRun(tx.QueryRow(ctx, `UPDATE runs AS r SET status = 'interrupted', end_reason = $2, ended_at = $3
		WHERE r.id = $1 AND r.status = 'queued' AND r.origin = 'inbox_reminder' AND r.wake_sleep_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM inbox_messages m WHERE m.reminder_run_id = r.id AND `+unreadCond("m")+`)
		RETURNING `+runColumns, runID, reason, tx.Now()))
	if errors.Is(err, db.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, s.insertEvent(ctx, tx, LifecycleEvent{AgentID: r.AgentID, RunID: &r.ID, Type: EventRunEnded, At: *r.EndedAt,
		Data: map[string]any{"status": r.Status, "endReason": reason}})
}

// taskTurnPendingSQL is the condition "turn t still carries pending messages": non-strict messages are matched by
// reminder_turn_id, strict node messages by their binding.
const taskTurnPendingSQL = `(EXISTS (SELECT 1 FROM inbox_messages m WHERE m.reminder_turn_id = t.id AND m.read_at IS NULL AND m.cancelled_at IS NULL)
	OR EXISTS (SELECT 1 FROM task_node_inbox_messages b JOIN inbox_messages m ON m.id = b.message_id
		WHERE b.reminder_run_id = t.id AND b.not_executed_at IS NULL AND m.read_at IS NULL AND m.cancelled_at IS NULL))`

// voidTaskTurnIfEmptyTx voids a queued reminder turn that has no pending messages and carries neither a sleep wake nor
// a question-ended wake (TaskMessage.voidReason=reason); returns whether it was voided.
func (s *Store) voidTaskTurnIfEmptyTx(ctx context.Context, m *TaskMutation, turnID uuid.UUID, reason string) (bool, error) {
	t, err := scanTaskTurn(m.Tx.QueryRow(ctx, `UPDATE task_turns AS t SET status = 'interrupted', ended_at = $3, void_reason = $2
		WHERE t.id = $1 AND t.status = 'queued' AND t.origin = 'inbox_reminder' AND t.wake_sleep_id IS NULL AND NOT t.question_wakeup AND NOT `+taskTurnPendingSQL+`
		RETURNING `+taskTurnColumns, turnID, reason, m.Tx.Now()))
	if errors.Is(err, db.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	m.Changed()
	if err = s.taskEndedEvent(ctx, m, t); err != nil {
		return false, err
	}
	if t.NodeRunID != uuid.Nil {
		return true, m.Emit("message.created", taskMessage(t, false))
	}
	return true, nil
}

// LinkInboxReminderTx records the platform reminder (regular Agent) that carries this Inbox message.
func LinkInboxReminderTx(ctx context.Context, tx *db.Tx, messageID, runID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages SET reminder_run_id = $2, reminder_turn_id = NULL WHERE id = $1`, messageID, runID)
	return err
}

// LinkInboxTurnTx records the task turn (non-strict message of a task instance) that carries this Inbox message.
func LinkInboxTurnTx(ctx context.Context, tx *db.Tx, messageID, turnID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages SET reminder_turn_id = $2, reminder_run_id = NULL WHERE id = $1`, messageID, turnID)
	return err
}

// AdoptTaskInboxOrphansTx attaches to turn the non-strict messages sent to a task instance that are still pending but
// have no queued carrying turn (for example because their turn was interrupted or ended with a node change).
func AdoptTaskInboxOrphansTx(ctx context.Context, tx *db.Tx, runtime, turn uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE inbox_messages AS m SET reminder_turn_id = $2 WHERE m.to_kind = 'agent' AND m.to_runtime_id = $1 AND `+unreadCond("m")+`
		AND NOT EXISTS (SELECT 1 FROM task_node_inbox_messages b WHERE b.message_id = m.id)
		AND (m.reminder_turn_id IS NULL OR EXISTS (SELECT 1 FROM task_turns o WHERE o.id = m.reminder_turn_id AND o.status IN ('completed', 'failed', 'interrupted')))`, runtime, turn)
	return err
}

// adoptAgentInboxOrphansTx attaches to run the messages sent to a regular Agent that are still pending but have no
// queued or in-progress carrying reminder.
func adoptAgentInboxOrphansTx(ctx context.Context, q queueQuerier, agentID, run uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE inbox_messages AS m SET reminder_run_id = $2 WHERE m.to_kind = 'agent' AND m.to_agent_id = $1 AND m.to_runtime_id IS NULL AND `+unreadCond("m")+`
		AND (m.reminder_run_id IS NULL OR EXISTS (SELECT 1 FROM runs o WHERE o.id = m.reminder_run_id AND o.status IN ('completed', 'failed', 'interrupted')))`, agentID, run)
	return err
}

// ReminderCarried is what a platform reminder names when it starts: the pending messages this turn carries, and the
// number of other messages still unread.
type ReminderCarried struct {
	Messages []InboxMessage
	Others   int
}

// PrepareReminderStart is called when a reminder run starts: it first adopts pending messages that have no carrying
// reminder, then returns the messages this turn carries and the number of other unread ones.
func (s *Store) PrepareReminderStart(ctx context.Context, agentID, run uuid.UUID) (ReminderCarried, error) {
	var out ReminderCarried
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := adoptAgentInboxOrphansTx(ctx, tx, agentID, run); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, inboxSelect(200)+` WHERE m.reminder_run_id = $1 AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, run)
		if err != nil {
			return err
		}
		if out.Messages, err = db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) }); err != nil {
			return err
		}
		n, err := countUnread(ctx, tx, AgentParty(agentID))
		out.Others = n - len(out.Messages)
		return err
	})
	return out, err
}

// taskTurnCarriedTx returns the pending messages a task turn carries (strict node messages and non-strict messages),
// in sending order.
func taskTurnCarriedTx(ctx context.Context, tx *db.Tx, turn uuid.UUID) ([]InboxMessage, error) {
	rows, err := tx.Query(ctx, inboxSelect(200)+` WHERE (m.reminder_turn_id = $1 OR m.id IN (SELECT b.message_id FROM task_node_inbox_messages b WHERE b.reminder_run_id = $1 AND b.not_executed_at IS NULL))
		AND `+unreadCond("m")+` ORDER BY m.created_at, m.id`, turn)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (InboxMessage, error) { return scanInbox(r) })
}

// PartyLabel is how a reminder names the sender: the address if there is one, otherwise the name.
func PartyLabel(v PartyView) string {
	switch v.Kind {
	case "console":
		return "@console"
	case "external":
		return "@" + v.Name
	case "plugin":
		return PluginAddress(v)
	case "agent":
		if addr, ok := naming.AgentAddress(v.OrgName, v.ProjectName, v.Name, v.Platform); ok {
			if v.TaskID != nil {
				addr += "#" + v.TaskID.String()
			}
			return addr
		}
	}
	return v.Name
}

// ReminderList is the list of messages a reminder names: one line each, with the message ID, the sender and a preview
// of the text.
func ReminderList(msgs []InboxMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		summary := strings.Join(strings.Fields(m.Text), " ")
		if r := []rune(summary); len(r) > 60 {
			summary = string(r[:60]) + "…"
		}
		if summary == "" && len(m.Images) > 0 {
			summary = fmt.Sprintf("[%d images]", len(m.Images))
		}
		fmt.Fprintf(&b, "- %s (from %s): %s\n", m.ID, PartyLabel(m.From), summary)
	}
	return strings.TrimRight(b.String(), "\n")
}

// taskReminderText gives the named content when a turn starts; base is the turn's original prompt.
func taskReminderText(base string, carried []InboxMessage, others int) string {
	text := base + "\nFirst handle the following " + fmt.Sprint(len(carried)) + " Inbox messages this turn:\n" + ReminderList(carried)
	if others > 0 {
		text += fmt.Sprintf("\n%d more unread messages are already queued; you will be reminded of them later, so you need not handle them this turn.", others)
	}
	return text
}

// prepareTaskReminderClaimTx is called when a reminder turn is claimed: a non-strict reminder first adopts pending
// messages that have no carrying turn; when there are no pending messages and it carries no wake, this turn is voided
// and skip is returned; otherwise the prompt names the messages this turn carries.
func (s *Store) prepareTaskReminderClaimTx(ctx context.Context, m *TaskMutation, t *TaskTurn) (bool, error) {
	if t.Origin != "inbox_reminder" {
		return false, nil
	}
	var scoped, question bool
	if err := m.Tx.QueryRow(ctx, `SELECT inbox_scope_node_id IS NOT NULL, question_wakeup FROM task_turns WHERE id = $1`, t.ID).Scan(&scoped, &question); err != nil {
		return false, err
	}
	if !scoped {
		if err := AdoptTaskInboxOrphansTx(ctx, m.Tx, t.RuntimeID, t.ID); err != nil {
			return false, err
		}
	}
	carried, err := taskTurnCarriedTx(ctx, m.Tx, t.ID)
	if err != nil {
		return false, err
	}
	if len(carried) == 0 {
		if t.WakeSleepID != nil || question {
			return false, nil
		}
		voided, err := s.voidTaskTurnIfEmptyTx(ctx, m, t.ID, "no_pending_messages")
		return voided, err
	}
	var others int
	if err = m.Tx.QueryRow(ctx, `SELECT count(*) FROM inbox_messages m WHERE m.to_kind = 'agent' AND m.to_runtime_id = $1 AND `+unreadCond("m"), t.RuntimeID).Scan(&others); err != nil {
		return false, err
	}
	t.Text = taskReminderText(t.Text, carried, others-len(carried))
	_, err = m.Tx.Exec(ctx, `UPDATE task_turns SET text = $2 WHERE id = $1`, t.ID, t.Text)
	return false, err
}

// TaskQueueItem converts a queue item into the structure used for the node queue list and receipts.
func (s *Store) TaskQueueItem(it QueueItem) taskmodel.QueueItem { return taskQueueItem(it) }

// taskQueueItem is one item in a node's queue list (the structure used by task events and the API).
func taskQueueItem(it QueueItem) taskmodel.QueueItem {
	from, _ := json.Marshal(partyWire(it.From))
	images := it.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return taskmodel.QueueItem{Kind: it.Kind, RunID: it.RunID, MessageID: it.MessageID, From: from, Text: it.Text, Images: images, WhenBusy: it.WhenBusy, QueuedAt: it.QueuedAt}
}

// taskQueueItemsTx returns a node's queue list: the pending messages carried by the node's queued reminder turns, one
// by one, in claim order.
func taskQueueItemsTx(ctx context.Context, tx *db.Tx, task, node uuid.UUID) ([]QueueItem, error) {
	type turn struct {
		id       uuid.UUID
		priority int
	}
	rows, err := tx.Query(ctx, `SELECT t.id, t.priority FROM task_turns t WHERE t.task_id = $1 AND t.node_run_id = $2 AND t.status = 'queued' AND t.origin = 'inbox_reminder'
		ORDER BY t.priority, t.queue_seq`, task, node)
	if err != nil {
		return nil, err
	}
	turns, err := db.CollectRows(rows, func(r db.CollectableRow) (turn, error) {
		var x turn
		return x, r.Scan(&x.id, &x.priority)
	})
	if err != nil {
		return nil, err
	}
	out := []QueueItem{}
	for _, t := range turns {
		msgs, err := taskTurnCarriedTx(ctx, tx, t.id)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			out = append(out, inboxItem(m, t.id, t.priority))
		}
	}
	return out, nil
}

// queueNotPending returns run_not_queued: the queue item is no longer in the queue.
func queueNotPending(runID *uuid.UUID, status, whenBusy string, message *uuid.UUID, state string) error {
	e := apperr.Conflict(apperr.CodeRunNotQueued, "该消息已开始处理、已读或已取消，不在排队中")
	d := RunNotQueuedDetails{RunID: runID, MessageID: message, MessageState: state}
	if status != "" {
		d.Status = &status
	}
	if whenBusy != "" {
		d.WhenBusy = &whenBusy
	}
	e.Details = d
	return e
}

// MarkQuestionWakeupTx records that this reminder turn is also responsible for waking the node after the node's own
// question ends: it runs as usual even without pending messages.
func MarkQuestionWakeupTx(ctx context.Context, tx *db.Tx, turn uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE task_turns SET question_wakeup = true WHERE id = $1`, turn)
	return err
}
