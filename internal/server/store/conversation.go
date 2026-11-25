package store

import (
	"context"
	"encoding/json"
	"errors"
	"loop-now/internal/server/messageimage"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// Run is the record of handling one user message.
type Run struct {
	ID               uuid.UUID
	AgentID          uuid.UUID
	UserMessageID    uuid.UUID
	ConfigVersion    int64
	InstanceID       *uuid.UUID
	Priority         int
	WhenBusy         string
	QueueSeq         int64
	Status           string
	Phase            string
	EndReason        string
	ErrorCode        string
	ErrorMessage     string
	QueuedAt         time.Time
	StartedAt        *time.Time // dispatch time
	RunningAt        *time.Time // time the message was handed to Pi
	AbortRequestedAt *time.Time
	EndedAt          *time.Time
	Origin           string // console / inbox_reminder
	WakeSleepID      *uuid.UUID
	TriggerInboxID   *uuid.UUID // platform reminder: the Inbox message that created this queued reminder
	// PiCommand is the command name (without "/") of a /pi:<name> message from the conversation page handed to Pi, or
	// empty when it is not a command; PiCommandHandled means Pi handled the command directly without passing it to the
	// model.
	PiCommand        *string
	PiCommandHandled bool
	// PiCommandNotices holds the notices an extension gave when Pi handled a command directly
	// ({"notices":[...],"truncated":bool}); empty otherwise.
	PiCommandNotices []byte
	// Compaction holds the instructions and result of a manual compaction (origin console_compact) (JSON, see
	// Compaction); empty otherwise.
	Compaction []byte
	// Selection is who ran the turn (JSON, see RunSelection and SelectionInfo); empty until the reply began and for
	// older runs.
	Selection []byte
	// ContextHandoff is the selection of the conversation the turn carried to a new runtime (JSON, see HandoffRecord
	// and HandoffInfo); empty for other runs.
	ContextHandoff []byte
	Tools          []ToolEvent
	Usage          UsageTotals
	Model          *ModelRef // provider and model of the last assistant call; nil when there is none
	// BackgroundTasks are the background tasks the run started; TriggerBackgroundTasks, on a reply the CLI gave by
	// itself, the tasks whose end it followed (nil for other runs). Both are filled with tools and usage.
	BackgroundTasks        []BackgroundTask
	TriggerBackgroundTasks []BackgroundTask
}

// ToolEvent is the status record of one tool call (without arguments or output).
type ToolEvent struct {
	ToolCallID string
	ToolName   string
	State      string
	At         time.Time
}

// Message is one message in a conversation.
type Message struct {
	Images       []messageimage.Ref
	ID           uuid.UUID
	AgentID      uuid.UUID
	Seq          int64
	Role         string
	Text         string
	RunID        uuid.UUID
	SessionEpoch int
	CreatedAt    time.Time
}

// ErrActiveRunExists means the Agent already has a run in progress (the single-run constraint).
var ErrActiveRunExists = errors.New("the Agent already has a run in progress")

// ErrQueuedRunsExist means the Agent still has queued runs that can start: a new message must not skip ahead of them
// and start directly.
var ErrQueuedRunsExist = errors.New("the Agent still has queued runs")

// dispatchableQueued is the condition for a queued run that can start (table name runs, no alias): the sleep wake
// turn must wait until the sleep has actually woken, the same check as in StartQueuedRun.
const dispatchableQueued = `runs.status = 'queued' AND (runs.origin <> 'sleep_wake' OR EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.id = runs.wake_sleep_id AND z.wake_run_id = runs.id AND z.state = 'woken'))`

// runColumnsOf returns the columns of runs, in the order of runFields; prefix is the table alias plus a dot, e.g. "r.".
func runColumnsOf(prefix string) string {
	return strings.NewReplacer("$.", prefix).Replace(`$.id, $.agent_id, coalesce($.user_message_id, '00000000-0000-0000-0000-000000000000'), $.config_version, $.instance_id, $.priority,
	coalesce($.when_busy, ''), $.queue_seq, $.status, coalesce($.phase, ''), coalesce($.end_reason, ''), coalesce($.error_code, ''),
	coalesce($.error_message, ''), $.queued_at, $.started_at, $.running_at, $.abort_requested_at, $.ended_at,
	$.origin, $.trigger_inbox_message_id, $.wake_sleep_id, $.pi_command, $.pi_command_handled, $.pi_command_notices, $.compaction,
	$.selection, $.context_handoff`)
}

var runColumns = runColumnsOf("")

func runFields(r *Run) []any {
	return []any{&r.ID, &r.AgentID, &r.UserMessageID, &r.ConfigVersion, &r.InstanceID, &r.Priority, &r.WhenBusy, &r.QueueSeq, &r.Status,
		&r.Phase, &r.EndReason, &r.ErrorCode, &r.ErrorMessage, &r.QueuedAt, &r.StartedAt,
		&r.RunningAt, &r.AbortRequestedAt, &r.EndedAt, &r.Origin, &r.TriggerInboxID, &r.WakeSleepID, &r.PiCommand, &r.PiCommandHandled, &r.PiCommandNotices, &r.Compaction,
		&r.Selection, &r.ContextHandoff}
}

func scanRun(row db.Scanner) (Run, error) {
	var r Run
	err := row.Scan(runFields(&r)...)
	r.Tools, r.BackgroundTasks = []ToolEvent{}, []BackgroundTask{}
	return r, err
}

const messageColumns = `id, agent_id, seq, role, text, run_id, session_epoch, created_at, images`

func scanMessage(row db.Scanner) (Message, error) {
	var m Message
	var raw []byte
	err := row.Scan(&m.ID, &m.AgentID, &m.Seq, &m.Role, &m.Text, &m.RunID, &m.SessionEpoch, &m.CreatedAt, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &m.Images)
	}
	return m, err
}

