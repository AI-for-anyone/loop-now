package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/db"
)

// PendingHandoff is a change of runtime whose conversation has not reached the new runtime yet
// (agent_sessions.handoff).
type PendingHandoff struct {
	ID uuid.UUID `json:"id"`
	// From is the runtime that last ran the conversation.
	From string `json:"from"`
	// SessionFile is From's session, continued as is when the change is undone before the new runtime ran anything;
	// empty when From had no saved session.
	SessionFile string `json:"sessionFile,omitempty"`
	// Launching is a message dispatched after the change that has not reached its runtime or ended yet.
	Launching *uuid.UUID `json:"launching,omitempty"`
	// Reason is why the conversation is to be carried: empty (a change of runtime) or HandoffSessionLost.
	Reason string `json:"reason,omitempty"`
	// Ran means a message reached a runtime after the change (or, without passing through dispatch, reached a runtime
	// other than From): the conversation has moved on, so undoing the change can no longer continue From's session.
	Ran bool `json:"ran"`
}

// RunSelection is who ran a turn: the runtime, model and thinking level the message was handed over with (runs.selection).
type RunSelection struct {
	Runtime       string  `json:"runtime"`
	Provider      string  `json:"provider"`
	ModelID       string  `json:"modelId"`
	ThinkingLevel *string `json:"thinkingLevel"`
}

// HandoffRecord is the selection of the saved conversation a turn carried to a new runtime (runs.context_handoff).
type HandoffRecord struct {
	// ID is the pending handoff the turn carried.
	ID uuid.UUID `json:"id"`
	// Reason is HandoffRuntimeChanged, HandoffSessionLost or HandoffTaskStart; empty in records from before it was
	// kept, which were all changes of runtime.
	Reason           string `json:"reason,omitempty"`
	FromRuntime      string `json:"fromRuntime"`
	ToRuntime        string `json:"toRuntime"`
	IncludedMessages int    `json:"includedMessages"`
	OmittedMessages  int    `json:"omittedMessages"`
	Images           int    `json:"images"`
	Bytes            int    `json:"bytes"`
	BudgetBytes      int    `json:"budgetBytes"`
}

// Why a turn carried the saved conversation (HandoffRecord.Reason).
const (
	HandoffRuntimeChanged = "runtime_changed"
	HandoffSessionLost    = "session_lost"
	HandoffTaskStart      = "task_start"
)

// ReasonOrDefault is the record's reason; records from before it was kept were all changes of runtime.
func (h HandoffRecord) ReasonOrDefault() string {
	if h.Reason == "" {
		return HandoffRuntimeChanged
	}
	return h.Reason
}

// RunStart is what is recorded when the reply of a turn begins.
type RunStart struct {
	Selection *RunSelection
	// Handoff is set when the message carried a handoff block.
	Handoff *HandoffRecord
}

// SelectionInfo returns who ran the turn; nil when it was not recorded.
func (r Run) SelectionInfo() *RunSelection {
	var out RunSelection
	if len(r.Selection) == 0 || json.Unmarshal(r.Selection, &out) != nil {
		return nil
	}
	return &out
}

// HandoffInfo returns the handoff the turn carried; nil when it carried none.
func (r Run) HandoffInfo() *HandoffRecord {
	var out HandoffRecord
	if len(r.ContextHandoff) == 0 || json.Unmarshal(r.ContextHandoff, &out) != nil {
		return nil
	}
	return &out
}

// quiet reports that nothing has run since the change and no message is on its way to a runtime: the conversation is
// still exactly where From left it.
func (h *PendingHandoff) quiet() bool { return !h.Ran && h.Launching == nil }

