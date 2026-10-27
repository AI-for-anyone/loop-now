package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
)

// projectColumns 返回项目及其未删除 Agent 的数量。
const projectColumns = `p.id, p.organization_id, p.name, p.created_at, p.is_default, p.manager_agent_id, p.updated_at,
	(SELECT count(*) FROM agents ca WHERE ca.project_id = p.id AND ca.deleted_at IS NULL)`

func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.CreatedAt, &p.IsDefault, &p.ManagerAgentID, &p.UpdatedAt, &p.AgentCount)
	return p, err
}

// ProjectBusyAgent 是删除项目被拒绝时列出的不空闲 Agent。
type ProjectBusyAgent struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ProjectNotIdleDetails 是删除项目时 agent_not_idle 错误的 details。
type ProjectNotIdleDetails struct {
	Agents []ProjectBusyAgent `json:"agents"`
}

// ListProjects 列出组织下未删除的项目：默认项目在前，其余按名称排序。
func (s *Store) ListProjects(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectColumns+` FROM projects p
		WHERE p.organization_id = $1 AND p.deleted_at IS NULL ORDER BY p.is_default DESC, p.name, p.id`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Project, error) { return scanProject(row) })
}

// GetProject 返回未删除的项目。
func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (Project, error) {
	p, err := scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects p WHERE p.id = $1 AND p.deleted_at IS NULL`, id))
	return p, notFoundIfNoRows(err, "项目")
}

// CreateProject 在组织下新建项目。
func (s *Store) CreateProject(ctx context.Context, orgID uuid.UUID, name string) (Project, error) {
	id := uuid.Must(uuid.NewV7())
	if _, err := s.Pool.Exec(ctx, `INSERT INTO projects (id, organization_id, name) VALUES ($1, $2, $3)`, id, orgID, name); err != nil {
		return Project{}, projectNameConflict(err, name)
	}
	return s.GetProject(ctx, id)
}

// RenameProject 改名。地址由名称拼出，Agent 的地址随之变化，不需要改其他数据。
func (s *Store) RenameProject(ctx context.Context, id uuid.UUID, name string) (Project, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveProject(ctx, tx, id, "FOR UPDATE"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE projects SET name = $2, updated_at = now() WHERE id = $1`, id, name)
		return projectNameConflict(err, name)
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, id)
}