// NewRun holds the parameters for inserting a user message and its run.
type NewRun struct {
	ImageIDs   []uuid.UUID
	MaxImages  int
	AgentID    uuid.UUID
	RunID      uuid.UUID
	Text       string
	Start      bool   // true: start directly as starting; false: queue
	WhenBusy   string // interrupt / queue / empty
	InstanceID uuid.UUID
	// RequestID is the ID of the API request that sent this message, recorded in the queued or dispatched event; may be
	// empty.
	RequestID string
	// A non-nil TriggerInboxID means this is a platform reminder turn (origin = inbox_reminder); it records the Inbox
	// message that created it.
	TriggerInboxID *uuid.UUID
	Origin         string
	WakeSleepID    *uuid.UUID
}

// InsertMessageAndRun writes, in one transaction, the user message, its run, and the queued or dispatched event.
// Returns not_found when the Agent does not exist or is deleted, and session_missing when the session is missing;
// when Start is true, returns ErrActiveRunExists if a run is already in progress and ErrQueuedRunsExist if there are
// still queued runs that can start.
func (s *Store) InsertMessageAndRun(ctx context.Context, in NewRun) (Message, Run, error) {
	var msg Message
	var run Run
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var err error
		msg, run, err = s.insertMessageAndRunTx(ctx, tx, in)
		return err
	})
	return msg, run, err
}

