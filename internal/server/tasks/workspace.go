package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks/gitworkspace"
	"loop-now/internal/server/workflow"
)

// workspacePlan is a checked workspace request: the repository resolved, the branch and base as given.
type workspacePlan struct {
	root, branch, baseBranch string
}

func (s *Source) git() string {
	if s.GitBin != "" {
		return s.GitBin
	}
	return "git"
}

// WorktreeRoot is the folder under Loop Now's own data that holds every task worktree; nothing outside it is ever
// removed as a worktree.
func (s *Source) WorktreeRoot() string { return filepath.Join(s.WorkRoot, "worktrees") }

// checkWorkspace checks a workspace request before the task is created: the folder must be, or lie inside, the working
// folder of an Agent in the flow or a local directory of the project, and belong to a git work tree; branch names must
// be valid. Every problem is a field error on workspace.*.
func (s *Service) checkWorkspace(ctx context.Context, projectID uuid.UUID, in taskmodel.WorkspaceInput, def workflow.Definition) (workspacePlan, error) {
	if s.Source == nil || s.Source.WorkRoot == "" {
		return workspacePlan{}, apperr.Invalid("workspace", "This server cannot create task worktrees.")
	}
	var plan workspacePlan
	for _, f := range []struct {
		field string
		value *string
		out   *string
	}{{"workspace.branch", in.Branch, &plan.branch}, {"workspace.baseBranch", in.BaseBranch, &plan.baseBranch}} {
		if f.value == nil {
			continue
		}
		if !gitworkspace.ValidBranch(*f.value) {
			return workspacePlan{}, apperr.Invalid(f.field, "Must be a valid git branch name.")
		}
		*f.out = *f.value
	}
	repo := in.Repository
	if !filepath.IsAbs(repo) {
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must be an absolute path.")
	}
	if info, err := os.Stat(repo); err != nil || !info.IsDir() {
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must be a folder on this computer.")
	}
	real, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must be a folder on this computer.")
	}
	allowed, err := s.workspaceFolders(ctx, projectID, def)
	if err != nil {
		return workspacePlan{}, err
	}
	if !slices.ContainsFunc(allowed, func(dir string) bool { return within(dir, real) }) {
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must be, or lie inside, the working folder of an Agent in the flow or a local directory of the project.")
	}
	if within(s.Source.WorktreeRoot(), real) {
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must not be inside a task's worktree.")
	}
	plan.root, err = gitworkspace.Resolve(ctx, s.Source.git(), real)
	if err != nil {
		if ctx.Err() != nil {
			return workspacePlan{}, ctx.Err()
		}
		return workspacePlan{}, apperr.Invalid("workspace.repository", "Must be inside a git work tree: "+err.Error())
	}
	return plan, nil
}

// workspaceFolders are the folders a workspace repository may lie in, with symlinks resolved: the working folders of
// the flow's Agents and the project's local directories that exist.
func (s *Service) workspaceFolders(ctx context.Context, projectID uuid.UUID, def workflow.Definition) ([]string, error) {
	var dirs []string
	seen := map[uuid.UUID]bool{}
	for _, step := range def.Steps {
		if seen[step.AgentID] {
			continue
		}
		seen[step.AgentID] = true
		a, err := s.Store.GetAgent(ctx, step.AgentID)
		if store.IsNotFound(err) {
			continue // Create reports the unknown Agent itself
		}
		if err != nil {
			return nil, err
		}
		if a.WorkingDirectory != nil && *a.WorkingDirectory != "" {
			dirs = append(dirs, *a.WorkingDirectory)
		}
	}
	list, err := s.Store.ProjectLocalDirectories(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, d := range list.Directories {
		dirs = append(dirs, d.Path)
	}
	out := []string{}
	for _, d := range dirs {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			continue
		}
		if real, err := filepath.EvalSymlinks(d); err == nil {
			out = append(out, real)
		}
	}
	return out, nil
}

// within reports whether path is dir or lies inside it (both cleaned, absolute).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// newWorkspace is the workspace of a task being created, before its worktree exists.
func (s *Service) newWorkspace(plan workspacePlan, id uuid.UUID, number int64, title string) *taskmodel.Workspace {
	branch := plan.branch
	if branch == "" {
		branch = gitworkspace.BranchName(int(number), title)
	}
	return &taskmodel.Workspace{Repository: plan.root, Path: filepath.Join(s.Source.WorktreeRoot(), id.String(), filepath.Base(plan.root)),
		Branch: branch, BaseBranch: plan.baseBranch, State: taskmodel.WorkspaceStatePending}
}

// prepareWorkspace creates the task's worktree while the task prepares (adopting it when an earlier preparation already
// made it) and returns the base it starts from; nil when the task has no pending workspace.
func (s *Service) prepareWorkspace(ctx context.Context, r taskmodel.Record) (*gitworkspace.Result, error) {
	w := r.View.Workspace
	if w == nil || w.State != taskmodel.WorkspaceStatePending {
		return nil, nil
	}
	if !within(s.Source.WorktreeRoot(), w.Path) {
		return nil, errors.New("the recorded worktree is not under Loop Now's worktree folder")
	}
	res, err := gitworkspace.Create(ctx, s.Source.git(), gitworkspace.Spec{Repository: w.Repository, Path: w.Path, Branch: w.Branch,
		BaseBranch: w.BaseBranch, BranchGiven: r.WorkspaceBranchGiven})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// activateWorkspace records the created worktree on the task.
func activateWorkspace(r *taskmodel.Record, res *gitworkspace.Result) {
	if res == nil || r.View.Workspace == nil {
		return
	}
	commit := res.BaseCommit
	r.View.Workspace.State = taskmodel.WorkspaceStateActive
	r.View.Workspace.BaseBranch = res.BaseBranch
	r.View.Workspace.BaseCommit = &commit
}

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
