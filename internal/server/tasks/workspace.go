package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	folder, root, branch, baseBranch string
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
	if in.Mode != "" && in.Mode != taskmodel.WorkspaceModeAuto && in.Mode != taskmodel.WorkspaceModeGit {
		return workspacePlan{}, apperr.Invalid("workspace.mode", "Copy mode is not available yet.")
	}
	folder := in.Folder
	if !filepath.IsAbs(folder) {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be an absolute path.")
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be a folder on this computer.")
	}
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be a folder on this computer.")
	}
	plan.folder = folder
	allowed, err := s.workspaceFolders(ctx, projectID, def)
	if err != nil {
		return workspacePlan{}, err
	}
	if !slices.ContainsFunc(allowed, func(dir string) bool { return within(dir, real) }) {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be, or lie inside, the working folder of an Agent in the flow or a local directory of the project.")
	}
	if within(s.Source.WorktreeRoot(), real) {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must not be inside a task's worktree.")
	}
	plan.root, err = gitworkspace.Resolve(ctx, s.Source.git(), real)
	if err != nil {
		if ctx.Err() != nil {
			return workspacePlan{}, ctx.Err()
		}
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be inside a git work tree: "+err.Error())
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
	return &taskmodel.Workspace{Mode: taskmodel.WorkspaceModeGit, Folder: plan.folder,
		Path: filepath.Join(s.Source.WorktreeRoot(), id.String(), filepath.Base(plan.root)), State: taskmodel.WorkspaceStatePending,
		Git: &taskmodel.WorkspaceGit{Repository: plan.root, Branch: branch, BaseBranch: plan.baseBranch}}
}

// prepareWorkspace creates the task's worktree while the task prepares (adopting it when an earlier preparation already
// made it) and returns the base it starts from; nil when the task has no pending workspace.
func (s *Service) prepareWorkspace(ctx context.Context, r taskmodel.Record) (*gitworkspace.Result, error) {
	w := r.View.Workspace
	if w == nil || w.State != taskmodel.WorkspaceStatePending || w.Git == nil {
		return nil, nil
	}
	if !within(s.Source.WorktreeRoot(), w.Path) {
		return nil, errors.New("the recorded worktree is not under Loop Now's worktree folder")
	}
	g := w.Git
	res, err := gitworkspace.Create(ctx, s.Source.git(), gitworkspace.Spec{Repository: g.Repository, Path: w.Path, Branch: g.Branch,
		BaseBranch: g.BaseBranch, BranchGiven: r.WorkspaceBranchGiven})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// activateWorkspace records the created worktree on the task.
func activateWorkspace(r *taskmodel.Record, res *gitworkspace.Result) {
	if res == nil || r.View.Workspace == nil || r.View.Workspace.Git == nil {
		return
	}
	commit := res.BaseCommit
	r.View.Workspace.State = taskmodel.WorkspaceStateActive
	r.View.Workspace.Git.BaseBranch = res.BaseBranch
	r.View.Workspace.Git.BaseCommit = &commit
}

// workspaceBlock tells a step of a task with a worktree where and how it works.
func workspaceBlock(w taskmodel.Workspace) string {
	g := w.Git
	if g == nil {
		return ""
	}
	base := g.BaseBranch
	if g.BaseCommit != nil {
		base += " at " + shortCommit(*g.BaseCommit)
	}
	return fmt.Sprintf("This task works in one git worktree: %s, on branch %s, started from %s. Work only in this worktree and stay on this "+
		"branch: do not switch branches, and do not touch the repository's main checkout (%s) or any other worktree. Commit your work "+
		"and push it to this branch; this branch has one pull request for the whole task. Your own folder is still at its usual place "+
		"(see \"Folders and context\").", w.Path, g.Branch, base, g.Repository)
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// cleanupWorkspace settles the worktree of an ended task once its steps are released: removed when that loses
// nothing (no uncommitted or untracked changes, every commit merged into the base on the remote or on the branch's
// own remote ref), with the local branch deleted only when merged; otherwise kept, with what removing it would lose.
// A missing folder counts as removed. Running it again changes nothing; a git failure is returned (cleanup failed,
// retried later).
func (s *Service) cleanupWorkspace(ctx context.Context, id uuid.UUID) error {
	r, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	w := r.View.Workspace
	if w == nil || w.State == taskmodel.WorkspaceStateRemoved || w.State == taskmodel.WorkspaceStateKept || w.Git == nil {
		return nil
	}
	if !within(s.Source.WorktreeRoot(), w.Path) || w.Path == s.Source.WorktreeRoot() {
		return errors.New("the recorded worktree is not under Loop Now's worktree folder; it is left alone")
	}
	git, g := s.Source.git(), w.Git
	st, err := gitworkspace.Inspect(ctx, git, w.Path, g.BaseBranch)
	if err != nil {
		return err
	}
	var kept *taskmodel.WorkspaceKept
	switch {
	case st.Missing:
		if err := gitworkspace.Remove(ctx, git, g.Repository, w.Path, false); err != nil {
			return err
		}
	case st.UncommittedFiles == 0 && st.UnpushedCommits == 0:
		if err := gitworkspace.Remove(ctx, git, g.Repository, w.Path, false); err != nil {
			// git refuses any worktree holding a submodule without force, even a clean one; Inspect has just found
			// this one clean, submodule changes included.
			if !strings.Contains(err.Error(), "submodule") {
				return err
			}
			if err := gitworkspace.Remove(ctx, git, g.Repository, w.Path, true); err != nil {
				return err
			}
		}
		if st.Merged {
			if err := gitworkspace.DeleteBranch(ctx, git, g.Repository, g.Branch, g.BaseBranch); err != nil && s.Log != nil {
				s.Log.Info("kept the task's merged branch", "task_id", id, "branch", g.Branch, "reason", err.Error())
			}
		}
	default:
		kept = &taskmodel.WorkspaceKept{UncommittedFiles: st.UncommittedFiles, UnpushedCommits: st.UnpushedCommits}
	}
	if kept == nil {
		_ = os.Remove(filepath.Dir(w.Path)) // <worktrees>/<task id>, when empty
	}
	_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		cur := m.Record.View.Workspace
		if cur == nil || cur.State == taskmodel.WorkspaceStateRemoved || cur.State == taskmodel.WorkspaceStateKept {
			m.Unchanged()
			return nil, nil
		}
		cur.Kept = kept
		cur.State = taskmodel.WorkspaceStateRemoved
		if kept != nil {
			cur.State = taskmodel.WorkspaceStateKept
		}
		return nil, nil
	})
	return err
}

