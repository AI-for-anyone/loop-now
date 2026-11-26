package gitworkspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

var (
	seenMu sync.Mutex
	seen   [][]string
)

// TestMain records every git command the package runs, and fails the run when one of them could touch what the
// person has: prune acts on the whole repository, and the others change a checkout or a remote.
func TestMain(m *testing.M) {
	observe = func(args []string) {
		seenMu.Lock()
		seen = append(seen, slices.Clone(args))
		seenMu.Unlock()
	}
	code := m.Run()
	if code == 0 {
		if bad := forbidden(seen); bad != "" {
			fmt.Fprintln(os.Stderr, "the package ran a forbidden git command:", bad)
			code = 1
		}
	}
	os.Exit(code)
}

func forbidden(all [][]string) string {
	for _, args := range all {
		sub := gitSubcommand(args)
		switch {
		case len(sub) >= 2 && sub[0] == "worktree" && sub[1] == "prune":
		case len(sub) >= 1 && slices.Contains([]string{"reset", "clean", "checkout", "switch", "push", "restore", "stash", "gc"}, sub[0]):
		default:
			continue
		}
		return strings.Join(args, " ")
	}
	return ""
}

// gitSubcommand drops the global options (-C dir, -c k=v, --git-dir=…) in front of the git subcommand.
func gitSubcommand(args []string) []string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-C" || a == "-c":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return args[i:]
		}
	}
	return nil
}

func TestForbiddenCatchesDangerousCommands(t *testing.T) {
	for _, args := range [][]string{{"-C", "/r", "worktree", "prune"}, {"-C", "/r", "reset", "--hard"}, {"clean", "-fdx"}, {"-c", "a=b", "push"}, {"checkout", "main"}} {
		if forbidden([][]string{args}) == "" {
			t.Errorf("%v should be forbidden", args)
		}
	}
	if forbidden([][]string{{"-C", "/r", "worktree", "remove", "/x"}, {"-C", "/r", "fetch", "origin"}}) != "" {
		t.Error("worktree remove and fetch are allowed")
	}
}

func gitBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	return p
}

// sh runs the test's own git (not the package's), with a fixed identity.
func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	git, root, repo, remote string
}

// newRepo makes a repository with one commit on main; with remote, also a bare "origin" holding main as its HEAD.
func newRepo(t *testing.T, remote bool) fixture {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	f := fixture{git: gitBin(t), root: real(t, t.TempDir())}
	f.repo = filepath.Join(f.root, "repo")
	sh(t, f.root, "init", "-q", "-b", "main", f.repo)
	write(t, filepath.Join(f.repo, "README"), "hello\n")
	write(t, filepath.Join(f.repo, ".gitignore"), "node_modules/\n")
	sh(t, f.repo, "add", ".")
	sh(t, f.repo, "commit", "-q", "-m", "init")
	if remote {
		f.remote = filepath.Join(f.root, "origin.git")
		sh(t, f.root, "init", "-q", "--bare", "-b", "main", f.remote)
		sh(t, f.repo, "remote", "add", "origin", f.remote)
		sh(t, f.repo, "push", "-q", "origin", "main")
		sh(t, f.repo, "remote", "set-head", "origin", "main")
	}
	return f
}

// pushFromClone adds a commit to the remote's branch through a second clone, so the person's checkout stays behind.
func (f fixture) pushFromClone(t *testing.T, branch, file string) string {
	t.Helper()
	clone := filepath.Join(f.root, "clone-"+strings.ReplaceAll(branch, "/", "-"))
	if _, err := os.Stat(clone); err != nil {
		sh(t, f.root, "clone", "-q", f.remote, clone)
	}
	sh(t, clone, "fetch", "-q", "origin")
	sh(t, clone, "checkout", "-q", "-B", branch, "origin/"+branch)
	write(t, filepath.Join(clone, file), file+"\n")
	sh(t, clone, "add", ".")
	sh(t, clone, "commit", "-q", "-m", file)
	sh(t, clone, "push", "-q", "origin", branch)
	return sh(t, clone, "rev-parse", "HEAD")
}

func (f fixture) path(name string) string {
	return filepath.Join(f.root, "work", "worktrees", name, "repo")
}

