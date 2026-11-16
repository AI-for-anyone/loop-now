package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// FolderGitPull is one "从 Git 拉取" ("Pull from Git") operation into an Agent folder.
type FolderGitPull struct {
	ID           uuid.UUID
	AgentID      uuid.UUID
	ActionID     uuid.UUID
	URL          string
	Branch       string
	Path         string
	Status       string // queued / running / succeeded / failed
	ErrorCode    *string
	ErrorMessage *string
	CreatedAt    time.Time
	FinishedAt   *time.Time
}

const gitPullColumns = `id, agent_id, action_id, url, branch, path, status, error_code, error_message, created_at, finished_at`

func scanGitPull(row db.Scanner) (FolderGitPull, error) {
	var p FolderGitPull
	err := row.Scan(&p.ID, &p.AgentID, &p.ActionID, &p.URL, &p.Branch, &p.Path, &p.Status, &p.ErrorCode, &p.ErrorMessage, &p.CreatedAt, &p.FinishedAt)
	return p, err
}

// CreateFolderGitPull registers a queued pull. The same actionId for the same Agent returns the original operation
// (created=false), or action_conflict when the content differs; returns path_conflict when the same target already has
// a pull in progress.
func (s *Store) CreateFolderGitPull(ctx context.Context, p FolderGitPull) (FolderGitPull, bool, error) {
	var out FolderGitPull
	created := false
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := lockAgent(ctx, tx, p.AgentID); err != nil {
			return err
		}
		existing, err := scanGitPull(tx.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 AND action_id = $2`, p.AgentID, p.ActionID))
		if err == nil {
			if existing.URL != p.URL || existing.Branch != p.Branch || existing.Path != p.Path {
				return apperr.Conflict(apperr.CodeActionConflict, "This actionId was already used for a different request.")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, db.ErrNoRows) {
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_folder_git_pulls WHERE agent_id = $1 AND path = $2 AND status IN ('queued', 'running'))`, p.AgentID, p.Path).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return apperr.Conflict(apperr.CodePathConflict, "A pull is already in progress for this subdirectory.")
		}
		out, err = scanGitPull(tx.QueryRow(ctx, `INSERT INTO agent_folder_git_pulls (id, agent_id, action_id, url, branch, path, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'queued') RETURNING `+gitPullColumns, p.ID, p.AgentID, p.ActionID, p.URL, p.Branch, p.Path))
		created = err == nil
		return err
	})
	return out, created, err
}

// FindFolderGitPull looks up, by actionId, a pull the Agent has already accepted.
func (s *Store) FindFolderGitPull(ctx context.Context, agentID, actionID uuid.UUID) (FolderGitPull, bool, error) {
	p, err := scanGitPull(s.Pool.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 AND action_id = $2`, agentID, actionID))
	if errors.Is(err, db.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
}

// GetFolderGitPull returns one of the Agent's pulls; returns not_found when it does not exist.
func (s *Store) GetFolderGitPull(ctx context.Context, agentID, id uuid.UUID) (FolderGitPull, error) {
	p, err := scanGitPull(s.Pool.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE id = $1 AND agent_id = $2`, id, agentID))
	return p, notFoundIfNoRows(err, "Pull operation")
}

// ListFolderGitPulls returns the Agent's most recent limit pulls, newest first.
func (s *Store) ListFolderGitPulls(ctx context.Context, agentID uuid.UUID, limit int) ([]FolderGitPull, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (FolderGitPull, error) { return scanGitPull(r) })
}

// StartFolderGitPull marks a queued pull as in progress; returns false when it is no longer queued.
func (s *Store) StartFolderGitPull(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE agent_folder_git_pulls SET status = 'running' WHERE id = $1 AND status = 'queued'`, id)
	return tag.RowsAffected() == 1, err
}

// FinishFolderGitPull records the result of a pull; an empty code means success.
func (s *Store) FinishFolderGitPull(ctx context.Context, id uuid.UUID, code, message string) error {
	status := "succeeded"
	if code != "" {
		status = "failed"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE agent_folder_git_pulls SET status = $2, error_code = nullif($3, ''), error_message = nullif($4, ''), finished_at = $5
		WHERE id = $1 AND status IN ('queued', 'running')`, id, status, code, message, s.Pool.Now())
	return err
}

// FailInterruptedFolderGitPulls marks, at startup, the pulls interrupted by the previous run as failed
// (server_restart) and returns them (so their temporary directories can be cleaned up).
func (s *Store) FailInterruptedFolderGitPulls(ctx context.Context) ([]FolderGitPull, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE agent_folder_git_pulls SET status = 'failed', error_code = 'server_restart', error_message = 'The server restarted while the pull was in progress, so it was aborted.', finished_at = $1
		WHERE status IN ('queued', 'running') RETURNING `+gitPullColumns, s.Pool.Now())
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (FolderGitPull, error) { return scanGitPull(r) })
}

// LegacyFolderSource is a retired uploaded-material source that has not been replayed yet.
type LegacyFolderSource struct {
	ID       uuid.UUID
	AgentID  uuid.UUID
	Path     string
	SyncedAt *time.Time
	Files    []SyncFile
}

// PendingLegacyFolderSources returns the uploaded-material sources of non-deleted Agents that have not been replayed
// yet, with all their file records (including those pending deletion).
func (s *Store) PendingLegacyFolderSources(ctx context.Context) ([]LegacyFolderSource, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id, s.agent_id, s.path, s.synced_at FROM agent_folder_sources s JOIN agents a ON a.id = s.agent_id
		WHERE a.deleted_at IS NULL AND s.kind = 'files' AND NOT EXISTS (SELECT 1 FROM agent_folder_legacy_applied l WHERE l.source_id = s.id)
		ORDER BY s.agent_id, s.path`)
	if err != nil {
		return nil, err
	}
	out, err := db.CollectRows(rows, func(r db.CollectableRow) (LegacyFolderSource, error) {
		var x LegacyFolderSource
		return x, r.Scan(&x.ID, &x.AgentID, &x.Path, &x.SyncedAt)
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Files, err = s.ListSourceFilesForSync(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RecordLegacyFolderApplied records that a retired source has been replayed onto disk.
func (s *Store) RecordLegacyFolderApplied(ctx context.Context, src LegacyFolderSource, written, deleted int) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO agent_folder_legacy_applied (source_id, agent_id, files_written, files_deleted) VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_id) DO NOTHING`, src.ID, src.AgentID, written, deleted)
	return err
}

// LegacyGitSource is a retired code repository source (used only to count, at startup, the sources that were never
// cloned successfully).
type LegacyGitSource struct {
	AgentID uuid.UUID
	Path    string
}

// LegacyGitSources returns the retired code repository sources of non-deleted Agents.
func (s *Store) LegacyGitSources(ctx context.Context) ([]LegacyGitSource, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.agent_id, s.path FROM agent_folder_sources s JOIN agents a ON a.id = s.agent_id
		WHERE a.deleted_at IS NULL AND s.kind = 'git' ORDER BY s.agent_id, s.path`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (LegacyGitSource, error) {
		var x LegacyGitSource
		return x, r.Scan(&x.AgentID, &x.Path)
	})
}