// RemoveWorkspace removes the kept worktree of an ended task, for a person in the console: exactly the recorded path,
// forced. Uncommitted changes are lost; commits stay on the task's local branch, and those of a detached HEAD are first
// saved under <branch>-detached-<short sha>; the branch and the remote are left alone. Replaying an actionId returns
// the first result.
func (s *Service) RemoveWorkspace(ctx context.Context, p auth.Principal, id uuid.UUID, in taskmodel.RemoveWorkspaceInput) (json.RawMessage, error) {
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("Only a person can remove a task's worktree.")
	}
	if in.ActionID == uuid.Nil {
		return nil, apperr.Invalid("actionId", "A valid actionId is required.")
	}
	action := bodyAction(p, in.ActionID, "removeTaskWorkspace", in)
	settle := func(m *store.TaskMutation) (any, error) {
		w := m.Record.View.Workspace
		if !m.Record.Ended() || w == nil || w.State != taskmodel.WorkspaceStateKept {
			return nil, notKept()
		}
		w.State, w.Kept = taskmodel.WorkspaceStateRemoved, nil
		return m.Record.View, nil
	}
	if recorded, err := s.ActionRecorded(ctx, p, in.ActionID); err != nil {
		return nil, err
	} else if recorded {
		return s.Store.MutateTask(ctx, id, action, settle)
	}
	r, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	w := r.View.Workspace
	if !r.Ended() || w == nil || w.State != taskmodel.WorkspaceStateKept {
		return nil, notKept()
	}
	if s.Source == nil || !within(s.Source.WorktreeRoot(), w.Path) || w.Path == s.Source.WorktreeRoot() {
		return nil, apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The recorded worktree is not under Loop Now's worktree folder; it is not removed.")
	}
	git, g := s.Source.git(), w.Git
	if g == nil {
		return nil, apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "Removing a copied workspace is not available yet.")
	}
	if _, err := os.Stat(w.Path); err == nil {
		if saved, err := gitworkspace.SaveDetached(ctx, git, g.Repository, w.Path, g.Branch); err != nil {
			return nil, err
		} else if saved != "" && s.Log != nil {
			s.Log.Info("saved the commits of a detached HEAD before removing the task's worktree", "task_id", id, "branch", saved)
		}
	}
	if err := gitworkspace.Remove(ctx, git, g.Repository, w.Path, true); err != nil {
		return nil, err
	}
	_ = os.Remove(filepath.Dir(w.Path))
	return s.Store.MutateTask(ctx, id, action, settle)
}

func notKept() error {
	return apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The task's worktree can be removed only after the task has ended and when it was kept.")
}