func (s *Store) insertMessageAndRunTx(ctx context.Context, tx *db.Tx, in NewRun) (Message, Run, error) {
	var msg Message
	var run Run
	err := func() error {
		var version int64
		if err := tx.QueryRow(ctx, `SELECT config_version FROM agents WHERE id = $1 AND deleted_at IS NULL`, in.AgentID).Scan(&version); err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		var state string
		var epoch int
		if err := tx.QueryRow(ctx, `SELECT state, epoch FROM agent_sessions WHERE agent_id = $1`, in.AgentID).Scan(&state, &epoch); err != nil {
			return err
		}
		if state == "missing" {
			return apperr.Conflict(apperr.CodeSessionMissing, "The session file is missing. Confirm reopening the session in the console first.")
		}
		status, priority := "queued", 1
		if in.WhenBusy == "interrupt" || in.Origin == "background" {
			// A reply the CLI gives by itself goes first: the CLI is giving it now.
			priority = 0
		}
		var startedAt *time.Time
		var instance *uuid.UUID
		now := tx.Now()
		if in.Start {
			var queued bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE agent_id = $1 AND `+dispatchableQueued+`)`, in.AgentID).Scan(&queued); err != nil {
				return err
			}
			if queued {
				return ErrQueuedRunsExist
			}
			status, startedAt, instance = "starting", &now, &in.InstanceID
		}
		queueSeq, err := tx.NextSeq(ctx, "runs.queue_seq")
		if err != nil {
			return err
		}
		messageSeq, err := tx.NextSeq(ctx, "conversation_messages.seq")
		if err != nil {
			return err
		}
		msgID := uuid.Must(uuid.NewV7())
		origin := "console"
		if in.Origin != "" {
			origin = in.Origin
		}
		if in.TriggerInboxID != nil {
			origin = "inbox_reminder"
		}
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, instance_id, priority, when_busy, status, started_at,
				origin, trigger_inbox_message_id, wake_sleep_id, queue_seq, queued_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING `+runColumns,
			in.RunID, in.AgentID, msgID, version, instance, priority, nullIfEmpty(in.WhenBusy), status, startedAt, origin, in.TriggerInboxID, in.WakeSleepID, queueSeq, now))
		if err != nil {
			if isUniqueViolation(err, "runs_one_active_per_agent") {
				return ErrActiveRunExists
			}
			return err
		}
		msg, err = scanMessage(tx.QueryRow(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch, seq, created_at)
			VALUES ($1, $2, 'user', $3, $4, $5, $6, $7) RETURNING `+messageColumns, msgID, in.AgentID, in.Text, run.ID, epoch, messageSeq, now))
		if err != nil {
			return err
		}
		if len(in.ImageIDs) > 0 {
			msg.Images, err = s.BindMessageImagesTx(ctx, tx, ImageOwner{AgentID: in.AgentID}, msgID, in.ImageIDs, in.MaxImages)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(msg.Images)
			if _, err = tx.Exec(ctx, `UPDATE conversation_messages SET images=$2 WHERE id=$1`, msgID, json.RawMessage(raw)); err != nil {
				return err
			}
		}

		ev := LifecycleEvent{AgentID: in.AgentID, RunID: &run.ID, Data: map[string]any{}}
		if in.RequestID != "" {
			ev.Data["requestId"] = in.RequestID
		}
		if in.Start {
			ev.Type, ev.At = EventRunDispatched, *startedAt
		} else {
			ev.Type, ev.At = EventRunQueued, run.QueuedAt
			ev.Data["whenBusy"] = in.WhenBusy
		}
		if err := s.insertEvent(ctx, tx, ev); err != nil {
			return err
		}
		if origin != "sleep_wake" {
			if err = s.WakeSleepForWorkTx(ctx, tx, nil, in.AgentID, uuid.Nil, run.ID); err != nil {
				return err
			}
			run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, run.ID))
			if err != nil {
				return err
			}
		}
		return nil
	}()
	return msg, run, err
}

// ReminderOutcome is the result of arranging a reminder when writing an Inbox message.
type ReminderOutcome struct {
	Created        bool     // a new reminder turn was created: Message and Run are its user message and run
	Merged         bool     // merged into an existing queued reminder of the same priority: Run is that turn
	SessionMissing bool     // the session is missing, so no reminder was arranged; the message is saved as usual
	Message        *Message // the user message, when a new one was created
	Run            *Run
}

// InsertInboxWithReminder writes, in one transaction, an Inbox message for an Agent (no message is written when msg is
// nil) and arranges a reminder: after locking the Agent row, if there is already a queued reminder with the same
// priority, the message is merged into it (its trigger message is left unchanged); otherwise a new turn is created
// from rem. When the session is missing no reminder is arranged and the message is saved as usual. When rem.Start is
// true, the caller already holds the busy lock.
func (s *Store) InsertInboxWithReminder(ctx context.Context, msg *NewInboxMessage, rem NewRun) (ReminderOutcome, error) {
	var out ReminderOutcome
	err := s.inTx(ctx, func(tx *db.Tx) error {
		out = ReminderOutcome{}
		if msg != nil {
			if err := InsertInboxMessage(ctx, tx, *msg); err != nil {
				return err
			}
		} else if _, err := lockAgent(ctx, tx, rem.AgentID); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM agent_sessions WHERE agent_id = $1`, rem.AgentID).Scan(&state); err != nil {
			return err
		}
		if state == "missing" {
			out.SessionMissing = true
			return nil
		}
		priority := 1
		if rem.WhenBusy == "interrupt" {
			priority = 0
		}
		existing, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs
			WHERE agent_id = $1 AND status = 'queued' AND origin = 'inbox_reminder' AND priority = $2`, rem.AgentID, priority))
		switch {
		case err == nil:
			out.Merged, out.Run = true, &existing
			if msg != nil {
				if err := LinkInboxReminderTx(ctx, tx, msg.ID, existing.ID); err != nil {
					return err
				}
			}
			return s.WakeSleepForWorkTx(ctx, tx, nil, rem.AgentID, uuid.Nil, existing.ID)
		case !errors.Is(err, db.ErrNoRows):
			return err
		}
		m, r, err := s.insertMessageAndRunTx(ctx, tx, rem)
		if err != nil {
			return err
		}
		out.Created, out.Message, out.Run = true, &m, &r
		if msg != nil {
			return LinkInboxReminderTx(ctx, tx, msg.ID, r.ID)
		}
		return nil
	})
	return out, err
}

// SetMessageText rewrites the text of a user message (the reminder text is written in when a platform reminder turn
// starts running).
func (s *Store) SetMessageText(ctx context.Context, id uuid.UUID, text string) (Message, error) {
	m, err := scanMessage(s.Pool.QueryRow(ctx, `UPDATE conversation_messages SET text = $2 WHERE id = $1 RETURNING `+messageColumns, id, text))
	return m, notFoundIfNoRows(err, "Message")
}

// GetRun returns a run record with its tool status, total usage and the model used.
func (s *Store) GetRun(ctx context.Context, id uuid.UUID) (Run, error) {
	r, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, id))
	if err != nil {
		return r, notFoundIfNoRows(err, "Run")
	}
	return r, s.fillRuns(ctx, &r)
}

// GetMessage returns one message.
func (s *Store) GetMessage(ctx context.Context, id uuid.UUID) (Message, error) {
	m, err := scanMessage(s.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages WHERE id = $1`, id))
	return m, notFoundIfNoRows(err, "Message")
}

