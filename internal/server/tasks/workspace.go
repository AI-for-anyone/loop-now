package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks/copyworkspace"
	"loop-now/internal/server/tasks/gitworkspace"
	"loop-now/internal/server/workflow"

	"github.com/google/uuid"
)

// workspacePlan is a checked workspace request: the mode chosen, the folder as named and resolved, and in git mode the
// repository with the branch and base as given.
type workspacePlan struct {
	mode, note               string
	folder, real             string
	root, branch, baseBranch string
}

func (s *Source) git() string {
	if s.Git != nil {
		return s.Git.Path()
	}
	if s.GitBin != "" {
		return s.GitBin
	}
	return "git"
}

// workingGit returns a git that runs on this computer, without ever opening the macOS developer tools dialog; ok is
// false when there is none.
func (s *Source) workingGit(ctx context.Context) (string, bool) {
	if s.Git != nil {
		return s.Git.Available(ctx)
	}
	return s.git(), true
}

// WorktreeRoot is the folder under Loop Now's own data that holds every task worktree; nothing outside it is ever
// removed as a worktree.
func (s *Source) WorktreeRoot() string { return filepath.Join(s.WorkRoot, "worktrees") }

// Notes on a workspace whose mode is not the one the folder suggests (TaskWorkspace.modeNote).
const (
	noteNoGit     = "No working git on this computer, so the folder was copied."
	noteGitFailed = "Git could not use this folder (%s), so the folder was copied."
)

