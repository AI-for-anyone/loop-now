package tasks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks/copyworkspace"
	"loop-now/internal/server/tasks/gitworkspace"
	"loop-now/internal/server/workflow"
)

// The workspaces of parallel branches. Git mode: each branch has its own worktree on its own branch
// (<task branch>--<first step>) at <task worktree>--<first step>, cut from the task worktree's HEAD when its branch
// starts, and merged into the task branch at the join. Copy mode: each branch has its own copy of the task's copy at
// <task folder>/<name>--<first step>/<name> (its start listing beside it); nothing is put back.

// branchPath is where the workspace of the branch is kept.
func branchPath(w taskmodel.Workspace, branch string) string {
	if w.Mode == taskmodel.WorkspaceModeCopy {
		name := filepath.Base(w.Path)
		return filepath.Join(filepath.Dir(w.Path), name+"--"+branch, name)
	}
	return w.Path + "--" + branch
}

// makeBranchWorkspace makes the branch's workspace from the task's workspace as it is now. One left by an earlier run
// of the fork (or by a start interrupted by a restart) is replaced: a copy always; a worktree only when it holds no
// uncommitted changes and the task branch has every commit of its branch.
func (s *Service) makeBranchWorkspace(ctx context.Context, r taskmodel.Record, branch string) (taskmodel.WorkspaceBranch, error) {
	w := r.View.Workspace
	path := branchPath(*w, branch)
	if !within(s.Source.WorktreeRoot(), path) || !within(s.Source.WorktreeRoot(), w.Path) {
		return taskmodel.WorkspaceBranch{}, errors.New("the recorded workspace is not under Loop Now's worktree folder")
	}
	made := taskmodel.WorkspaceBranch{Step: branch, Path: path, State: taskmodel.WorkspaceStateActive}
	if w.Mode == taskmodel.WorkspaceModeCopy {
		if err := copyworkspace.Remove(path); err != nil {
			return made, err
		}
		if _, err := copyworkspace.Make(w.Path, path); err != nil {
			return made, err
		}
		return made, nil
	}
	g := w.Git
	if g == nil {
		return made, errors.New("the recorded git workspace has no repository")
	}
	git, name := s.Source.git(), g.Branch+"--"+branch
	if _, err := os.Lstat(path); err == nil {
		n, err := gitworkspace.Uncommitted(ctx, git, path)
		if err != nil {
			return made, err
		}
		if n > 0 {
			return made, fmt.Errorf("the branch's worktree %s still has %d uncommitted files from an earlier run of the fork: commit them to %s and merge it into the task branch, or remove the worktree, then retry", path, n, name)
		}
		if err := gitworkspace.Remove(ctx, git, g.Repository, path, false); err != nil {
			return made, err
		}
	}
	base, err := gitworkspace.CreateBranch(ctx, git, g.Repository, w.Path, path, name)
	if err != nil {
		return made, err
	}
	made.Git = &taskmodel.WorkspaceBranchGit{Branch: name, BaseCommit: base}
	return made, nil
}

// joinResult is what the join did to the branches' workspaces: the merge result and whether the workspace was
// removed, per branch, and the report for the join step's node message.
type joinResult struct {
	merges  map[string]string
	removed map[string]bool
	report  string
}

