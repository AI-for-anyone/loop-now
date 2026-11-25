package store

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/naming"
)

// ImportedTurn is one turn of an imported conversation as the Agent's chat shows it: the prompt and the reply's text.
type ImportedTurn struct {
	Prompt   string
	PromptAt time.Time
	Reply    string // empty: the turn shows no reply text
	ReplyAt  time.Time
}

// NewImportedAgent is an Agent continuing a conversation of a CLI on this computer.
type NewImportedAgent struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// Name is the wanted name; " (2)", " (3)" ... is added when the project already has an Agent of that name.
	Name             string
	Provider         string
	ModelID          string
	WorkingDirectory string
	PermissionMode   string
	From             ImportedFrom
	Turns            []ImportedTurn
}

// AlreadyImportedError: the conversation already has a live Agent.
type AlreadyImportedError struct{ AgentID uuid.UUID }

func (e *AlreadyImportedError) Error() string {
	return fmt.Sprintf("the conversation was already imported as Agent %s", e.AgentID)
}

// CreateImportedAgent creates, in one transaction, an Agent continuing an imported conversation (working in its folder
// with the person's own CLI configuration) and its chat history: one completed run of origin imported per turn, with the
// prompt and the reply as its messages at their original times. Returns *AlreadyImportedError when the conversation
// already has a live Agent.
func (s *Store) CreateImportedAgent(ctx context.Context, in NewImportedAgent) (Agent, error) {
	var a Agent
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveProject(ctx, tx, in.ProjectID); err != nil {
			return err
		}
		if id, err := importedAgentTx(ctx, tx, in.From.CLI, in.From.SessionID); err != nil || id != nil {
			if err == nil {
				err = &AlreadyImportedError{AgentID: *id}
			}
			return err
		}
		name, err := freeAgentName(ctx, tx, in.ProjectID, in.Name)
		if err != nil {
			return err
		}
		mode := in.PermissionMode
		if mode == "" {
			mode = PermissionModeFull
		}
		a, err = scanAgent(tx.QueryRow(ctx, `INSERT INTO agents (id, project_id, name, model_provider, model_id, prompt, working_directory, use_local_config,
				permission_mode, imported_cli, imported_session_id, imported_path, imported_capture, imported_at)
			VALUES ($1, $2, $3, $4, $5, '', $6, 1, $7, $8, $9, $10, $11, $12) RETURNING `+agentColumns,
			in.ID, in.ProjectID, name, in.Provider, in.ModelID, in.WorkingDirectory, mode, in.From.CLI, in.From.SessionID, in.From.Path, in.From.Capture, in.From.ImportedAt))
		if err != nil {
			if isUniqueViolation(err, "agents_imported_once") {
				if id, e := importedAgentTx(ctx, tx, in.From.CLI, in.From.SessionID); e == nil && id != nil {
					return &AlreadyImportedError{AgentID: *id}
				}
			}
			return agentNameConflict(err, name)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO agent_sessions (agent_id) VALUES ($1)`, a.ID); err != nil {
			return err
		}
		for _, turn := range in.Turns {
			if err := insertImportedTurn(ctx, tx, a, turn, in.From.ImportedAt); err != nil {
				return err
			}
		}
		return nil
	})
	return a, err
}

// insertImportedTurn writes one imported turn as a completed run with its prompt and reply.
func insertImportedTurn(ctx context.Context, tx *db.Tx, a Agent, turn ImportedTurn, fallback time.Time) error {
	promptAt := turn.PromptAt
	if promptAt.IsZero() {
		promptAt = fallback
	}
	replyAt := turn.ReplyAt
	if replyAt.IsZero() || replyAt.Before(promptAt) {
		replyAt = promptAt
	}
	queueSeq, err := tx.NextSeq(ctx, "runs.queue_seq")
	if err != nil {
		return err
	}
	runID, msgID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, queued_at, started_at, ended_at, origin, queue_seq)
		VALUES ($1, $2, $3, $4, 1, 'completed', $5, $5, $6, 'imported', $7)`, runID, a.ID, msgID, a.ConfigVersion, promptAt, replyAt, queueSeq); err != nil {
		return err
	}
	seq, err := tx.NextSeq(ctx, "conversation_messages.seq")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch, seq, created_at)
		VALUES ($1, $2, 'user', $3, $4, 1, $5, $6)`, msgID, a.ID, turn.Prompt, runID, seq, promptAt); err != nil {
		return err
	}
	if turn.Reply == "" {
		return nil
	}
	if seq, err = tx.NextSeq(ctx, "conversation_messages.seq"); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO conversation_messages (id, agent_id, role, text, run_id, session_epoch, seq, created_at)
		VALUES ($1, $2, 'assistant', $3, $4, 1, $5, $6)`, uuid.Must(uuid.NewV7()), a.ID, turn.Reply, runID, seq, replyAt)
	return err
}

// freeAgentName is name, or name with " (n)" added, that no live Agent of the project has.
func freeAgentName(ctx context.Context, tx *db.Tx, projectID uuid.UUID, name string) (string, error) {
	for n := 1; ; n++ {
		candidate := name
		if n > 1 {
			suffix := fmt.Sprintf(" (%d)", n)
			candidate = clipRunes(name, naming.MaxLen-utf8.RuneCountInString(suffix)) + suffix
		}
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE project_id = $1 AND name = $2 AND deleted_at IS NULL)`, projectID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func importedAgentTx(ctx context.Context, q queueQuerier, cli, sessionID string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM agents WHERE imported_cli = $1 AND imported_session_id = $2 AND deleted_at IS NULL`, cli, sessionID).Scan(&id)
	if IsNotFound(notFoundIfNoRows(err, "Agent")) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// ImportedAgents returns the live Agents imported from conversations, by CLI and then session id.
func (s *Store) ImportedAgents(ctx context.Context) (map[string]map[string]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT imported_cli, imported_session_id, id FROM agents WHERE imported_session_id IS NOT NULL AND deleted_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]uuid.UUID{}
	for rows.Next() {
		var cli, session string
		var id uuid.UUID
		if err := rows.Scan(&cli, &session, &id); err != nil {
			return nil, err
		}
		if out[cli] == nil {
			out[cli] = map[string]uuid.UUID{}
		}
		out[cli][session] = id
	}
	return out, rows.Err()
}

// ImportedAgent returns the live Agent imported from a conversation; nil when there is none.
func (s *Store) ImportedAgent(ctx context.Context, cli, sessionID string) (*uuid.UUID, error) {
	return importedAgentTx(ctx, s.Pool, cli, sessionID)
}