func parseHandoff(raw []byte) (*PendingHandoff, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var h PendingHandoff
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// jsonArg returns a JSON column value: NULL for a nil pointer.
func jsonArg[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// switchRuntimeTx is called when a model provider change switches the runtime (between Pi and Claude Code). The
// conversation is not started over: the session number stays, sleeps stay, and a run in progress goes on. The new
// runtime starts its own session (the pointer is cleared) and is given a selection of the saved conversation with its
// first message, which agent_sessions.handoff keeps pending until then. When the change is undone before the new
// runtime ran anything, the earlier runtime's session continues as if nothing happened. The old session is neither
// migrated nor deleted.
func (s *Store) switchRuntimeTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID, from, to string) error {
	fromRT, toRT := agentruntime.ForProvider(from).ID, agentruntime.ForProvider(to).ID
	var file *string
	var state string
	var epoch int
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT session_file, state, epoch, handoff FROM agent_sessions WHERE agent_id = $1`, agentID).Scan(&file, &state, &epoch, &raw)
	if errors.Is(err, db.ErrNoRows) {
		return nil // no session row (should not happen; it exists from Agent creation): nothing to carry over
	}
	if err != nil {
		return err
	}
	pending, err := parseHandoff(raw)
	if err != nil {
		return err
	}
	outcome := "none"
	var nextFile *string
	nextState := "none"
	var next *PendingHandoff
	switch {
	case pending != nil && pending.quiet() && pending.From == toRT && pending.SessionFile != "":
		// Undone before the runtime being left ran anything: continue the earlier session.
		nextFile, nextState, outcome = &pending.SessionFile, "active", "restored"
	case pending != nil && pending.quiet():
		// The runtime being left never ran anything: the conversation is still where the pending handoff says.
		next, outcome = pending, "pending"
	default:
		var talked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM conversation_messages m JOIN runs r ON r.id = m.run_id WHERE `+conversationCond+`)`, agentID, epoch).Scan(&talked); err != nil {
			return err
		}
		if talked || state == "active" {
			next, outcome = &PendingHandoff{ID: uuid.Must(uuid.NewV7()), From: fromRT}, "pending"
			if state == "active" && file != nil {
				next.SessionFile = *file
			}
		}
	}
	arg, err := jsonArg(next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_sessions SET session_file = $2, state = $3, handoff = $4, updated_at = $5 WHERE agent_id = $1`,
		agentID, nextFile, nextState, arg, tx.Now()); err != nil {
		return err
	}
	return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventRuntimeChanged,
		Data: map[string]any{"fromRuntime": fromRT, "toRuntime": toRT, "handoff": outcome, "epoch": epoch}})
}

// RecordRuntimeSessionFile records a session file confirmed written to disk by the given runtime. When that runtime is
// the Agent's current one, it becomes the session (state active). If a handoff from that same runtime is pending and
// nothing has run since the change, the save comes from a reply that was in progress during the change: that session
// holds the whole conversation, and the handoff ends. A runtime the Agent has since changed away from (such a reply)
// does not touch the session: its file goes into the pending handoff, so undoing the change continues it.
func (s *Store) RecordRuntimeSessionFile(ctx context.Context, agentID uuid.UUID, runtime, path string) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		var provider string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT a.model_provider, s.handoff FROM agents a JOIN agent_sessions s ON s.agent_id = a.id WHERE a.id = $1`, agentID).Scan(&provider, &raw)
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		pending, err := parseHandoff(raw)
		if err != nil {
			return err
		}
		if agentruntime.ForProvider(provider).ID == runtime {
			end := pending != nil && pending.From == runtime && pending.quiet()
			_, err = tx.Exec(ctx, `UPDATE agent_sessions SET session_file = $2, state = 'active', handoff = CASE WHEN $3 THEN NULL ELSE handoff END, updated_at = $4
				WHERE agent_id = $1`, agentID, path, end, tx.Now())
			return err
		}
		if pending == nil || pending.From != runtime {
			return nil
		}
		pending.SessionFile = path
		arg, err := jsonArg(pending)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE agent_sessions SET handoff = $2, updated_at = $3 WHERE agent_id = $1`, agentID, arg, tx.Now())
		return err
	})
}

// DispatchHandoff is called when a message (the run run) is dispatched to the Agent: it returns the pending handoff
// (nil when there is none) and records the message as on its way to the runtime, until it reaches it or ends.
func (s *Store) DispatchHandoff(ctx context.Context, agentID, run uuid.UUID) (*PendingHandoff, error) {
	// Most messages have no pending handoff: find that out without taking a write transaction.
	var pending bool
	if err := s.Pool.QueryRow(ctx, `SELECT handoff IS NOT NULL FROM agent_sessions WHERE agent_id = $1`, agentID).Scan(&pending); err != nil || !pending {
		return nil, notFoundIfNoRows(err, "Agent session")
	}
	var out *PendingHandoff
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT handoff FROM agent_sessions WHERE agent_id = $1`, agentID).Scan(&raw); err != nil {
			return notFoundIfNoRows(err, "Agent session")
		}
		pending, err := parseHandoff(raw)
		if err != nil || pending == nil {
			return err
		}
		pending.Launching = &run
		arg, err := jsonArg(pending)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_sessions SET handoff = $2 WHERE agent_id = $1`, agentID, arg); err != nil {
			return err
		}
		out = pending
		return nil
	})
	return out, err
}

// SetRunSelection records who a run is dispatched to (the runtime, provider, model and thinking level of the
// configuration it is dispatched with), so a run that fails before it starts still names them. It does not replace
// what was recorded when the reply began.
func (s *Store) SetRunSelection(ctx context.Context, runID uuid.UUID, sel RunSelection) error {
	arg, err := jsonArg(&sel)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE runs SET selection = $2 WHERE id = $1 AND running_at IS NULL`, runID, arg)
	return err
}