// checkWorkspace checks a workspace request before the task is created and chooses its mode. The folder must be, or
// lie inside, the working folder of an Agent in the flow or a local directory of the project. Mode auto is git when
// the folder is inside a git work tree and a working git is on this computer, else copy; git requires both; copy
// copies any folder. Branch names are for git mode only and must be valid. Every problem is a field error on
// workspace.*.
func (s *Service) checkWorkspace(ctx context.Context, projectID uuid.UUID, in taskmodel.WorkspaceInput, def workflow.Definition) (workspacePlan, error) {
	if s.Source == nil || s.Source.WorkRoot == "" {
		return workspacePlan{}, apperr.Invalid("workspace", "This server cannot create task workspaces.")
	}
	var plan workspacePlan
	switch in.Mode {
	case "", taskmodel.WorkspaceModeAuto, taskmodel.WorkspaceModeGit, taskmodel.WorkspaceModeCopy:
	default:
		return workspacePlan{}, apperr.Invalid("workspace.mode", "Must be auto, git or copy.")
	}
	for _, f := range []struct {
		field string
		value *string
		out   *string
	}{{"workspace.branch", in.Branch, &plan.branch}, {"workspace.baseBranch", in.BaseBranch, &plan.baseBranch}} {
		if f.value == nil {
			continue
		}
		if in.Mode == taskmodel.WorkspaceModeCopy {
			return workspacePlan{}, apperr.Invalid(f.field, "Only for git mode; a copied folder has no branch.")
		}
		if !gitworkspace.ValidBranch(*f.value) {
			return workspacePlan{}, apperr.Invalid(f.field, "Must be a valid git branch name.")
		}
		*f.out = *f.value
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
	plan.folder, plan.real = folder, real
	allowed, err := s.workspaceFolders(ctx, projectID, def)
	if err != nil {
		return workspacePlan{}, err
	}
	if !slices.ContainsFunc(allowed, func(dir string) bool { return within(dir, real) }) {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must be, or lie inside, the working folder of an Agent in the flow or a local directory of the project.")
	}
	if within(s.Source.WorktreeRoot(), real) {
		return workspacePlan{}, apperr.Invalid("workspace.folder", "Must not be inside a task's workspace.")
	}
	if in.Mode == taskmodel.WorkspaceModeCopy {
		plan.mode = taskmodel.WorkspaceModeCopy
		return plan, nil
	}
	gitPath, gitOK := s.Source.workingGit(ctx)
	var gitErr error
	if gitOK {
		if plan.root, gitErr = gitworkspace.Resolve(ctx, gitPath, real); gitErr == nil {
			plan.mode = taskmodel.WorkspaceModeGit
			return plan, nil
		}
		if ctx.Err() != nil {
			return workspacePlan{}, ctx.Err()
		}
	}
	gitField := "workspace.mode"
	if in.Mode != taskmodel.WorkspaceModeGit {
		// auto: copy, saying why when the folder is in a repository. A branch asked for cannot be honored.
		gitField = "workspace.branch"
		if in.Branch == nil {
			gitField = "workspace.baseBranch"
		}
		if in.Branch == nil && in.BaseBranch == nil {
			plan.mode = taskmodel.WorkspaceModeCopy
			if inGitWorkTree(real) {
				plan.note = noteNoGit
				if gitOK {
					plan.note = fmt.Sprintf(noteGitFailed, gitErr.Error())
				}
			}
			return plan, nil
		}
	}
	if !gitOK {
		return workspacePlan{}, apperr.Invalid(gitField, "Needs git mode, but there is no working git on this computer (on macOS, git needs the developer tools: xcode-select --install).")
	}
	if in.Mode == taskmodel.WorkspaceModeGit {
		gitField = "workspace.folder"
	}
	return workspacePlan{}, apperr.Invalid(gitField, "Needs git mode, but the folder is not inside a git work tree: "+gitErr.Error())
}

// inGitWorkTree reports, without running git, whether dir or a folder above it has a .git entry.
func inGitWorkTree(dir string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
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

// newWorkspace is the workspace of a task being created, before its worktree or copy exists.
func (s *Service) newWorkspace(plan workspacePlan, id uuid.UUID, number int64, title string) *taskmodel.Workspace {
	w := &taskmodel.Workspace{Mode: plan.mode, Folder: plan.folder, State: taskmodel.WorkspaceStatePending}
	if plan.note != "" {
		w.ModeNote = &plan.note
	}
	if plan.mode == taskmodel.WorkspaceModeCopy {
		name := filepath.Base(plan.real)
		if name == copyworkspace.ListingName || name == string(filepath.Separator) {
			name = "folder"
		}
		w.Path = filepath.Join(s.Source.WorktreeRoot(), id.String(), name)
		return w
	}
	branch := plan.branch
	if branch == "" {
		branch = gitworkspace.BranchName(int(number), title)
	}
	w.Path = filepath.Join(s.Source.WorktreeRoot(), id.String(), filepath.Base(plan.root))
	w.Git = &taskmodel.WorkspaceGit{Repository: plan.root, Branch: branch, BaseBranch: plan.baseBranch}
	return w
}

// preparedWorkspace is what preparing made: the worktree's base in git mode, the copy in copy mode.
type preparedWorkspace struct {
	git  *gitworkspace.Result
	copy *taskmodel.WorkspaceCopy
}

// prepareWorkspace creates the task's worktree or copy while the task prepares (adopting one an earlier preparation
// already made); nil when the task has no pending workspace.
func (s *Service) prepareWorkspace(ctx context.Context, r taskmodel.Record) (*preparedWorkspace, error) {
	w := r.View.Workspace
	if w == nil || w.State != taskmodel.WorkspaceStatePending {
		return nil, nil
	}
	if !within(s.Source.WorktreeRoot(), w.Path) || w.Path == s.Source.WorktreeRoot() {
		return nil, errors.New("the recorded workspace is not under Loop Now's worktree folder")
	}
	if w.Mode == taskmodel.WorkspaceModeCopy {
		l, err := copyworkspace.Make(w.Folder, w.Path)
		if err != nil {
			return nil, err
		}
		if l.Left > 0 && s.Log != nil {
			s.Log.Info("left links to outside, git links and special files out of the task's copy", "task_id", r.View.Task.ID, "count", l.Left)
		}
		return &preparedWorkspace{copy: &taskmodel.WorkspaceCopy{Files: l.Files, Bytes: l.Bytes}}, nil
	}
	g := w.Git
	if g == nil {
		return nil, errors.New("the recorded git workspace has no repository")
	}
	res, err := gitworkspace.Create(ctx, s.Source.git(), gitworkspace.Spec{Repository: g.Repository, Path: w.Path, Branch: g.Branch,
		BaseBranch: g.BaseBranch, BranchGiven: r.WorkspaceBranchGiven})
	if err != nil {
		return nil, err
	}
	return &preparedWorkspace{git: &res}, nil
}

// activateWorkspace records the created worktree or copy on the task.
func activateWorkspace(r *taskmodel.Record, p *preparedWorkspace) {
	w := r.View.Workspace
	if p == nil || w == nil {
		return
	}
	switch {
	case p.copy != nil:
		w.Copy = p.copy
	case p.git != nil && w.Git != nil:
		commit := p.git.BaseCommit
		w.Git.BaseBranch = p.git.BaseBranch
		w.Git.BaseCommit = &commit
	default:
		return
	}
	w.State = taskmodel.WorkspaceStateActive
}

// workspaceBlock tells a step of a task with a worktree where and how it works.
func workspaceBlock(w taskmodel.Workspace) string {
	g := w.Git
	if g == nil {
		return fmt.Sprintf("This task works in a copy of the folder %s: %s. Work only in this copy. The original folder is not "+
			"changed by anything done here, and nothing is put back into it; links that pointed outside the folder are not in "+
			"the copy. Save every result the task delivers with `loopctl task artifact put`: the copy is deleted when the task "+
			"completes. Your own folder is still at its usual place (see \"Folders and context\").", w.Folder, w.Path)
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
	if w == nil || w.State == taskmodel.WorkspaceStateRemoved || w.State == taskmodel.WorkspaceStateKept {
		return nil
	}
	if !within(s.Source.WorktreeRoot(), w.Path) || w.Path == s.Source.WorktreeRoot() {
		return errors.New("the recorded workspace is not under Loop Now's worktree folder; it is left alone")
	}
	var kept *taskmodel.WorkspaceKept
	if w.Mode == taskmodel.WorkspaceModeCopy {
		if kept, err = cleanupCopy(r.View.Task.Status, w.Path); err != nil {
			return err
		}
		return s.settleWorkspace(ctx, id, kept)
	}
	g := w.Git
	if g == nil {
		return errors.New("the recorded git workspace has no repository; it is left alone")
	}
	git := s.Source.git()
	st, err := gitworkspace.Inspect(ctx, git, w.Path, g.BaseBranch)
	if err != nil {
		return err
	}
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
	return s.settleWorkspace(ctx, id, kept)
}

// cleanupCopy settles the copy of an ended task: a completed task's copy is removed (its results are artifacts); a
// cancelled task's copy is kept when files were added or changed in it since the start, else removed. The original
// folder is never touched.
func cleanupCopy(status, path string) (*taskmodel.WorkspaceKept, error) {
	if _, err := copyworkspace.ReadListing(path); errors.Is(err, fs.ErrNotExist) {
		return nil, copyworkspace.Remove(path) // the copy was never finished: nothing in it is the task's work
	}
	if status != "completed" {
		changed, missing, err := copyworkspace.Changed(path)
		if err != nil {
			return nil, err
		}
		if !missing && changed > 0 {
			return &taskmodel.WorkspaceKept{ChangedFiles: changed}, nil
		}
	}
	return nil, copyworkspace.Remove(path)
}

// settleWorkspace records the end state of the workspace: kept with what removing it would lose, or removed.
func (s *Service) settleWorkspace(ctx context.Context, id uuid.UUID, kept *taskmodel.WorkspaceKept) error {
	_, err := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
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
		return nil, apperr.Forbidden("Only a person can remove a task's workspace.")
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
		return nil, apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The recorded workspace is not under Loop Now's worktree folder; it is not removed.")
	}
	if w.Mode == taskmodel.WorkspaceModeCopy {
		if err := copyworkspace.Remove(w.Path); err != nil {
			return nil, err
		}
		return s.Store.MutateTask(ctx, id, action, settle)
	}
	git, g := s.Source.git(), w.Git
	if g == nil {
		return nil, apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The recorded git workspace has no repository; it is not removed.")
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
	return apperr.Conflict(apperr.CodeTaskWorkspaceNotKept, "The task's workspace can be removed only after the task has ended and when it was kept.")
}