// ActiveRun returns the Agent's run in progress; nil when there is none.
func (s *Store) ActiveRun(ctx context.Context, agentID uuid.UUID) (*Run, error) {
	r, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status IN `+activeRunStatuses, agentID))
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// QueuedRuns returns the queued runs in handling order (interrupts first, then normal ones, each first come first
// served).
func (s *Store) QueuedRuns(ctx context.Context, agentID uuid.UUID) ([]Run, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND status = 'queued' ORDER BY priority, queue_seq`, agentID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (Run, error) { return scanRun(r) })
}

// NextQueuedRun returns the Agent's next queued run that can start (interrupts first, then normal ones, each in queue
// order); ok is false when there is none.
func (s *Store) NextQueuedRun(ctx context.Context, agentID uuid.UUID) (id uuid.UUID, ok bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT id FROM runs WHERE agent_id = $1 AND `+dispatchableQueued+` ORDER BY priority, queue_seq LIMIT 1`, agentID).Scan(&id)
	if errors.Is(err, db.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// AgentsWithQueuedRuns returns the Agents that have queued runs.
func (s *Store) AgentsWithQueuedRuns(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT agent_id FROM runs WHERE status = 'queued'`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, db.RowTo[uuid.UUID])
}

// ActiveRuns returns all runs in progress.
func (s *Store) ActiveRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runColumns+` FROM runs WHERE status IN `+activeRunStatuses)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (Run, error) { return scanRun(r) })
}

// StartQueuedRun turns a queued run into starting and records the dispatched event; returns false when it is no longer
// queued (already handled), guaranteeing it is handled exactly once.
func (s *Store) StartQueuedRun(ctx context.Context, runID, instanceID uuid.UUID) (Run, bool, error) {
	var r Run
	started := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM runs WHERE id=$1`, runID).Scan(&owner); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, owner); err != nil {
			return err
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT origin<>'sleep_wake' OR EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.id=runs.wake_sleep_id AND z.wake_run_id=runs.id AND z.state='woken') FROM runs WHERE id=$1`, runID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return nil
		}
		var err error
		r, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'starting', started_at = $3, instance_id = $2
			WHERE id = $1 AND status = 'queued' RETURNING `+runColumns, runID, instanceID, tx.Now()))
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		started = true
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: r.AgentID, RunID: &r.ID, Type: EventRunDispatched, At: *r.StartedAt})
	})
	if err != nil {
		if isUniqueViolation(err, "runs_one_active_per_agent") {
			return r, false, ErrActiveRunExists
		}
		return r, false, err
	}
	return r, started, nil
}

// SetRunPhase updates the phase while in the starting state and records a phase-started event with the runtime the run
// is being started on.
func (s *Store) SetRunPhase(ctx context.Context, runID uuid.UUID, phase, runtime string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		var agentID uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE runs SET phase = $2 WHERE id = $1 AND status = 'starting' RETURNING agent_id`, runID, phase).Scan(&agentID)
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &runID, Type: EventRunPhase, Data: map[string]any{"phase": phase, "runtime": runtime}})
	})
}

