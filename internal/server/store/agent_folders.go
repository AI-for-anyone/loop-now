package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// FolderGitPull 是一次“从 Git 拉取”到 Agent 文件夹的操作。
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

func scanGitPull(row pgx.Row) (FolderGitPull, error) {
	var p FolderGitPull
	err := row.Scan(&p.ID, &p.AgentID, &p.ActionID, &p.URL, &p.Branch, &p.Path, &p.Status, &p.ErrorCode, &p.ErrorMessage, &p.CreatedAt, &p.FinishedAt)
	return p, err
}

// CreateFolderGitPull 登记一次排队的拉取。同一 Agent 的同一 actionId 返回原操作（created=false），
// 内容不同时返回 action_conflict；同一目标已有进行中的拉取时返回 path_conflict。
func (s *Store) CreateFolderGitPull(ctx context.Context, p FolderGitPull) (FolderGitPull, bool, error) {
	var out FolderGitPull
	created := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, p.AgentID); err != nil {
			return err
		}
		existing, err := scanGitPull(tx.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 AND action_id = $2`, p.AgentID, p.ActionID))
		if err == nil {
			if existing.URL != p.URL || existing.Branch != p.Branch || existing.Path != p.Path {
				return apperr.Conflict(apperr.CodeActionConflict, "动作ID已用于其他请求")
			}
			out = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_folder_git_pulls WHERE agent_id = $1 AND path = $2 AND status IN ('queued', 'running'))`, p.AgentID, p.Path).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return apperr.Conflict(apperr.CodePathConflict, "这个子目录已有进行中的拉取")
		}
		out, err = scanGitPull(tx.QueryRow(ctx, `INSERT INTO agent_folder_git_pulls (id, agent_id, action_id, url, branch, path, status)
			VALUES ($1, $2, $3, $4, $5, $6, 'queued') RETURNING `+gitPullColumns, p.ID, p.AgentID, p.ActionID, p.URL, p.Branch, p.Path))
		created = err == nil
		return err
	})
	return out, created, err
}

// FindFolderGitPull 按 actionId 查找 Agent 已受理的拉取。
func (s *Store) FindFolderGitPull(ctx context.Context, agentID, actionID uuid.UUID) (FolderGitPull, bool, error) {
	p, err := scanGitPull(s.Pool.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 AND action_id = $2`, agentID, actionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
}

// GetFolderGitPull 返回 Agent 的一次拉取；不存在时返回 not_found。
func (s *Store) GetFolderGitPull(ctx context.Context, agentID, id uuid.UUID) (FolderGitPull, error) {
	p, err := scanGitPull(s.Pool.QueryRow(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE id = $1 AND agent_id = $2`, id, agentID))
	return p, notFoundIfNoRows(err, "拉取操作")
}

// ListFolderGitPulls 返回 Agent 最近的 limit 次拉取，新的在前。
func (s *Store) ListFolderGitPulls(ctx context.Context, agentID uuid.UUID, limit int) ([]FolderGitPull, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+gitPullColumns+` FROM agent_folder_git_pulls WHERE agent_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FolderGitPull, error) { return scanGitPull(r) })
}

// StartFolderGitPull 把排队的拉取标为进行中；已不在排队时返回 false。
func (s *Store) StartFolderGitPull(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE agent_folder_git_pulls SET status = 'running' WHERE id = $1 AND status = 'queued'`, id)
	return tag.RowsAffected() == 1, err
}

// FinishFolderGitPull 记下拉取的结果；code 为空表示成功。
func (s *Store) FinishFolderGitPull(ctx context.Context, id uuid.UUID, code, message string) error {
	status := "succeeded"
	if code != "" {
		status = "failed"
	}
	_, err := s.Pool.Exec(ctx, `UPDATE agent_folder_git_pulls SET status = $2, error_code = nullif($3, ''), error_message = nullif($4, ''), finished_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')`, id, status, code, message)
	return err
}

// FailInterruptedFolderGitPulls 在启动时把上次运行中断的拉取标为失败（server_restart），返回它们（用于清理临时目录）。
func (s *Store) FailInterruptedFolderGitPulls(ctx context.Context) ([]FolderGitPull, error) {
	rows, err := s.Pool.Query(ctx, `UPDATE agent_folder_git_pulls SET status = 'failed', error_code = 'server_restart', error_message = '服务重启时仍在进行，已中止', finished_at = now()
		WHERE status IN ('queued', 'running') RETURNING `+gitPullColumns)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (FolderGitPull, error) { return scanGitPull(r) })
}

// LegacyFolderSource 是一个尚未回放的退役上传资料来源。
type LegacyFolderSource struct {
	ID       uuid.UUID
	AgentID  uuid.UUID
	Path     string
	SyncedAt *time.Time
	Files    []SyncFile
}

// PendingLegacyFolderSources 返回未删除的 Agent 名下、尚未回放过的上传资料来源及其全部文件记录（含待删除）。
func (s *Store) PendingLegacyFolderSources(ctx context.Context) ([]LegacyFolderSource, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.id, s.agent_id, s.path, s.synced_at FROM agent_folder_sources s JOIN agents a ON a.id = s.agent_id
		WHERE a.deleted_at IS NULL AND s.kind = 'files' AND NOT EXISTS (SELECT 1 FROM agent_folder_legacy_applied l WHERE l.source_id = s.id)
		ORDER BY s.agent_id, s.path`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LegacyFolderSource, error) {
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

// RecordLegacyFolderApplied 记下一个退役来源已回放到磁盘。
func (s *Store) RecordLegacyFolderApplied(ctx context.Context, src LegacyFolderSource, written, deleted int) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO agent_folder_legacy_applied (source_id, agent_id, files_written, files_deleted) VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_id) DO NOTHING`, src.ID, src.AgentID, written, deleted)
	return err
}

// LegacyGitSource 是一个退役的代码仓库来源（只用于启动时统计从未克隆成功的来源）。
type LegacyGitSource struct {
	AgentID uuid.UUID
	Path    string
}

// LegacyGitSources 返回未删除的 Agent 名下的退役代码仓库来源。
func (s *Store) LegacyGitSources(ctx context.Context) ([]LegacyGitSource, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.agent_id, s.path FROM agent_folder_sources s JOIN agents a ON a.id = s.agent_id
		WHERE a.deleted_at IS NULL AND s.kind = 'git' ORDER BY s.agent_id, s.path`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LegacyGitSource, error) {
		var x LegacyGitSource
		return x, r.Scan(&x.AgentID, &x.Path)
	})
}
