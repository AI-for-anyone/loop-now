package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FolderSource 是已退役的文件夹来源：只用于旧任务（快照 v8）的冻结来源同步，以及启动时的一次性回放。
type FolderSource struct {
	ID        uuid.UUID
	AgentID   uuid.UUID
	Path      string
	Kind      string // git / files
	GitURL    string
	GitBranch string
	GitCommit string // 任务来源快照的固定commit；常态数据库来源为空
	SyncedAt  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

const sourceColumns = `id, agent_id, path, kind, coalesce(git_url, ''), coalesce(git_branch, ''), synced_at, created_at, updated_at`

func scanSource(row pgx.Row) (FolderSource, error) {
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

// SyncFile 是同步时需要的文件信息（包含待删除记录）。
type SyncFile struct {
	Path      string
	SHA256    string
	Pending   string
	UpdatedAt time.Time
}

// ListSourceFilesForSync 返回来源的全部文件记录，包括待删除记录。
func (s *Store) ListSourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]SyncFile, error) {
	rows, err := s.Pool.Query(ctx, `SELECT path, blob_sha256, pending, updated_at FROM agent_folder_files WHERE source_id = $1 ORDER BY path`, sourceID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SyncFile, error) {
		var f SyncFile
		err := r.Scan(&f.Path, &f.SHA256, &f.Pending, &f.UpdatedAt)
		return f, err
	})
}

// MarkSourceSynced 记录来源已同步到磁盘，并清除已处理的人工修改记录。
// 只清除 updated_at 与处理时一致的记录：处理期间人又改过的文件保留标记，下次再同步。
func (s *Store) MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []SyncFile) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE agent_folder_sources SET synced_at = coalesce(synced_at, now()) WHERE id = $1`, sourceID); err != nil {
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