// DeleteProject 软删除项目，返回随之删除的 Agent。
// 锁住项目与其中未删除的 Agent；任一 Agent 有进行中的执行或排队消息时返回 agent_not_idle（details 列出这些 Agent）。
// 通过后：删除这些 Agent 的凭证密文，软删除 Agent 与项目，并取消 Manager。默认项目不能删除。
// 停止 Pi 进程与删除凭证文件由调用方在提交后完成。
func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var isDefault bool
		if err := tx.QueryRow(ctx, `SELECT is_default FROM projects WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&isDefault); err != nil {
			return notFoundIfNoRows(err, "项目")
		}
		if isDefault {
			return apperr.Invalid("projectId", "默认项目不能删除")
		}
		var taskActive bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_records WHERE project_id=$1 AND ended_at IS NULL)`, id).Scan(&taskActive); err != nil {
			return err
		}
		if taskActive {
			type activeTask struct {
				ID     uuid.UUID `json:"id"`
				Status string    `json:"status"`
			}
			rows, e := tx.Query(ctx, `SELECT id,status FROM task_records WHERE project_id=$1 AND ended_at IS NULL ORDER BY created_at,id`, id)
			if e != nil {
				return e
			}
			active, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (activeTask, error) {
				var a activeTask
				e := r.Scan(&a.ID, &a.Status)
				return a, e
			})
			if e != nil {
				return e
			}
			conflict := apperr.Conflict(apperr.CodeTaskNotIdle, "项目中存在未结束任务，不能删除")
			conflict.Details = struct {
				ProjectID uuid.UUID    `json:"projectId"`
				Tasks     []activeTask `json:"tasks"`
			}{id, active}
			return conflict
		}
		// 先锁住项目内未删除的 Agent，再用新的语句检查忙闲：READ COMMITTED 下新语句能看到在等锁期间已提交的执行；
		// 若在同一条带 FOR UPDATE 的语句里判断忙闲，子查询仍按语句开始时的快照，会漏掉刚排队的消息。
		lockRows, err := tx.Query(ctx, `SELECT id FROM agents WHERE project_id = $1 AND deleted_at IS NULL ORDER BY id FOR UPDATE`, id)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(lockRows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		busyRows, err := tx.Query(ctx, `SELECT a.id, a.name FROM agents a
			WHERE a.id = ANY($1) AND EXISTS (SELECT 1 FROM runs r WHERE r.agent_id = a.id AND r.status IN ('queued', 'starting', 'running', 'aborting') AND NOT(r.status='queued' AND r.origin='sleep_wake'))
			ORDER BY a.id`, ids)
		if err != nil {
			return err
		}
		busy, err := pgx.CollectRows(busyRows, func(row pgx.CollectableRow) (ProjectBusyAgent, error) {
			var b ProjectBusyAgent
			err := row.Scan(&b.ID, &b.Name)
			return b, err
		})
		if err != nil {
			return err
		}
		if len(busy) > 0 {
			e := apperr.Conflict(apperr.CodeAgentNotIdle, "项目中还有 Agent 正在回复或有排队消息，请等它们空闲后再删除项目")
			e.Details = ProjectNotIdleDetails{Agents: busy}
			return e
		}
		if s.CLIImportDeletingTx != nil {
			if err := s.CLIImportDeletingTx(ctx, tx, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE projects SET manager_agent_id = NULL WHERE id = $1`, id); err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := purgeAgentCredentials(ctx, tx, ids); err != nil {
				return err
			}
			for _, agentID := range ids {
				if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "owner_deleted"); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE agents SET deleted_at = now(), updated_at = now() WHERE id = ANY($1)`, ids); err != nil {
				return err
			}
		}
		// Shared accounts exist independently of live Agents, including historical
		// versions and deleted-account tombstones. Purge them even for an empty
		// project, in this same deletion transaction.
		if err := purgeProjectCredentials(ctx, tx, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE projects SET deleted_at = now(), updated_at = now() WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.VerificationInvalidated != nil {
		s.VerificationInvalidated(id, nil)
	}
	if s.CLIImportDeleted != nil {
		s.CLIImportDeleted(id)
	}
	return ids, nil
}

// SetProjectManager 指定或更换项目 Manager。Agent 必须是本项目内未删除的 Agent，否则字段 agentId 校验失败。
func (s *Store) SetProjectManager(ctx context.Context, projectID, agentID uuid.UUID) (Project, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveProject(ctx, tx, projectID, "FOR UPDATE"); err != nil {
			return err
		}
		var ok bool
		// 锁住 Agent 行，与删除该 Agent 串行化，不会把已删除的 Agent 设为 Manager。
		err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL FOR SHARE`, agentID, projectID).Scan(&ok)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.Invalid("agentId", "Manager 必须是本项目内未删除的 Agent")
			}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE projects SET manager_agent_id = $2, updated_at = now() WHERE id = $1`, projectID, agentID)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}

// ClearProjectManager 取消项目 Manager；原本没有时也成功。
func (s *Store) ClearProjectManager(ctx context.Context, projectID uuid.UUID) (Project, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockLiveProject(ctx, tx, projectID, "FOR UPDATE"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE projects SET manager_agent_id = NULL, updated_at = now() WHERE id = $1`, projectID)
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}

// lockLiveProject 锁住未删除的项目行；lock 为 "FOR UPDATE" 或 "FOR SHARE"。
func lockLiveProject(ctx context.Context, tx pgx.Tx, id uuid.UUID, lock string) error {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT true FROM projects WHERE id = $1 AND deleted_at IS NULL `+lock, id).Scan(&ok)
	return notFoundIfNoRows(err, "项目")
}

// projectNameConflict 把组织内重名的唯一约束冲突转为 name_conflict。
func projectNameConflict(err error, name string) error {
	if isUniqueViolation(err, "projects_name_live") {
		return apperr.Conflict(apperr.CodeNameConflict, "已有名为 "+name+" 的项目")
	}
	return err
}
