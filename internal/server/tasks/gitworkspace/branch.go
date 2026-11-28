package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The worktrees of a fork's parallel branches: each branch works in its own worktree on its own branch, cut from the
// task worktree's HEAD when the fork is taken, and merged back into the task branch at the join.

// CreateBranch creates the worktree of a parallel branch at path on branch, starting at the HEAD of the task worktree
// from, and returns that commit. path must not exist yet. The branch is created; one that exists already (a fork
// taken again) is reused only when every commit of it is in from's HEAD, and is then moved there.
func CreateBranch(ctx context.Context, git, repository, from, path, branch string) (string, error) {
	if !filepath.IsAbs(path) || !ValidBranch(branch) {
		return "", fmt.Errorf("invalid branch worktree request")
	}
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	head, err := Head(ctx, git, from)
	if err != nil {
		return "", err
	}
	defer lock(repository)()
	trees, err := worktrees(ctx, git, repository)
	if err != nil {
		return "", err
	}
	for _, w := range trees {
		if w.branch == branch {
			return "", fmt.Errorf("the branch %s is checked out in %s", branch, w.path)
		}
	}
	tip, err := run(ctx, git, "-C", repository, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	exists := err == nil && tip != ""
	if exists {
		merged, err := ok(ctx, git, "-C", repository, "merge-base", "--is-ancestor", tip, head)
		if err != nil {
			return "", err
		}
		if !merged {
			return "", fmt.Errorf("the branch %s has commits that are not in the task branch: merge it into the task branch or delete it, then retry", branch)
		}
		// Only the branch's own ref moves, and only from where it was checked.
		if _, err := run(ctx, git, "-C", repository, "update-ref", "refs/heads/"+branch, head, tip); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	args := []string{"-C", repository, "worktree", "add", "--quiet"}
	if exists {
		args = append(args, path, branch)
	} else {
		args = append(args, "--no-track", "-b", branch, path, head)
	}
	if _, err := run(ctx, git, args...); err != nil {
		return "", err
	}
	return head, nil
}

// Head is the commit at the HEAD of the worktree at path.
func Head(ctx context.Context, git, path string) (string, error) {
	return run(ctx, git, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
}

// MergeResult is what merging a branch into the task worktree did.
type MergeResult struct {
	// Merged reports that the branch is now in the task branch (also when it had nothing new).
	Merged bool
	// Conflicts are the files in conflict when the merge stopped on a conflict; the merge is then left in progress.
	Conflicts []string
}

// Merge merges branch into the worktree at path with a merge commit (git merge --no-ff). When it stops on a conflict,
// the merge is left in progress with the conflict markers in the files, for someone to resolve and commit; any other
// failure (uncommitted changes the merge would overwrite, no git identity) is returned as an error, with nothing
// changed.
func Merge(ctx context.Context, git, repository, path, branch, message string) (MergeResult, error) {
	defer lock(repository)()
	_, err := run(ctx, git, "-C", path, "merge", "--no-ff", "--no-edit", "-m", message, "refs/heads/"+branch)
	if err == nil {
		return MergeResult{Merged: true}, nil
	}
	files, inProgress, e := MergeInProgress(ctx, git, path)
	if e != nil {
		return MergeResult{}, errors.Join(err, e)
	}
	if !inProgress {
		return MergeResult{}, err
	}
	return MergeResult{Conflicts: files}, nil
}

// MergeInProgress reports whether a merge is in progress in the worktree at path, with the files still in conflict.
func MergeInProgress(ctx context.Context, git, path string) ([]string, bool, error) {
	if _, err := run(ctx, git, "-C", path, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); err != nil {
		return nil, false, nil
	}
	out, err := run(ctx, git, "-C", path, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, true, err
	}
	files := []string{}
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, true, nil
}

// Uncommitted counts the files of the worktree at path with uncommitted changes, untracked files included and ignored
// ones not.
func Uncommitted(ctx context.Context, git, path string) (int, error) {
	out, err := run(ctx, git, "-C", path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return 0, err
	}
	n := 0
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		n++
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the source path of a rename or copy follows as its own entry
		}
	}
	return n, nil
}

// DeleteMergedBranch deletes a branch's local ref when every commit of it is in the local branch into; a branch
// checked out in any work tree is refused. Unlike DeleteBranch it does not look at the remote: a parallel branch is
// merged into the task branch on this computer.
func DeleteMergedBranch(ctx context.Context, git, repository, branch, into string) error {
	defer lock(repository)()
	trees, err := worktrees(ctx, git, repository)
	if err != nil {
		return err
	}
	for _, w := range trees {
		if w.branch == branch {
			return fmt.Errorf("the branch %s is checked out in %s", branch, w.path)
		}
	}
	tip, err := run(ctx, git, "-C", repository, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("the branch %s does not exist", branch)
	}
	merged, err := ok(ctx, git, "-C", repository, "merge-base", "--is-ancestor", tip, "refs/heads/"+into)
	if err != nil {
		return err
	}
	if !merged {
		return fmt.Errorf("the branch %s is not merged into %s", branch, into)
	}
	_, err = run(ctx, git, "-C", repository, "update-ref", "-d", "refs/heads/"+branch, tip)
	return err
}

// IsMerged reports whether every commit of branch is in the HEAD of the worktree at path.
func IsMerged(ctx context.Context, git, path, branch string) (bool, error) {
	return ok(ctx, git, "-C", path, "merge-base", "--is-ancestor", "refs/heads/"+branch, "HEAD")
}