// SetRunRunning turns a run into running (a run being interrupted stays aborting), and records the config version
// actually used, the time it started replying, and the event.
func (s *Store) SetRunRunning(ctx context.Context, runID uuid.UUID, configVersion int64) error {
	return s.SetRunRunningWith(ctx, runID, configVersion, RunStart{})
}

// MarkAborting turns the Agent's run in progress into aborting, records the time the interrupt was requested and the
// event (byRunID is the run whose message caused the interrupt), and returns that run; returns nil when there is no
// run in progress. If the run that caused the interrupt has already been dequeued and started, it does not interrupt
// itself.
func (s *Store) MarkAborting(ctx context.Context, agentID, byRunID uuid.UUID) (*Run, error) {
	var out *Run
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1`, agentID).Scan(&exists); err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return nil
			}
			return err
		}
		r, err := scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = 'aborting', abort_requested_at = coalesce(abort_requested_at, $3)
			WHERE agent_id = $1 AND status IN `+activeRunStatuses+` AND id <> $2 RETURNING `+runColumns, agentID, byRunID, tx.Now()))
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = &r
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &r.ID, Type: EventRunAbortRequested,
			Data: map[string]any{"byRunId": byRunID.String()}})
	})
	return out, err
}

// RunNotQueuedDetails are the details of a run_not_queued conflict: the current state of the queue item. For an Inbox
// message it carries MessageID and MessageState (read / cancelled / dispatched / not_queued), and RunID is the run
// currently carrying the message, or nil when there is none.
type RunNotQueuedDetails struct {
	RunID        *uuid.UUID `json:"runId"`
	Status       *string    `json:"status"`
	WhenBusy     *string    `json:"whenBusy"`
	MessageID    *uuid.UUID `json:"messageId,omitempty"`
	MessageState string     `json:"messageState,omitempty"`
}

// RunNotActiveDetails are the details of a run_not_active conflict: the current status of the run to stop.
type RunNotActiveDetails struct {
	RunID  uuid.UUID `json:"runId"`
	Status string    `json:"status"`
}

// RunStatus returns the current status of a run.
func (s *Store) RunStatus(ctx context.Context, runID uuid.UUID) (string, error) {
	var st string
	err := s.Pool.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, runID).Scan(&st)
	return st, err
}

// RunEnd is the result of a run when it ends.
type RunEnd struct {
	Status        string // completed / failed / interrupted
	EndReason     string // user_interrupt / server_restart (only for interrupted)
	ErrorCode     string
	ErrorMessage  string
	Text          string
	ConfigVersion int64 // unchanged when 0
	// When PiCommand is not empty, it records the command name this turn handed to Pi; PiCommandHandled means Pi
	// handled the command directly.
	PiCommand        string
	PiCommandHandled bool
	// PiCommandNotices holds the notices an extension gave when Pi handled a command directly (a JSON object); nothing
	// is recorded when it is empty.
	PiCommandNotices []byte
	// Compaction is the result of a manual compaction (Outcome and so on), merged into the instructions recorded when
	// the run started; nil when it is not a manual compaction or has no result.
	Compaction *Compaction
}

