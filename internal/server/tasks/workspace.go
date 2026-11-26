package tasks

import (
	"context"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

// RemoveWorkspace removes the kept worktree of an ended task, for a person in the console. Until workspaces are
// created no task has a kept worktree, so every known task answers task_workspace_not_kept.
func (s *Service) RemoveWorkspace(ctx context.Context, p auth.Principal, id uuid.UUID, in taskmodel.RemoveWorkspaceInput) (taskmodel.Detail, error) {
	if p.Kind != auth.KindConsole {
		return taskmodel.Detail{}, apperr.Forbidden("Only a person can remove a task's worktree.")
	}
	if _, err := s.Store.GetTask(ctx, id); err != nil {
		return taskmodel.Detail{}, err
	}
	return taskmodel.Detail{}, apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The task has no kept worktree to remove.")
}
