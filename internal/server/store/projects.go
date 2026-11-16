package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// projectColumns returns the project and the number of its non-deleted Agents.
const projectColumns = `p.id, p.organization_id, p.name, p.created_at, p.is_default, p.manager_agent_id, p.updated_at,
	(SELECT count(*) FROM agents ca WHERE ca.project_id = p.id AND ca.deleted_at IS NULL)`

func scanProject(row db.Scanner) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.CreatedAt, &p.IsDefault, &p.ManagerAgentID, &p.UpdatedAt, &p.AgentCount)
	return p, err
}

// ProjectBusyAgent is an Agent that is not idle, listed when deleting a project is rejected.
type ProjectBusyAgent struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// ProjectNotIdleDetails are the details of the agent_not_idle error when deleting a project.
type ProjectNotIdleDetails struct {
	Agents []ProjectBusyAgent `json:"agents"`
}

// ListProjects lists the non-deleted projects of an organization: the default project first, the rest sorted by name.
func (s *Store) ListProjects(ctx context.Context, orgID uuid.UUID) ([]Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectColumns+` FROM projects p
		WHERE p.organization_id = $1 AND p.deleted_at IS NULL ORDER BY p.is_default DESC, p.name COLLATE NOCASE, p.name, p.id`, orgID)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, func(row db.CollectableRow) (Project, error) { return scanProject(row) })
}

// GetProject returns a non-deleted project.
func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (Project, error) {
	p, err := scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects p WHERE p.id = $1 AND p.deleted_at IS NULL`, id))
	return p, notFoundIfNoRows(err, "项目")
}

// CreateProject creates a project in an organization.
func (s *Store) CreateProject(ctx context.Context, orgID uuid.UUID, name string) (Project, error) {
	id := uuid.Must(uuid.NewV7())
	if _, err := s.Pool.Exec(ctx, `INSERT INTO projects (id, organization_id, name) VALUES ($1, $2, $3)`, id, orgID, name); err != nil {
		return Project{}, projectNameConflict(err, name)
	}
	return s.GetProject(ctx, id)
}

// RenameProject renames a project. Addresses are built from the names, so Agent addresses change with it and no other
// data needs to change.
func (s *Store) RenameProject(ctx context.Context, id uuid.UUID, name string) (Project, error) {
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveProject(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE projects SET name = $2, updated_at = $3 WHERE id = $1`, id, name, tx.Now())
		return projectNameConflict(err, name)
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, id)
}

// DeleteProject soft-deletes a project and returns the Agents deleted with it.
// Returns agent_not_idle (details lists those Agents) when any non-deleted Agent has a run in progress or queued
// messages.
// Once that passes: deletes the encrypted credentials of these Agents, soft-deletes the Agents and the project, and
// clears the Manager. The default project cannot be deleted.
// The caller stops the Pi processes and deletes the credential files after commit.
func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var isDefault bool
		if err := tx.QueryRow(ctx, `SELECT is_default FROM projects WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&isDefault); err != nil {
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
			active, e := db.CollectRows(rows, func(r db.CollectableRow) (activeTask, error) {
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
		// Write transactions are serialized: no new run can be queued between this busy check and the soft delete.
		agentRows, err := tx.Query(ctx, `SELECT id FROM agents WHERE project_id = $1 AND deleted_at IS NULL ORDER BY id`, id)
		if err != nil {
			return err
		}
		ids, err = db.CollectRows(agentRows, db.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		busyRows, err := tx.Query(ctx, `SELECT a.id, a.name FROM agents a
			WHERE a.id IN (SELECT value FROM json_each($1)) AND EXISTS (SELECT 1 FROM runs r WHERE r.agent_id = a.id AND r.status IN ('queued', 'starting', 'running', 'aborting') AND NOT(r.status='queued' AND r.origin='sleep_wake'))
			ORDER BY a.id`, ids)
		if err != nil {
			return err
		}
		busy, err := db.CollectRows(busyRows, func(row db.CollectableRow) (ProjectBusyAgent, error) {
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
			if _, err := tx.Exec(ctx, `UPDATE agents SET deleted_at = $2, updated_at = $2 WHERE id IN (SELECT value FROM json_each($1))`, ids, tx.Now()); err != nil {
				return err
			}
		}
		// Shared accounts exist independently of live Agents, including historical
		// versions and deleted-account tombstones. Purge them even for an empty
		// project, in this same deletion transaction.
		if err := purgeProjectCredentials(ctx, tx, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE projects SET deleted_at = $2, updated_at = $2 WHERE id = $1`, id, tx.Now())
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

// SetProjectManager sets or replaces the project's Manager. The Agent must be a non-deleted Agent of this project,
// otherwise validation of the agentId field fails.
func (s *Store) SetProjectManager(ctx context.Context, projectID, agentID uuid.UUID) (Project, error) {
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveProject(ctx, tx, projectID); err != nil {
			return err
		}
		var ok bool
		// Serialized with Agent deletion in write transactions, so a deleted Agent cannot be made Manager.
		err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`, agentID, projectID).Scan(&ok)
		if err != nil {
			if errors.Is(err, db.ErrNoRows) {
				return apperr.Invalid("agentId", "Manager 必须是本项目内未删除的 Agent")
			}
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE projects SET manager_agent_id = $2, updated_at = $3 WHERE id = $1`, projectID, agentID, tx.Now())
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}

// ClearProjectManager clears the project's Manager; also succeeds when there was none.
func (s *Store) ClearProjectManager(ctx context.Context, projectID uuid.UUID) (Project, error) {
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := requireLiveProject(ctx, tx, projectID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE projects SET manager_agent_id = NULL, updated_at = $2 WHERE id = $1`, projectID, tx.Now())
		return err
	})
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(ctx, projectID)
}

// requireLiveProject confirms the project exists and is not deleted. Write transactions are serialized, so the row
// does not need to be locked.
func requireLiveProject(ctx context.Context, tx *db.Tx, id uuid.UUID) error {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT true FROM projects WHERE id = $1 AND deleted_at IS NULL`, id).Scan(&ok)
	return notFoundIfNoRows(err, "项目")
}

// projectNameConflict turns a unique constraint violation for a duplicate name within an organization into
// name_conflict.
func projectNameConflict(err error, name string) error {
	if isUniqueViolation(err, "projects_name_live") {
		return apperr.Conflict(apperr.CodeNameConflict, "已有名为 "+name+" 的项目")
	}
	return err
}