// mergeBranches merges the fork's branches into the task branch in the worktree, in the order of the fork's edges,
// with a merge commit each. It stops at the first conflict and leaves that merge in progress for the join step; a task
// worktree with uncommitted changes is not merged into at all. Merged branches whose worktrees hold nothing
// uncommitted are removed, with their branches. Only a failure to read the state is an error; what could not be merged
// is in the report.
func (s *Service) mergeBranches(ctx context.Context, r taskmodel.Record) (joinResult, error) {
	out := joinResult{merges: map[string]string{}, removed: map[string]bool{}}
	w, g, git := r.View.Workspace, r.View.Workspace.Git, s.Source.git()
	if g == nil || !within(s.Source.WorktreeRoot(), w.Path) {
		return out, errors.New("the recorded git workspace has no repository")
	}
	type item struct{ step, branch string }
	order := []item{}
	for _, b := range r.Fork.Branches {
		if e := w.Branch(b.Step); e != nil && e.Git != nil {
			order = append(order, item{b.Step, e.Git.Branch})
		}
	}
	names := func(items []item) string {
		parts := []string{}
		for _, it := range items {
			parts = append(parts, fmt.Sprintf("%s (%s)", it.step, it.branch))
		}
		if len(parts) == 0 {
			return "none"
		}
		return strings.Join(parts, ", ")
	}
	how := " (for each, `git merge --no-ff <branch>`)"
	merged, rest := []item{}, []item{}
	report := ""
	if files, inProgress, err := gitworkspace.MergeInProgress(ctx, git, w.Path); err != nil {
		return out, err
	} else if inProgress {
		// An earlier join stopped on this conflict before the server restarted.
		for _, it := range order {
			if ok, err := gitworkspace.IsMerged(ctx, git, w.Path, it.branch); err == nil && ok {
				merged, out.merges[it.step] = append(merged, it), taskmodel.BranchMerged
			} else {
				rest, out.merges[it.step] = append(rest, it), taskmodel.BranchSkipped
			}
		}
		report = fmt.Sprintf("Join: a merge is in progress in the task's worktree %s, with conflicts in: %s. Finish it (`git status` lists what is left), commit the merge, then merge the branches that are not in the task branch yet, in this order: %s%s. Already merged: %s.", w.Path, strings.Join(files, ", "), names(rest), how, names(merged))
	} else if dirty, err := gitworkspace.Uncommitted(ctx, git, w.Path); err != nil {
		return out, err
	} else if dirty > 0 {
		for _, it := range order {
			out.merges[it.step] = taskmodel.BranchSkipped
		}
		report = fmt.Sprintf("Join: every branch has arrived, but the task's worktree %s has %d uncommitted files, so Loop Now merged no branch. Commit or remove them, then merge the branches into the task branch in this order: %s%s.", w.Path, dirty, names(order), how)
	} else {
		for i, it := range order {
			res, err := gitworkspace.Merge(ctx, git, g.Repository, w.Path, it.branch, fmt.Sprintf("Merge branch %s at the join %s", it.branch, r.Fork.Join))
			if err != nil {
				for _, later := range order[i:] {
					out.merges[later.step] = taskmodel.BranchSkipped
				}
				report = fmt.Sprintf("Join: every branch has arrived, but merging branch %s into the task branch failed: %s. Merge it and the branches after it yourself, in this order: %s%s. Already merged: %s.", names(order[i:i+1]), err.Error(), names(order[i:]), how, names(merged))
				break
			}
			if !res.Merged {
				out.merges[it.step] = taskmodel.BranchConflict
				for _, later := range order[i+1:] {
					out.merges[later.step] = taskmodel.BranchSkipped
				}
				report = fmt.Sprintf("Join: every branch has arrived. Merging branch %s into the task branch stopped on a conflict in: %s. The merge is in progress in the task's worktree %s: resolve the conflicts there (`git status` lists them), commit the merge, then merge the branches not merged yet, in this order: %s%s. Already merged: %s.", names(order[i:i+1]), strings.Join(res.Conflicts, ", "), w.Path, names(order[i+1:]), how, names(merged))
				break
			}
			merged, out.merges[it.step] = append(merged, it), taskmodel.BranchMerged
		}
		if report == "" {
			report = fmt.Sprintf("Join: every branch has arrived, and Loop Now merged them into the task branch %s in this order: %s. The task's worktree %s has the result.", g.Branch, names(merged), w.Path)
		}
	}
	// A merged branch's worktree goes when nothing in it would be lost; its branch goes with it.
	for _, it := range merged {
		e := w.Branch(it.step)
		if _, err := os.Lstat(e.Path); errors.Is(err, fs.ErrNotExist) {
			out.removed[it.step] = true
			continue
		}
		n, err := gitworkspace.Uncommitted(ctx, git, e.Path)
		if err == nil && n > 0 {
			report += fmt.Sprintf(" The worktree of branch %s still has %d uncommitted files, which were not merged; it is kept at %s.", it.step, n, e.Path)
			continue
		}
		if err == nil {
			err = gitworkspace.Remove(ctx, git, g.Repository, e.Path, false)
		}
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("kept the worktree of a merged branch", "task_id", r.View.Task.ID, "branch", it.branch, "error", err)
			}
			continue
		}
		out.removed[it.step] = true
		if err := gitworkspace.DeleteMergedBranch(ctx, git, g.Repository, it.branch, g.Branch); err != nil && s.Log != nil {
			s.Log.Info("kept the branch of a merged parallel branch", "task_id", r.View.Task.ID, "branch", it.branch, "reason", err.Error())
		}
	}
	out.report = report
	return out, nil
}

// copyJoin is the join of a copy-mode task: nothing is merged; the join step is told where the branches' copies are.
func copyJoin(r taskmodel.Record) joinResult {
	w := r.View.Workspace
	parts := []string{}
	for _, b := range r.Fork.Branches {
		if e := w.Branch(b.Step); e != nil {
			parts = append(parts, fmt.Sprintf("%s: %s", b.Step, e.Path))
		}
	}
	return joinResult{report: fmt.Sprintf("Join: every branch has arrived. Each worked in its own copy, and nothing was put back into the task's copy (%s). The branches' copies, to read what they made: %s. What they saved are the task's artifacts (`loopctl task artifact list`).", w.Path, strings.Join(parts, "; "))}
}

// branchBlock tells a step in a branch that it runs at the same time as the fork's other branches.
func branchBlock(def workflow.Definition, branch string) string {
	return fmt.Sprintf("This step is in the parallel branch that starts at step %s: the fork's other branches run at the same time, each in its own working folder, and they meet at the join step %s, which starts once every branch has submitted an edge into it.", branch, def.JoinOf(branch))
}