func TestResolveFindsTheMainWorkTree(t *testing.T) {
	f := newRepo(t, false)
	ctx := context.Background()
	if got, err := Resolve(ctx, f.git, f.repo); err != nil || got != f.repo {
		t.Fatalf("Resolve(repo) = %q, %v", got, err)
	}
	sub := filepath.Join(f.repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(ctx, f.git, sub); err != nil || got != f.repo {
		t.Fatalf("Resolve(subfolder) = %q, %v", got, err)
	}
	own := filepath.Join(f.root, "own-worktree")
	sh(t, f.repo, "worktree", "add", "-q", "-b", "mine", own)
	if got, err := Resolve(ctx, f.git, own); err != nil || got != f.repo {
		t.Fatalf("Resolve(the person's own worktree) = %q, %v", got, err)
	}
	link := filepath.Join(f.root, "link")
	if err := os.Symlink(f.repo, link); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(ctx, f.git, filepath.Join(link, "a")); err != nil || got != f.repo {
		t.Fatalf("Resolve(through a symlink) = %q, %v", got, err)
	}
	if _, err := Resolve(ctx, f.git, f.root); err == nil {
		t.Fatal("a folder outside any repository must not resolve")
	}
	bare := filepath.Join(f.root, "bare.git")
	sh(t, f.root, "init", "-q", "--bare", bare)
	if _, err := Resolve(ctx, f.git, bare); err == nil {
		t.Fatal("a bare repository has no work tree and must not resolve")
	}
}

func TestBranchName(t *testing.T) {
	cases := map[string]string{
		"修复登录":                    "task/12", // check-english: zh (a title with no ASCII letters)
		"Fix a/b ..c ":            "task/12-fix-a-b-c",
		"Add the API.":            "task/12-add-the-api",
		"  --Hello__World--  ":    "task/12-hello-world",
		"登录 Login 修复":             "task/12-login", // check-english: zh (non-ASCII words are dropped from the slug)
		strings.Repeat("ab ", 30): "task/12-" + strings.TrimSuffix(strings.Repeat("ab-", 14)[:40], "-"),
	}
	for title, want := range cases {
		got := BranchName(12, title)
		if got != want {
			t.Errorf("BranchName(12, %q) = %q, want %q", title, got, want)
		}
		if !ValidBranch(got) {
			t.Errorf("BranchName(12, %q) = %q is not a valid branch", title, got)
		}
	}
}

func TestValidBranchMatchesGit(t *testing.T) {
	git := gitBin(t)
	names := []string{"main", "task/12-fix", "a..b", "a b", "-x", "x.", "x.lock", "a/.b", "/a", "a/", "a//b", "a@{b", "@", "HEAD",
		"a~b", "a^b", "a:b", "a?b", "a*b", "a[b", `a\b`, "a\tb", "a\x7fb", "", "feature/日本", "a.b/c", ".a", "a/b.lock/c", "x@y"} // check-english: zh (git accepts non-ASCII branch names)
	for _, n := range names {
		err := exec.Command(git, "check-ref-format", "--branch", n).Run()
		want := err == nil && n != "HEAD" && n != "@" // git expands both to the current branch
		if got := ValidBranch(n); got != want {
			t.Errorf("ValidBranch(%q) = %v, git says %v", n, got, want)
		}
	}
}

func TestCreateWithRemoteStartsFromTheRemoteDefaultBranch(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	ahead := f.pushFromClone(t, "main", "remote-only")
	path := f.path("t1")
	res, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseBranch != "main" || res.BaseCommit != ahead {
		t.Fatalf("Create = %+v, want main at the remote's commit %s", res, ahead)
	}
	if got := sh(t, path, "rev-parse", "--abbrev-ref", "HEAD"); got != "task/1-x" {
		t.Fatalf("worktree branch = %q", got)
	}
	if _, err := os.Stat(filepath.Join(path, "remote-only")); err != nil {
		t.Fatal("the worktree should hold the remote's commit, not the stale local main")
	}
	if got := sh(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("the person's checkout moved to %q", got)
	}
	if sh(t, f.repo, "rev-parse", "main") == ahead {
		t.Fatal("the person's local main must not be updated")
	}
}

func TestCreateWithGivenBaseBranch(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	sh(t, f.repo, "push", "-q", "origin", "main:release")
	tip := f.pushFromClone(t, "release", "on-release")
	res, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t1"), Branch: "task/1-x", BaseBranch: "release"})
	if err != nil || res.BaseBranch != "release" || res.BaseCommit != tip {
		t.Fatalf("Create = %+v, %v; want release at %s", res, err, tip)
	}
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t2"), Branch: "task/2-x", BaseBranch: "nope"}); err == nil {
		t.Fatal("an unknown base branch must fail")
	}
}

