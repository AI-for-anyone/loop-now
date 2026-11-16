package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// FolderSource is a retired folder source: used only to sync the captured (frozen) sources of old tasks (snapshot
// v8), and for the one-time replay at startup.
type FolderSource struct {
	ID        uuid.UUID
	AgentID   uuid.UUID
	Path      string
	Kind      string // git / files
	GitURL    string
	GitBranch string
	GitCommit string // pinned commit of the task source snapshot; empty for a regular database source
	SyncedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

const sourceColumns = `id, agent_id, path, kind, coalesce(git_url, ''), coalesce(git_branch, ''), synced_at, created_at, updated_at`

func scanSource(row db.Scanner) (FolderSource, error) {
	var f FolderSource
	err := row.Scan(&f.ID, &f.AgentID, &f.Path, &f.Kind, &f.GitURL, &f.GitBranch, &f.SyncedAt, &f.CreatedAt, &f.UpdatedAt)
	return f, err
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// SyncFile is the file information needed for a sync (including records pending deletion).
type SyncFile struct {
	Path      string
	SHA256    string
	Pending   string
	UpdatedAt time.Time
}

// ListSourceFilesForSync returns all file records of a source, including records pending deletion.
func (s *Store) ListSourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]SyncFile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT path, blob_sha256, pending, updated_at FROM agent_folder_files WHERE source_id = $1 ORDER BY path`, sourceID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(r db.CollectableRow) (SyncFile, error) {
		var f SyncFile
		err := r.Scan(&f.Path, &f.SHA256, &f.Pending, &f.UpdatedAt)
		return f, err
	})
}

// MarkSourceSynced records that a source has been synced to disk and clears the manual-change records that were
// handled. Only records whose updated_at still matches the one at handling time are cleared: files a person changed
// again during handling keep their mark and are synced next time.
func (s *Store) MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []SyncFile) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE agent_folder_sources SET synced_at = coalesce(synced_at, $2) WHERE id = $1`, sourceID, tx.Now()); err != nil {
			return err
		}
		for _, f := range processed {
			switch f.Pending {
			case "write":
				if _, err := tx.Exec(ctx, `UPDATE agent_folder_files SET pending = 'none'
					WHERE source_id = $1 AND path = $2 AND pending = 'write' AND updated_at = $3`, sourceID, f.Path, f.UpdatedAt); err != nil {
					return err
				}
			case "delete":
				if _, err := tx.Exec(ctx, `DELETE FROM agent_folder_files
					WHERE source_id = $1 AND path = $2 AND pending = 'delete' AND updated_at = $3`, sourceID, f.Path, f.UpdatedAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