// branchWorkspaceBlock tells a step in a branch where and how it works.
func branchWorkspaceBlock(w taskmodel.Workspace, b taskmodel.WorkspaceBranch, join string) string {
	if b.Git == nil {
		return fmt.Sprintf("This step works in its branch's own copy: %s, made from the task's copy (%s) when the branch started. Work only in this copy. Nothing in it is put back into the task's copy or into the original folder %s: save every result the task delivers with `loopctl task artifact put`; the join step %s can read this copy. Your own folder is still at its usual place (see \"Folders and context\").", b.Path, w.Path, w.Folder, join)
	}
	task := ""
	if w.Git != nil {
		task = w.Git.Branch
	}
	return fmt.Sprintf("This step works in its branch's own git worktree: %s, on branch %s, cut from the task branch %s at %s. Work only in this worktree and stay on this branch: do not switch branches, and do not touch the task's worktree (%s), the repository's main checkout or any other worktree. Commit your work to this branch; do not push it and open no pull request: when every branch has arrived at the join step %s, Loop Now merges this branch into the task branch (uncommitted changes are not merged). Your own folder is still at its usual place (see \"Folders and context\").", b.Path, b.Git.Branch, task, shortCommit(b.Git.BaseCommit), w.Path, join)
}

// instanceWorkspace is where the task instance of the Agent works in the task's workspace: its branch's worktree or
// copy for an Agent that works in a branch (while that exists, unless all is true), else the task's worktree or copy.
func instanceWorkspace(r *taskmodel.Record, agent uuid.UUID, all bool) string {
	w := r.View.Workspace
	if w == nil {
		return ""
	}
	if branch := r.View.Workflow.AgentBranch(agent); branch != "" {
		if b := w.Branch(branch); b != nil && (all || b.State == taskmodel.WorkspaceStateActive) {
			return b.Path
		}
	}
	return w.Path
}

// cleanupBranches settles the branches' workspaces of an ended task: a worktree is removed when it holds no
// uncommitted changes (its commits stay on its branch, deleted when the task branch has them all), a copy when the task
// completed or nothing in it changed; otherwise it is kept, and what removing it would lose is added to kept.
func (s *Service) cleanupBranches(ctx context.Context, r taskmodel.Record, kept *taskmodel.WorkspaceKept) (map[string]string, error) {
	w := r.View.Workspace
	states := map[string]string{}
	for _, b := range w.Branches {
		if b.State != taskmodel.WorkspaceStateActive {
			continue
		}
		if !within(s.Source.WorktreeRoot(), b.Path) {
			return nil, errors.New("a recorded branch workspace is not under Loop Now's worktree folder; it is left alone")
		}
		if b.Git == nil {
			k, err := cleanupCopy(r.View.Task.Status, b.Path)
			if err != nil {
				return nil, err
			}
			states[b.Step] = taskmodel.WorkspaceStateRemoved
			if k != nil {
				kept.ChangedFiles += k.ChangedFiles
				states[b.Step] = taskmodel.WorkspaceStateKept
			}
			continue
		}
		git, g := s.Source.git(), w.Git
		if _, err := os.Lstat(b.Path); err == nil {
			n, err := gitworkspace.Uncommitted(ctx, git, b.Path)
			if err != nil {
				return nil, err
			}
			if n > 0 {
				kept.UncommittedFiles += n
				states[b.Step] = taskmodel.WorkspaceStateKept
				continue
			}
		}
		if err := gitworkspace.Remove(ctx, git, g.Repository, b.Path, false); err != nil {
			return nil, err
		}
		if err := gitworkspace.DeleteMergedBranch(ctx, git, g.Repository, b.Git.Branch, g.Branch); err != nil && s.Log != nil {
			s.Log.Info("kept the branch of a parallel branch", "task_id", r.View.Task.ID, "branch", b.Git.Branch, "reason", err.Error())
		}
		states[b.Step] = taskmodel.WorkspaceStateRemoved
	}
	return states, nil
}

// removeBranches removes the kept branches' workspaces for a person: exactly the recorded paths, forced, after saving
// the commits of a detached HEAD.
func (s *Service) removeBranches(ctx context.Context, w taskmodel.Workspace) error {
	for _, b := range w.Branches {
		if b.State != taskmodel.WorkspaceStateKept {
			continue
		}
		if !within(s.Source.WorktreeRoot(), b.Path) {
			return errors.New("a recorded branch workspace is not under Loop Now's worktree folder; it is not removed")
		}
		if b.Git == nil {
			if err := copyworkspace.Remove(b.Path); err != nil {
				return err
			}
			continue
		}
		git := s.Source.git()
		if _, err := os.Stat(b.Path); err == nil {
			if _, err := gitworkspace.SaveDetached(ctx, git, w.Git.Repository, b.Path, b.Git.Branch); err != nil {
				return err
			}
		}
		if err := gitworkspace.Remove(ctx, git, w.Git.Repository, b.Path, true); err != nil {
			return err
		}
	}
	return nil
}