func TestCreateWithoutRemoteUsesTheCurrentBranch(t *testing.T) {
	f := newRepo(t, false)
	ctx := context.Background()
	sh(t, f.repo, "checkout", "-q", "-b", "dev")
	write(t, filepath.Join(f.repo, "dev"), "dev\n")
	sh(t, f.repo, "add", ".")
	sh(t, f.repo, "commit", "-q", "-m", "dev")
	dev := sh(t, f.repo, "rev-parse", "HEAD")
	res, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t1"), Branch: "task/1-x"})
	if err != nil || res.BaseBranch != "dev" || res.BaseCommit != dev {
		t.Fatalf("Create = %+v, %v; want dev at %s", res, err, dev)
	}
	res, err = Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t2"), Branch: "task/2-x", BaseBranch: "main"})
	if err != nil || res.BaseBranch != "main" || res.BaseCommit != sh(t, f.repo, "rev-parse", "main") {
		t.Fatalf("Create with base main = %+v, %v", res, err)
	}
}

func TestCreateFetchFailureIsAnError(t *testing.T) {
	f := newRepo(t, true)
	if err := os.RemoveAll(f.remote); err != nil {
		t.Fatal(err)
	}
	_, err := Create(context.Background(), f.git, Spec{Repository: f.repo, Path: f.path("t1"), Branch: "task/1-x"})
	if err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("Create with a missing remote = %v, want git's fetch failure", err)
	}
	if _, e := os.Stat(f.path("t1")); e == nil {
		t.Fatal("no worktree should be left after a failure")
	}
}

func TestCreateBranchRules(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	sh(t, f.repo, "branch", "task/1-x")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t1"), Branch: "task/1-x"}); err == nil {
		t.Fatal("a generated branch that exists must be an error")
	}
	// A branch the Manager names may exist: the task continues it as it is.
	sh(t, f.repo, "branch", "-f", "carry-on", "main")
	other := f.pushFromClone(t, "main", "later")
	res, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t2"), Branch: "carry-on", BranchGiven: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.BaseCommit != other {
		t.Fatalf("base commit = %s, want the remote base %s", res.BaseCommit, other)
	}
	if got, want := sh(t, f.path("t2"), "rev-parse", "HEAD"), sh(t, f.repo, "rev-parse", "carry-on"); got != want {
		t.Fatalf("a given existing branch must be checked out as it is: %s / %s", got, want)
	}
	// ...but not while a work tree has it checked out.
	sh(t, f.repo, "worktree", "add", "-q", "-b", "busy", filepath.Join(f.root, "busy"))
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path("t3"), Branch: "busy", BranchGiven: true}); err == nil {
		t.Fatal("a branch checked out in another work tree must be an error")
	}
}

func TestCreateAdoptsItsOwnWorktree(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	spec := Spec{Repository: f.repo, Path: f.path("t1"), Branch: "task/1-x"}
	first, err := Create(ctx, f.git, spec)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(spec.Path, "work"), "in progress\n")
	f.pushFromClone(t, "main", "moved-on")
	again, err := Create(ctx, f.git, spec)
	if err != nil || again != first {
		t.Fatalf("second Create = %+v, %v; want the first result %+v", again, err, first)
	}
	if _, err := os.Stat(filepath.Join(spec.Path, "work")); err != nil {
		t.Fatal("adopting must not touch the worktree")
	}
	if branches := sh(t, f.repo, "branch", "--list", "task/*"); branches != "+ task/1-x" && branches != "* task/1-x" {
		t.Fatalf("branches after adopting = %q", branches)
	}
	// A folder at the path that is not this worktree is never adopted.
	stray := f.path("t2")
	write(t, filepath.Join(stray, "x"), "x")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: stray, Branch: "task/2-x"}); err == nil {
		t.Fatal("an existing folder that is not this task's worktree must be an error")
	}
}

