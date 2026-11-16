package agentconfig

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// ListProjects lists the projects in the default organization that are not deleted.
func (s *Service) ListProjects(ctx context.Context) ([]store.Project, error) {
	return s.Store.ListProjects(ctx, s.OrgID)
}

// CreateProject creates a project in the default organization.
func (s *Service) CreateProject(ctx context.Context, name string) (store.Project, error) {
	if err := validateProjectName(name); err != nil {
		return store.Project{}, err
	}
	return s.Store.CreateProject(ctx, s.OrgID, name)
}

// RenameProject renames a project; the addresses of the Agents in the project change with it.
func (s *Service) RenameProject(ctx context.Context, id uuid.UUID, name string) (store.Project, error) {
	if err := validateProjectName(name); err != nil {
		return store.Project{}, err
	}
	return s.Store.RenameProject(ctx, id, name)
}

// DeleteProject soft-deletes a project: each of its Agents is handled as a deleted Agent (stop Pi, delete credentials,
// keep the working folder, session files and conversation history). A failed release in the executor is only logged:
// the data is already deleted in the database, and the executor also cleans credentials out of the HOME of deleted
// Agents when it starts.
func (s *Service) DeleteProject(ctx context.Context, id uuid.UUID) error {
	ids, err := s.Store.DeleteProject(ctx, id)
	if err != nil {
		return err
	}
	if s.ModelAuth != nil {
		if err := s.ModelAuth.CancelProjectLogins(ctx, id, nil); err != nil && s.Logger != nil {
			s.Logger.Warn("project login resource cleanup timed out", "projectId", id, "code", "auth_cleanup_timeout")
		}
	}
	if s.CancelProjectCLILogins != nil {
		if e := s.CancelProjectCLILogins(ctx, id); e != nil && s.Logger != nil {
			s.Logger.Warn("project CLI login resource cleanup timed out", "stage", "deleted_project_login_cleanup", "projectId", id.String())
		}
	}
	s.cancelDeletedAgentLogins(ctx, ids)
	for _, aid := range ids {
		if s.Releaser != nil {
			if err := s.Releaser.Release(ctx, aid); err != nil {
				s.Logger.Error("failed to release the run resources of an Agent in a deleted project", "projectId", id, "agentId", aid, "err", err)
			}
		}
		if s.AgentRemoved != nil {
			s.AgentRemoved(aid)
		}
	}
	return nil
}

// SetProjectManager sets or changes the project's Manager.
func (s *Service) SetProjectManager(ctx context.Context, projectID, agentID uuid.UUID) (store.Project, error) {
	p, err := s.Store.SetProjectManager(ctx, projectID, agentID)
	s.managerChanged(ctx, projectID)
	return p, err
}

// ClearProjectManager removes the project's Manager.
func (s *Service) ClearProjectManager(ctx context.Context, projectID uuid.UUID) (store.Project, error) {
	p, err := s.Store.ClearProjectManager(ctx, projectID)
	s.managerChanged(ctx, projectID)
	return p, err
}

// managerChanged makes the overview refresh the project's Agents (isManager may have changed).
func (s *Service) managerChanged(ctx context.Context, projectID uuid.UUID) {
	if s.AgentChanged == nil {
		return
	}
	items, err := s.Store.ListAgents(ctx, store.ProjectScope(projectID))
	if err != nil {
		return
	}
	for _, it := range items {
		s.AgentChanged(it.ID)
	}
}

func validateProjectName(name string) error {
	if msg := naming.Validate("项目名称", name); msg != "" {
		return apperr.Invalid("name", msg)
	}
	return nil
}
