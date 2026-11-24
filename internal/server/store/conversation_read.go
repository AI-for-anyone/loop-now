package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// ConversationQuery pages the Agent's current conversation: at most one of Before and After; neither means the newest
// page.
type ConversationQuery struct {
	Before *uuid.UUID
	After  *uuid.UUID
	Limit  int
}

// ConversationEntry is one saved message of the current conversation, with what the platform knows of its turn.
type ConversationEntry struct {
	ID        uuid.UUID
	Seq       int64
	Role      string
	Origin    string
	Text      string
	CreatedAt time.Time
	RunID     uuid.UUID
	RunStatus string
	Images    int
	// Tools summarizes the turn's tool calls on its reply (empty on the request), in order of first use.
	Tools []ToolUse
}

// ToolUse is how often a turn called one tool and how many of those calls failed.
type ToolUse struct {
	Name   string
	Calls  int
	Failed int
}

// ConversationPage is one page of the current conversation, oldest first.
type ConversationPage struct {
	Entries      []ConversationEntry
	HasMore      bool // older messages exist
	HasMoreAfter bool // newer messages exist
	Total        int  // messages in the whole current conversation
}

// conversationCond selects the messages of the Agent's current conversation (m: conversation_messages, r: runs; $1 is
// the Agent, $2 the session epoch): everything saved since the user last started over, except messages still queued
// (not yet given to the Agent) and messages voided while queued.
const conversationCond = `m.agent_id = $1 AND m.session_epoch = $2 AND r.status <> 'queued' AND NOT coalesce(` + voidedRunCond + `, false)`

// ReadConversation returns one page of the Agent's current conversation, the conversation a change of runtime carries
// over. A position outside the current conversation returns not_found.
func (s *Store) ReadConversation(ctx context.Context, agentID uuid.UUID, q ConversationQuery) (ConversationPage, error) {
	var page ConversationPage
	err := s.Pool.ReadTx(ctx, func(tx *db.Tx) error {
		var epoch int
		if err := tx.QueryRow(ctx, `SELECT s.epoch FROM agent_sessions s JOIN agents a ON a.id = s.agent_id WHERE s.agent_id = $1 AND a.deleted_at IS NULL`, agentID).Scan(&epoch); err != nil {
			return notFoundIfNoRows(err, "Agent")
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM conversation_messages m JOIN runs r ON r.id = m.run_id WHERE `+conversationCond, agentID, epoch).Scan(&page.Total); err != nil {
			return err
		}
		seqOf := func(id uuid.UUID) (int64, error) {
			var seq int64
			err := tx.QueryRow(ctx, `SELECT m.seq FROM conversation_messages m JOIN runs r ON r.id = m.run_id WHERE `+conversationCond+` AND m.id = $3`, agentID, epoch, id).Scan(&seq)
			return seq, notFoundIfNoRows(err, "Message")
		}
		var err error
		switch {
		case q.After != nil:
			var seq int64
			if seq, err = seqOf(*q.After); err != nil {
				return err
			}
			if page.Entries, err = conversationRows(ctx, tx, agentID, epoch, `m.seq > $3 ORDER BY m.seq ASC`, seq, q.Limit+1); err != nil {
				return err
			}
			if len(page.Entries) > q.Limit {
				page.Entries, page.HasMoreAfter = page.Entries[:q.Limit], true
			}
			page.HasMore = true
		default:
			seq := int64(1<<62 - 1)
			if q.Before != nil {
				if seq, err = seqOf(*q.Before); err != nil {
					return err
				}
				page.HasMoreAfter = true
			}
			if page.Entries, err = conversationRows(ctx, tx, agentID, epoch, `m.seq < $3 ORDER BY m.seq DESC`, seq, q.Limit+1); err != nil {
				return err
			}
			if len(page.Entries) > q.Limit {
				page.Entries, page.HasMore = page.Entries[:q.Limit], true
			}
			reverse(page.Entries)
		}
		return fillToolUse(ctx, tx, page.Entries)
	})
	if page.Entries == nil {
		page.Entries = []ConversationEntry{}
	}
	return page, err
}

func conversationRows(ctx context.Context, tx *db.Tx, agentID uuid.UUID, epoch int, cond string, arg any, limit int) ([]ConversationEntry, error) {
	rows, err := tx.Query(ctx, `SELECT m.id, m.seq, m.role, r.origin, m.text, m.created_at, m.run_id, r.status, m.images
		FROM conversation_messages m JOIN runs r ON r.id = m.run_id WHERE `+conversationCond+` AND `+cond+` LIMIT $4`, agentID, epoch, arg, limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(row db.CollectableRow) (ConversationEntry, error) {
		var e ConversationEntry
		var images []byte
		if err := row.Scan(&e.ID, &e.Seq, &e.Role, &e.Origin, &e.Text, &e.CreatedAt, &e.RunID, &e.RunStatus, &images); err != nil {
			return e, err
		}
		var refs []json.RawMessage
		if err := json.Unmarshal(images, &refs); err != nil {
			return e, err
		}
		e.Images = len(refs)
		e.Tools = []ToolUse{}
		return e, nil
	})
}

// fillToolUse adds each reply's tool summary: one entry per tool name in order of first use, counting calls and the
// calls whose last state is failed.
func fillToolUse(ctx context.Context, tx *db.Tx, entries []ConversationEntry) error {
	replies := map[uuid.UUID]int{}
	ids := []uuid.UUID{}
	for i, e := range entries {
		if e.Role == "assistant" {
			replies[e.RunID] = i
			ids = append(ids, e.RunID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT run_id, tool_call_id, tool_name, state FROM run_tool_events WHERE run_id IN (SELECT value FROM json_each($1)) ORDER BY run_id, seq`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	type call struct {
		name   string
		failed bool
	}
	calls := map[uuid.UUID]map[string]*call{}
	order := map[uuid.UUID][]string{}
	for rows.Next() {
		var run uuid.UUID
		var id, name, state string
		if err := rows.Scan(&run, &id, &name, &state); err != nil {
			return err
		}
		if calls[run] == nil {
			calls[run] = map[string]*call{}
		}
		c := calls[run][id]
		if c == nil {
			c = &call{name: name}
			calls[run][id] = c
			order[run] = append(order[run], id)
		}
		c.failed = state == "failed"
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for run, i := range replies {
		index := map[string]int{}
		for _, id := range order[run] {
			c := calls[run][id]
			at, ok := index[c.name]
			if !ok {
				at = len(entries[i].Tools)
				index[c.name] = at
				entries[i].Tools = append(entries[i].Tools, ToolUse{Name: c.name})
			}
			entries[i].Tools[at].Calls++
			if c.failed {
				entries[i].Tools[at].Failed++
			}
		}
	}
	return nil
}

// HandoffConversation returns the Agent's whole current conversation before the given run (that run's own message is
// the new request, sent apart), oldest first: what a change of runtime carries over.
func (s *Store) HandoffConversation(ctx context.Context, agentID, run uuid.UUID) ([]ConversationEntry, error) {
	var out []ConversationEntry
	err := s.Pool.ReadTx(ctx, func(tx *db.Tx) error {
		var epoch int
		if err := tx.QueryRow(ctx, `SELECT epoch FROM agent_sessions WHERE agent_id = $1`, agentID).Scan(&epoch); err != nil {
			return notFoundIfNoRows(err, "Agent session")
		}
		var err error
		if out, err = conversationRows(ctx, tx, agentID, epoch, `m.run_id <> $3 ORDER BY m.seq ASC`, run, -1); err != nil {
			return err
		}
		return fillToolUse(ctx, tx, out)
	})
	return out, err
}