func TestCreateTwoAtOnce(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Create(ctx, f.git, Spec{Repository: f.repo, Path: f.path(fmt.Sprint("t", i)), Branch: fmt.Sprint("task/", i)})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Create %d: %v", i, err)
		}
	}
}

func TestInspect(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	check := func(want Status) {
		t.Helper()
		got, err := Inspect(ctx, f.git, path, "main")
		if err != nil || got != want {
			t.Fatalf("Inspect = %+v, %v; want %+v", got, err, want)
		}
	}
	check(Status{Merged: true})
	write(t, filepath.Join(path, "node_modules", "x", "index.js"), "ignored")
	check(Status{Merged: true})
	write(t, filepath.Join(path, "README"), "changed\n")
	write(t, filepath.Join(path, "new.txt"), "new\n")
	write(t, filepath.Join(path, "dir", "a"), "a")
	write(t, filepath.Join(path, "dir", "b"), "b")
	check(Status{UncommittedFiles: 4, Merged: true})
	sh(t, path, "add", ".")
	sh(t, path, "commit", "-q", "-m", "work")
	check(Status{UnpushedCommits: 1})
	sh(t, path, "mv", "new.txt", "renamed.txt")
	check(Status{UncommittedFiles: 1, UnpushedCommits: 1})
	sh(t, path, "commit", "-q", "-m", "rename")
	check(Status{UnpushedCommits: 2})
	sh(t, path, "push", "-q", "origin", "task/1-x")
	check(Status{})
	// Merged on the remote: the person's local main stays behind, the remote's main counts.
	sh(t, path, "push", "-q", "origin", "task/1-x:main")
	check(Status{Merged: true})
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	check(Status{Missing: true})
}

func TestInspectWithoutRemote(t *testing.T) {
	f := newRepo(t, false)
	ctx := context.Background()
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(path, "a"), "a")
	sh(t, path, "add", ".")
	sh(t, path, "commit", "-q", "-m", "a")
	if got, err := Inspect(ctx, f.git, path, "main"); err != nil || got != (Status{UnpushedCommits: 1}) {
		t.Fatalf("Inspect = %+v, %v", got, err)
	}
	sh(t, f.repo, "merge", "-q", "--ff-only", "task/1-x")
	if got, err := Inspect(ctx, f.git, path, "main"); err != nil || got != (Status{Merged: true}) {
		t.Fatalf("Inspect after merging into the local base = %+v, %v", got, err)
	}
}

func TestInspectDetachedHeadCountsItsCommits(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	sh(t, path, "checkout", "-q", "--detach")
	write(t, filepath.Join(path, "a"), "a")
	sh(t, path, "add", ".")
	sh(t, path, "commit", "-q", "-m", "a")
	if got, err := Inspect(ctx, f.git, path, "main"); err != nil || got.UnpushedCommits != 1 {
		t.Fatalf("Inspect = %+v, %v; commits on a detached HEAD exist only here", got, err)
	}
}

func TestRemoveAndDeleteBranch(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(path, "dirty"), "x")
	if err := Remove(ctx, f.git, f.repo, path, false); err == nil {
		t.Fatal("a worktree with untracked files must not be removed without force")
	}
	if err := Remove(ctx, f.git, f.repo, path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the worktree folder should be gone")
	}
	if strings.Contains(sh(t, f.repo, "worktree", "list"), path) {
		t.Fatal("git should not list the removed worktree")
	}
	// Removing again, or a worktree whose folder is gone, is not an error.
	if err := Remove(ctx, f.git, f.repo, path, false); err != nil {
		t.Fatal(err)
	}
	// Unmerged branches are refused; merged ones go.
	other := filepath.Join(f.root, "elsewhere")
	sh(t, f.repo, "worktree", "add", "-q", "-b", "unmerged", other)
	write(t, filepath.Join(other, "u"), "u")
	sh(t, other, "add", ".")
	sh(t, other, "commit", "-q", "-m", "u")
	sh(t, f.repo, "worktree", "remove", other)
	if err := DeleteBranch(ctx, f.git, f.repo, "unmerged", "main"); err == nil {
		t.Fatal("an unmerged branch must not be deleted")
	}
	if err := DeleteBranch(ctx, f.git, f.repo, "task/1-x", "main"); err != nil {
		t.Fatal(err)
	}
	if sh(t, f.repo, "branch", "--list", "task/1-x") != "" {
		t.Fatal("the merged branch should be gone")
	}
}