// FinishRun ends a run in progress, writing the reply and the end event. When the run has already ended it returns
// (run, nil, false, nil) and writes nothing again.
func (s *Store) FinishRun(ctx context.Context, runID uuid.UUID, end RunEnd) (Run, *Message, bool, error) {
	var run Run
	var msg *Message
	finished := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var owner uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM runs WHERE id=$1`, runID).Scan(&owner); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, owner); err != nil {
			return err
		}
		var err error
		// A run stopped by a person (stop_requested) ends as user_stopped, not as user_interrupt (interrupted by a new
		// message).
		run, err = scanRun(tx.QueryRow(ctx, `UPDATE runs SET status = $2, end_reason = CASE WHEN $3 = 'user_interrupt' AND stop_requested THEN 'user_stopped' ELSE $3 END,
				error_code = $4, error_message = $5, phase = NULL, ended_at = $7, config_version = CASE WHEN $6 > 0 THEN $6 ELSE config_version END,
				pi_command = coalesce($8, pi_command), pi_command_handled = $9, pi_command_notices = $10
			WHERE id = $1 AND status IN `+activeRunStatuses+` RETURNING `+runColumns,
			runID, end.Status, nullIfEmpty(end.EndReason), nullIfEmpty(end.ErrorCode), nullIfEmpty(end.ErrorMessage), end.ConfigVersion, tx.Now(), nullIfEmpty(end.PiCommand), end.PiCommandHandled, nullIfEmptyBytes(end.PiCommandNotices)))
		if errors.Is(err, db.ErrNoRows) {
			run, err = scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id = $1`, runID))
			return notFoundIfNoRows(err, "Run")
		}
		if err != nil {
			return err
		}
		finished = true
		if run.Origin == "console_compact" && end.Compaction != nil {
			merged := ParseCompaction(run.Compaction)
			merged.Outcome, merged.SkipReason = end.Compaction.Outcome, end.Compaction.SkipReason
			merged.TokensBefore, merged.EstimatedTokensAfter = end.Compaction.TokensBefore, end.Compaction.EstimatedTokensAfter
			raw, err := json.Marshal(merged)
			if err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `UPDATE runs SET compaction = $2 WHERE id = $1 RETURNING compaction`, run.ID, string(raw)).Scan(&run.Compaction); err != nil {
				return err
			}
		}
		if run.Origin != "console_compact" {
			var epoch int
			if err := tx.QueryRow(ctx, `SELECT epoch FROM agent_sessions WHERE agent_id = $1`, run.AgentID).Scan(&epoch); err != nil {
				return err
			}
			seq, err := tx.NextSeq(ctx, "conversation_messages.seq")
			if err != nil {
				return err
			}
			m, err := scanMessage(tx.QueryRow(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch, seq, created_at)
				VALUES ($1, $2, 'assistant', $3, $4, $5, $6, $7) RETURNING `+messageColumns, uuid.Must(uuid.NewV7()), run.AgentID, end.Text, run.ID, epoch, seq, tx.Now()))
			if err != nil {
				return err
			}
			msg = &m
		}
		data := map[string]any{"status": end.Status}
		if run.EndReason != "" {
			data["endReason"] = run.EndReason
		}
		if end.ErrorCode != "" {
			data["errorCode"] = end.ErrorCode
		}
		if err = s.insertEvent(ctx, tx, LifecycleEvent{AgentID: run.AgentID, RunID: &run.ID, Type: EventRunEnded, At: *run.EndedAt, Data: data}); err != nil {
			return err
		}
		if err = finishHandoffTx(ctx, tx, run); err != nil {
			return err
		}
		_, err = s.FinishSleepTx(ctx, tx, nil, run.ID, end.Status)
		return err
	})
	if err == nil {
		err = s.fillRuns(ctx, &run)
	}
	return run, msg, finished, err
}

// AddToolEvent records one tool status.
func (s *Store) AddToolEvent(ctx context.Context, runID uuid.UUID, ev ToolEvent) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO run_tool_events (run_id, seq, tool_call_id, tool_name, state, at)
		VALUES ($1, coalesce((SELECT max(seq) FROM run_tool_events WHERE run_id = $1), 0) + 1, $2, $3, $4, $5)`,
		runID, ev.ToolCallID, ev.ToolName, ev.State, ev.At)
	return err
}

