package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// Compaction holds the instructions and result of a manual compaction, stored in runs.compaction (JSON). Outcome is
// empty before the run ends, and when it failed or was interrupted.
type Compaction struct {
	Instructions string `json:"instructions"`
	// Outcome: compacted means it was compacted; skipped means it was not compacted (SkipReason: too_small means the
	// session is too short, already_compacted means it was already compacted, cancelled_by_extension means an extension
	// cancelled this compaction), and the run still counts as completed normally.
	Outcome              string `json:"outcome,omitempty"`
	SkipReason           string `json:"skipReason,omitempty"`
	TokensBefore         *int64 `json:"tokensBefore,omitempty"`
	EstimatedTokensAfter *int64 `json:"estimatedTokensAfter,omitempty"`
}

// Results of a manual compaction.
const (
	CompactionCompacted = "compacted"
	CompactionSkipped   = "skipped"
)

// ParseCompaction parses runs.compaction; returns the zero value when it is empty or cannot be parsed.
func ParseCompaction(raw []byte) Compaction {
	var c Compaction
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &c)
	}
	return c
}

// InsertConsoleCompactRun re-confirms, under the Agent row lock, that the Agent is idle and has an active session,
// inserts a compaction run without conversation messages, and records the compaction instructions (may be empty).
// Write transactions are serialized and the idle check and the insert are in the same transaction, so "at most one
// run in progress per Agent" cannot be violated.
func (s *Store) InsertConsoleCompactRun(ctx context.Context, agentID, runID, instanceID uuid.UUID, instructions string) (Run, error) {
	raw, err := json.Marshal(Compaction{Instructions: instructions})
	if err != nil {
		return Run{}, err
	}
	var run Run
	err = s.inTx(ctx, func(tx *db.Tx) error {
		agent, err := lockAgent(ctx, tx, agentID)
		if err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		var state string
		var file *string
		if err := tx.QueryRow(ctx, `SELECT state, session_file FROM agent_sessions WHERE agent_id=$1`, agentID).Scan(&state, &file); err != nil {
			return err
		}
		if state == "missing" {
			return apperr.Conflict(apperr.CodeSessionMissing, "The session file is missing. Confirm reopening the session in the console first.")
		}
		if state != "active" || file == nil {
			return apperr.Conflict(apperr.CodeSessionNone, "There is no session yet, so there is nothing to compact.")
		}
		now := tx.Now()
		queueSeq, err := tx.NextSeq(ctx, "runs.queue_seq")
		if err != nil {
			return err
		}
		run, err = scanRun(tx.QueryRow(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, instance_id, priority, status, started_at, origin, queue_seq, queued_at, compaction)
			VALUES ($1,$2,NULL,$3,$4,1,'starting',$5,'console_compact',$6,$5,$7) RETURNING `+runColumns,
			runID, agentID, agent.ConfigVersion, instanceID, now, queueSeq, string(raw)))
		if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: &run.ID, Type: EventRunDispatched, At: now})
	})
	return run, err
}

// IdleSession confirms in the database that the Agent has no work in progress or queued, and returns the current
// session.
func (s *Store) IdleSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		var file *string
		if err := tx.QueryRow(ctx, `SELECT session_file, state, epoch, updated_at FROM agent_sessions WHERE agent_id=$1`, agentID).
			Scan(&file, &sess.State, &sess.Epoch, &sess.UpdatedAt); err != nil {
			return err
		}
		if file != nil {
			sess.SessionFile = *file
		}
		return nil
	})
	return sess, err
}

// LatestCompletedAssistant returns the latest assistant reply with text that belongs to a successful run; returns nil
// when there is none.
// Replies without text (/pi: commands Pi handled directly, a turn that only called tools) are skipped; copying them
// would be pointless.
func (s *Store) LatestCompletedAssistant(ctx context.Context, agentID uuid.UUID) (*Message, error) {
	if _, err := s.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	m, err := scanMessage(s.Pool.QueryRow(ctx, `SELECT `+messageColumns+` FROM conversation_messages
		WHERE agent_id=$1 AND role='assistant' AND trim(text, ' '||char(9, 10, 13)) <> ''
			AND run_id IN (SELECT id FROM runs WHERE agent_id=$1 AND status='completed')
		ORDER BY seq DESC LIMIT 1`, agentID))
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// LatestCompactRun returns the Agent's latest manual compaction run (in any status); nil when there is none.
func (s *Store) LatestCompactRun(ctx context.Context, agentID uuid.UUID) (*Run, error) {
	r, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE agent_id = $1 AND origin = 'console_compact'
		ORDER BY queued_at DESC, id DESC LIMIT 1`, agentID))
	if errors.Is(err, db.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, s.fillRuns(ctx, &r)
}