func TestRemoveLeavesOtherWorktreesAlone(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	mine := filepath.Join(f.root, "person-worktree")
	sh(t, f.repo, "worktree", "add", "-q", "-b", "person", mine)
	if err := os.RemoveAll(mine); err != nil { // its folder is missing: prune would drop it
		t.Fatal(err)
	}
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, f.git, f.repo, path, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sh(t, f.repo, "worktree", "list", "--porcelain"), "worktree "+mine) {
		t.Fatal("the person's worktree record must be kept")
	}
}

func TestInspectCountsChangesInsideASubmodule(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	lib := filepath.Join(f.root, "lib")
	sh(t, f.root, "init", "-q", "-b", "main", lib)
	write(t, filepath.Join(lib, "x"), "x")
	sh(t, lib, "add", ".")
	sh(t, lib, "commit", "-q", "-m", "lib")
	sh(t, f.repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", lib, "lib")
	sh(t, f.repo, "commit", "-q", "-m", "submodule")
	sh(t, f.repo, "push", "-q", "origin", "main")
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	sh(t, path, "-c", "protocol.file.allow=always", "submodule", "update", "-q", "--init")
	if got, err := Inspect(ctx, f.git, path, "main"); err != nil || got != (Status{Merged: true}) {
		t.Fatalf("Inspect with a clean submodule = %+v, %v", got, err)
	}
	write(t, filepath.Join(path, "lib", "x"), "changed")
	if got, err := Inspect(ctx, f.git, path, "main"); err != nil || got.UncommittedFiles != 1 {
		t.Fatalf("Inspect with a changed submodule = %+v, %v; the change exists only here", got, err)
	}
}

func TestSaveDetached(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	path := f.path("t1")
	if _, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"}); err != nil {
		t.Fatal(err)
	}
	if name, err := SaveDetached(ctx, f.git, f.repo, path, "task/1-x"); err != nil || name != "" {
		t.Fatalf("on its branch: %q %v", name, err)
	}
	sh(t, path, "checkout", "-q", "--detach")
	if name, err := SaveDetached(ctx, f.git, f.repo, path, "task/1-x"); err != nil || name != "" {
		t.Fatalf("detached without commits of its own: %q %v", name, err)
	}
	write(t, filepath.Join(path, "d"), "d")
	sh(t, path, "add", ".")
	sh(t, path, "commit", "-q", "-m", "d")
	head := sh(t, path, "rev-parse", "HEAD")
	sh(t, f.repo, "branch", "task/1-x-detached-"+head[:7], "main") // the first name is taken
	name, err := SaveDetached(ctx, f.git, f.repo, path, "task/1-x")
	if err != nil || name != "task/1-x-detached-"+head[:7]+"-2" || sh(t, f.repo, "rev-parse", name) != head {
		t.Fatalf("saved as %q, %v", name, err)
	}
	if sh(t, f.repo, "rev-parse", "task/1-x-detached-"+head[:7]) == head {
		t.Fatal("an existing branch must not be moved")
	}
}

func TestInspectWithoutAKnownBase(t *testing.T) {
	f := newRepo(t, true)
	ctx := context.Background()
	path := f.path("t1")
	res, err := Create(ctx, f.git, Spec{Repository: f.repo, Path: path, Branch: "task/1-x"})
	if err != nil {
		t.Fatal(err)
	}
	// The base the worktree recorded is used when the caller does not know it.
	if got, err := Inspect(ctx, f.git, path, ""); err != nil || got != (Status{Merged: true}) || res.BaseBranch != "main" {
		t.Fatalf("Inspect = %+v, %v", got, err)
	}
}