// SetRunRunningWith is SetRunRunning that also records who ran the turn and the handoff it carried (with its event). A
// pending handoff notes that a message ran: one dispatched after the change, or any that reached a runtime other than
// the one the handoff comes from.
func (s *Store) SetRunRunningWith(ctx context.Context, runID uuid.UUID, configVersion int64, start RunStart) error {
	sel, err := jsonArg(start.Selection)
	if err != nil {
		return err
	}
	carried, err := jsonArg(start.Handoff)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *db.Tx) error {
		var agentID uuid.UUID
		var runningAt time.Time
		err := tx.QueryRow(ctx, `UPDATE runs SET status = CASE WHEN status = 'aborting' THEN status ELSE 'running' END,
				phase = NULL, config_version = $2, running_at = coalesce(running_at, $3), selection = coalesce($4, selection), context_handoff = $5
			WHERE id = $1 AND status IN ('starting', 'aborting') AND running_at IS NULL RETURNING agent_id, running_at`, runID, configVersion, tx.Now(), sel, carried).Scan(&agentID, &runningAt)
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &runID, Type: EventRunRunning, At: runningAt,
			Data: map[string]any{"configVersion": configVersion}}); err != nil {
			return err
		}
		if h := start.Handoff; h != nil {
			if err := s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &runID, Type: EventContextHandoff, At: runningAt,
				Data: map[string]any{"fromRuntime": h.FromRuntime, "toRuntime": h.ToRuntime, "includedMessages": h.IncludedMessages,
					"omittedMessages": h.OmittedMessages, "images": h.Images, "bytes": h.Bytes, "budgetBytes": h.BudgetBytes}}); err != nil {
				return err
			}
		}
		if start.Selection == nil {
			return nil
		}
		return updateHandoffTx(ctx, tx, agentID, func(h *PendingHandoff) bool {
			launched := h.Launching != nil && *h.Launching == runID
			if !launched && start.Selection.Runtime == h.From {
				return false // a reply dispatched before the change, on the runtime the conversation was on
			}
			h.Launching, h.Ran = nil, true
			return true
		})
	})
}

// updateHandoffTx applies change to the Agent's pending handoff and saves it when change reports a change; nothing
// happens when no handoff is pending.
func updateHandoffTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID, change func(*PendingHandoff) bool) error {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT handoff FROM agent_sessions WHERE agent_id = $1`, agentID).Scan(&raw); err != nil {
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		return err
	}
	pending, err := parseHandoff(raw)
	if err != nil || pending == nil || !change(pending) {
		return err
	}
	arg, err := jsonArg(pending)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE agent_sessions SET handoff = $2 WHERE agent_id = $1`, agentID, arg)
	return err
}

// finishHandoffTx updates the pending handoff when a turn ends. A message that ended before reaching its runtime is no
// longer on its way. The handoff the turn carried ends once the runtime has it: when the runtime's session is saved,
// or when the turn completed (the runtime has it in its context even if its session record could not be found, as with
// Claude Code on some systems); a turn that failed with no session saved leaves it for the next message.
func finishHandoffTx(ctx context.Context, tx *db.Tx, run Run) error {
	if err := updateHandoffTx(ctx, tx, run.AgentID, func(h *PendingHandoff) bool {
		if h.Launching == nil || *h.Launching != run.ID {
			return false
		}
		h.Launching = nil
		return true
	}); err != nil {
		return err
	}
	h := run.HandoffInfo()
	if h == nil {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE agent_sessions SET handoff = NULL WHERE agent_id = $1 AND handoff->>'id' = $2 AND (state = 'active' OR $3)`,
		run.AgentID, h.ID.String(), run.Status == "completed")
	return err
}

// LoseSession turns a session its runtime could not continue (marked missing) into a new start that carries the saved
// conversation: the pointer is cleared and a handoff with reason session_lost from the current runtime is pending, so
// the next message (the one that found it missing, sent again) gives the new session the conversation. A handoff that
// is already pending stays as it is. It reports whether there was a missing session to turn.
func (s *Store) LoseSession(ctx context.Context, agentID uuid.UUID) (bool, error) {
	lost := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var provider string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT a.model_provider, s.handoff FROM agents a JOIN agent_sessions s ON s.agent_id = a.id
			WHERE a.id = $1 AND s.state = 'missing'`, agentID).Scan(&provider, &raw)
		if errors.Is(err, db.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		pending, err := parseHandoff(raw)
		if err != nil {
			return err
		}
		if pending == nil {
			pending = &PendingHandoff{ID: uuid.Must(uuid.NewV7()), From: agentruntime.ForProvider(provider).ID, Reason: HandoffSessionLost}
		}
		arg, err := jsonArg(pending)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_sessions SET session_file = NULL, state = 'none', handoff = $2, updated_at = $3 WHERE agent_id = $1`,
			agentID, arg, tx.Now()); err != nil {
			return err
		}
		lost = true
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionLost, Data: map[string]any{"runtime": pending.From}})
	})
	return lost, err
}

// EndHandoff ends the given pending handoff without carrying anything (there was nothing saved to carry).
func (s *Store) EndHandoff(ctx context.Context, agentID, id uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agent_sessions SET handoff = NULL WHERE agent_id = $1 AND handoff->>'id' = $2`, agentID, id.String())
	return err
}