func (s *Store) toolsFor(ctx context.Context, runIDs []uuid.UUID) (map[uuid.UUID][]ToolEvent, error) {
	out := map[uuid.UUID][]ToolEvent{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT run_id, tool_call_id, tool_name, state, at FROM run_tool_events WHERE run_id IN (SELECT value FROM json_each($1)) ORDER BY run_id, seq`, runIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var ev ToolEvent
		if err := rows.Scan(&id, &ev.ToolCallID, &ev.ToolName, &ev.State, &ev.At); err != nil {
			return nil, err
		}
		out[id] = append(out[id], ev)
	}
	return out, rows.Err()
}

// MessageWithRun is one message in the history together with the run it belongs to.
type MessageWithRun struct {
	Message
	Run Run
}

// MessageQuery is how the conversation history is queried: at most one of Before, After and AroundRun; when all are
// empty, the latest page is fetched.
type MessageQuery struct {
	Before    *uuid.UUID
	After     *uuid.UUID
	AroundRun *uuid.UUID
	Limit     int
}

// MessagePage is one page of messages, plus whether there are more messages before and after it.
type MessagePage struct {
	Items        []MessageWithRun
	HasMore      bool // there are older messages before this page
	HasMoreAfter bool // there are newer messages after this page
	// TurnsBefore is the number of turns before the turns on this page, and TurnsTotal is the total number of turns in
	// the conversation; see turnCountsTx for how they are counted.
	TurnsBefore int64
	TurnsTotal  int64
}

var historySelect = `SELECT m.id, m.agent_id, m.seq, m.role, m.text, m.run_id, m.session_epoch, m.created_at, m.images,
		` + runColumnsOf("r.") + `
	FROM conversation_messages m JOIN runs r ON r.id = m.run_id`

// ListMessages returns one page of messages for the query, oldest first, along with the turn counts. The messages and
// the counts come from the same snapshot.
func (s *Store) ListMessages(ctx context.Context, agentID uuid.UUID, q MessageQuery) (MessagePage, error) {
	var page MessagePage
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return page, err
	}
	err := s.Pool.ReadTx(ctx, func(tx *db.Tx) error {
		var err error
		page, err = listMessagesTx(ctx, tx, agentID, q)
		return err
	})
	if err != nil {
		return page, err
	}
	runs := make([]*Run, len(page.Items))
	for i := range page.Items {
		runs[i] = &page.Items[i].Run
	}
	if err := s.fillRuns(ctx, runs...); err != nil {
		return page, err
	}
	if page.Items == nil {
		page.Items = []MessageWithRun{}
	}
	return page, nil
}

func listMessagesTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID, q MessageQuery) (MessagePage, error) {
	var page MessagePage
	seqOf := func(id uuid.UUID) (int64, error) {
		var seq int64
		err := tx.QueryRow(ctx, `SELECT seq FROM conversation_messages WHERE id = $1 AND agent_id = $2`, id, agentID).Scan(&seq)
		return seq, notFoundIfNoRows(err, "Message")
	}
	var items []MessageWithRun
	// from is the message sequence number where this page starts: the sequence number of the first message when the
	// page has messages; otherwise the position where it would have started.
	var from int64
	switch {
	case q.AroundRun != nil:
		anchor, err := aroundRunAnchor(ctx, tx, agentID, *q.AroundRun)
		if err != nil {
			return page, err
		}
		nBefore := (q.Limit - 1) / 2
		after, err := historyPage(ctx, tx, agentID, `m.seq >= $2 ORDER BY m.seq ASC`, anchor, q.Limit-nBefore+1)
		if err != nil {
			return page, err
		}
		if len(after) > q.Limit-nBefore {
			after, page.HasMoreAfter = after[:q.Limit-nBefore], true
		}
		nBefore = q.Limit - len(after)
		before, err := historyPage(ctx, tx, agentID, `m.seq < $2 ORDER BY m.seq DESC`, anchor, nBefore+1)
		if err != nil {
			return page, err
		}
		if len(before) > nBefore {
			before, page.HasMore = before[:nBefore], true
		}
		reverse(before)
		items, from = append(before, after...), anchor
	case q.After != nil:
		seq, err := seqOf(*q.After)
		if err != nil {
			return page, err
		}
		if items, err = historyPage(ctx, tx, agentID, `m.seq > $2 ORDER BY m.seq ASC`, seq, q.Limit+1); err != nil {
			return page, err
		}
		if len(items) > q.Limit {
			items, page.HasMoreAfter = items[:q.Limit], true
		}
		page.HasMore = true // the anchor message itself is older
		from = seq + 1
	default:
		seq := int64(1<<62 - 1)
		if q.Before != nil {
			var err error
			if seq, err = seqOf(*q.Before); err != nil {
				return page, err
			}
			page.HasMoreAfter = true // the anchor message itself is newer
		}
		var err error
		if items, err = historyPage(ctx, tx, agentID, `m.seq < $2 ORDER BY m.seq DESC`, seq, q.Limit+1); err != nil {
			return page, err
		}
		if len(items) > q.Limit {
			items, page.HasMore = items[:q.Limit], true
		}
		reverse(items)
		from = seq
	}
	if len(items) > 0 {
		from = items[0].Seq
	}
	page.Items = items
	var err error
	page.TurnsBefore, page.TurnsTotal, err = turnCountsTx(ctx, tx, agentID, from)
	return page, err
}

// aroundRunAnchor returns the boundary sequence number for the "around a run" query: messages at or after the
// boundary go in the second half of the page, earlier ones in the first half.
// A normal run uses its user message as the boundary. A manual compaction (console_compact) has no conversation
// messages, so it uses its position at the time it started as the boundary: just after the last message before it.
// Returns not_found when the run does not belong to this Agent.
func aroundRunAnchor(ctx context.Context, q queueQuerier, agentID, runID uuid.UUID) (int64, error) {
	var origin string
	var queuedAt time.Time
	var userSeq *int64
	err := q.QueryRow(ctx, `SELECT r.origin, r.queued_at, m.seq FROM runs r LEFT JOIN conversation_messages m ON m.id = r.user_message_id
		WHERE r.id = $1 AND r.agent_id = $2`, runID, agentID).Scan(&origin, &queuedAt, &userSeq)
	if err != nil {
		return 0, notFoundIfNoRows(err, "Run")
	}
	if userSeq != nil {
		return *userSeq, nil
	}
	if origin != "console_compact" {
		return 0, apperr.NotFound("Run")
	}
	var last int64
	err = q.QueryRow(ctx, `SELECT coalesce(max(seq), 0) FROM conversation_messages WHERE agent_id = $1 AND created_at <= $2`, agentID, queuedAt).Scan(&last)
	return last + 1, err
}

// voidedRunCond matches runs "voided while queued, never actually run" (withdrawn, all messages turned into
// interrupts, messages already read or cancelled), consistent with isVoidedRun in the console conversation stream:
// these runs do not count as turns.
const voidedRunCond = `r.end_reason IN ('user_cancelled', 'merged_into_interrupt', 'no_pending_messages')`

// turnCountsTx returns the turn counts of the conversation, counted the same way as the console conversation stream
// (buildTurns in web/src/features/chat/turns.ts): one run is one turn, queued and voided runs do not count, and a
// session restart does not reset the count; turns are ordered by the time they actually started running, or by queue
// time when they have no start time.
//
// total is the number of all turns. before is the number of turns ordered before "the turns that messages with
// sequence number from or later belong to": turns that have ended and whose messages (user message and replies) all
// have sequence numbers below from. The turn in progress has no reply, and is not counted however early its user
// message is: it is always the last turn, and the console appends it at the end from its live status.
//
// Only one run per Agent is in progress at a time, and a sleep wake is only queued while idle. So when one turn is
// ordered after another, it always has a message later than all messages of the previous turn (replies are written
// after it starts, or it was queued after the previous turn ended); the turns that messages from from on belong to
// (plus the turn in progress) are exactly a suffix of all turns in the order above, and before is the index of the
// first turn in that suffix.
//
// The count joins runs through this Agent's user messages (exactly one per run), so the cost is proportional to the
// number of turns in the conversation.
func turnCountsTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID, from int64) (before, total int64, err error) {
	err = tx.QueryRow(ctx, `SELECT count(*),
			coalesce(sum(CASE WHEN m.seq < $2 AND r.status IN ('completed', 'failed', 'interrupted')
				AND NOT EXISTS (SELECT 1 FROM conversation_messages a WHERE a.run_id = r.id AND a.role = 'assistant' AND a.seq >= $2)
				THEN 1 ELSE 0 END), 0)
		FROM conversation_messages m JOIN runs r ON r.id = m.run_id
		WHERE m.agent_id = $1 AND m.role = 'user' AND r.status <> 'queued' AND NOT coalesce(`+voidedRunCond+`, false)`,
		agentID, from).Scan(&total, &before)
	return before, total, err
}

func historyPage(ctx context.Context, q queueQuerier, agentID uuid.UUID, cond string, seq int64, limit int) ([]MessageWithRun, error) {
	rows, err := q.Query(ctx, historySelect+` WHERE m.agent_id = $1 AND `+cond+` LIMIT $3`, agentID, seq, limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(row db.CollectableRow) (MessageWithRun, error) {
		var it MessageWithRun
		m := &it.Message
		var raw []byte
		dest := append([]any{&m.ID, &m.AgentID, &m.Seq, &m.Role, &m.Text, &m.RunID, &m.SessionEpoch, &m.CreatedAt, &raw}, runFields(&it.Run)...)
		if e := row.Scan(dest...); e != nil {
			return it, e
		}
		e := json.Unmarshal(raw, &m.Images)
		return it, e
	})
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func nullIfEmptyBytes(b []byte) *string {
	if len(b) == 0 {
		return nil
	}
	v := string(b)
	return &v
}
