package gitworkspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// taskWorktree makes the task's worktree on task/1-t from main, as Create does.
func taskWorktree(t *testing.T, f fixture) string {
	t.Helper()
	path := filepath.Join(f.root, "worktrees", "t", "repo")
	if _, err := Create(context.Background(), f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-t", BaseBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func commit(t *testing.T, dir, file, content string) string {
	t.Helper()
	write(t, filepath.Join(dir, file), content)
	sh(t, dir, "add", ".")
	sh(t, dir, "commit", "-q", "-m", file)
	return sh(t, dir, "rev-parse", "HEAD")
}

// Each branch gets its own worktree on its own branch cut from the task worktree's HEAD; merging them back in order
// gives the task branch both changes.
func TestBranchWorktreesMergeBackCleanly(t *testing.T) {
	ctx := context.Background()
	f := newRepo(t, false)
	task := taskWorktree(t, f)
	head := commit(t, task, "plan.md", "plan\n")
	a, b := task+"--server", task+"--web"
	base, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--server")
	if err != nil || base != head {
		t.Fatalf("CreateBranch = %q, %v; want %s", base, err, head)
	}
	if _, err := CreateBranch(ctx, f.git, f.repo, task, b, "task/1-t--web"); err != nil {
		t.Fatal(err)
	}
	if got := sh(t, a, "rev-parse", "--abbrev-ref", "HEAD"); got != "task/1-t--server" {
		t.Fatalf("branch worktree is on %s", got)
	}
	commit(t, a, "server.go", "package server\n")
	commit(t, b, "web.ts", "export {}\n")
	for _, branch := range []string{"task/1-t--server", "task/1-t--web"} {
		res, err := Merge(ctx, f.git, f.repo, task, branch, "Merge "+branch)
		if err != nil || !res.Merged || len(res.Conflicts) != 0 {
			t.Fatalf("Merge %s = %+v, %v", branch, res, err)
		}
	}
	for _, file := range []string{"server.go", "web.ts", "plan.md"} {
		if _, err := os.Stat(filepath.Join(task, file)); err != nil {
			t.Fatalf("%s is not in the task worktree after the merge", file)
		}
	}
	// Merged: the worktrees go and the branches are deleted, the task branch keeps every commit.
	for _, p := range []struct{ path, branch string }{{a, "task/1-t--server"}, {b, "task/1-t--web"}} {
		if n, err := Uncommitted(ctx, f.git, p.path); err != nil || n != 0 {
			t.Fatalf("Uncommitted = %d, %v", n, err)
		}
		if err := Remove(ctx, f.git, f.repo, p.path, false); err != nil {
			t.Fatal(err)
		}
		if err := DeleteMergedBranch(ctx, f.git, f.repo, p.branch, "task/1-t"); err != nil {
			t.Fatal(err)
		}
		if found, _ := refExists(ctx, f.git, f.repo, "refs/heads/"+p.branch); found {
			t.Fatalf("%s was not deleted", p.branch)
		}
	}
	// A merge with nothing new merges nothing and is not an error.
	if _, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--server"); err != nil {
		t.Fatal(err)
	}
	if res, err := Merge(ctx, f.git, f.repo, task, "task/1-t--server", "Merge"); err != nil || !res.Merged {
		t.Fatalf("empty merge = %+v, %v", res, err)
	}
}

// Two branches changing the same line: the second merge stops on the conflict and leaves it in progress in the task
// worktree for the join step; MergeInProgress reports it again later.
func TestBranchMergeConflictIsLeftInProgress(t *testing.T) {
	ctx := context.Background()
	f := newRepo(t, false)
	task := taskWorktree(t, f)
	a, b := task+"--a", task+"--b"
	for _, p := range []struct{ path, branch, text string }{{a, "task/1-t--a", "from a\n"}, {b, "task/1-t--b", "from b\n"}} {
		if _, err := CreateBranch(ctx, f.git, f.repo, task, p.path, p.branch); err != nil {
			t.Fatal(err)
		}
		commit(t, p.path, "README", p.text)
	}
	if res, err := Merge(ctx, f.git, f.repo, task, "task/1-t--a", "Merge a"); err != nil || !res.Merged {
		t.Fatalf("first merge = %+v, %v", res, err)
	}
	res, err := Merge(ctx, f.git, f.repo, task, "task/1-t--b", "Merge b")
	if err != nil || res.Merged || !slices.Equal(res.Conflicts, []string{"README"}) {
		t.Fatalf("conflicting merge = %+v, %v", res, err)
	}
	files, inProgress, err := MergeInProgress(ctx, f.git, task)
	if err != nil || !inProgress || !slices.Equal(files, []string{"README"}) {
		t.Fatalf("MergeInProgress = %v, %v, %v", files, inProgress, err)
	}
	// The join step resolves it: afterwards no merge is in progress.
	write(t, filepath.Join(task, "README"), "from a and b\n")
	sh(t, task, "add", "README")
	sh(t, task, "commit", "-q", "--no-edit")
	if _, inProgress, err := MergeInProgress(ctx, f.git, task); err != nil || inProgress {
		t.Fatalf("merge still in progress after the commit: %v", err)
	}
	// Uncommitted changes in the task worktree: merging is refused without touching them.
	if _, err := CreateBranch(ctx, f.git, f.repo, task, task+"--c", "task/1-t--c"); err != nil {
		t.Fatal(err)
	}
	commit(t, task+"--c", "README", "from c\n")
	write(t, filepath.Join(task, "README"), "local edit\n")
	if res, err := Merge(ctx, f.git, f.repo, task, "task/1-t--c", "Merge c"); err == nil || res.Merged {
		t.Fatalf("merge over uncommitted changes = %+v, %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(task, "README")); string(got) != "local edit\n" {
		t.Fatalf("the uncommitted change was touched: %q", got)
	}
}

// A fork taken again reuses a branch only when its commits are all in the task branch; it then starts from the task
// worktree's HEAD again. A branch with commits the task branch lacks is refused, and so is an unmerged branch's delete.
func TestBranchTakenAgain(t *testing.T) {
	ctx := context.Background()
	f := newRepo(t, false)
	task := taskWorktree(t, f)
	a := task + "--a"
	if _, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--a"); err != nil {
		t.Fatal(err)
	}
	commit(t, a, "a.txt", "a\n")
	if err := Remove(ctx, f.git, f.repo, a, false); err != nil {
		t.Fatal(err)
	}
	if err := DeleteMergedBranch(ctx, f.git, f.repo, "task/1-t--a", "task/1-t"); err == nil {
		t.Fatal("an unmerged branch was deleted")
	}
	if _, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--a"); err == nil {
		t.Fatal("a branch with commits the task branch lacks was reused")
	}
	if _, err := Merge(ctx, f.git, f.repo, task, "task/1-t--a", "Merge a"); err != nil {
		t.Fatal(err)
	}
	head := commit(t, task, "later.txt", "later\n")
	base, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--a")
	if err != nil || base != head || sh(t, a, "rev-parse", "HEAD") != head {
		t.Fatalf("reused branch = %q, %v; want it at %s", base, err, head)
	}
	// The path exists already: CreateBranch does not touch it.
	if _, err := CreateBranch(ctx, f.git, f.repo, task, a, "task/1-t--a"); err == nil {
		t.Fatal("an existing path was accepted")
	}
}
