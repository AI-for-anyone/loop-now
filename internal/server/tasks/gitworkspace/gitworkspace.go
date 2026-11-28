// Package gitworkspace creates, inspects and removes the git worktree of a task. It runs the person's own git, as the
// person (their environment and HOME), never reads a credential, and only ever touches the one worktree it is given:
// it never prunes, resets, cleans, checks out or pushes anything of the person's.
package gitworkspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"loop-now/internal/procgroup"
)

// Timeout bounds one git command; a fetch over a slow network is the longest.
var Timeout = 5 * time.Minute

// observe, when set (by tests), sees the arguments of every git command before it runs.
var observe func(args []string)

// baseFile is kept in the worktree's own administrative folder (removed with the worktree) and records the base the
// worktree was created from, so a preparation that is run again adopts the worktree with the same result.
const baseFile = "loop-now-base"

// Spec says which worktree to create.
type Spec struct {
	// Repository is the root of the repository's main work tree (see Resolve).
	Repository string
	// Path is the folder of the new worktree; it must not exist yet unless it is this worktree already.
	Path   string
	Branch string
	// BaseBranch is empty for the remote's default branch, or the current branch without a remote.
	BaseBranch string
	// BranchGiven allows Branch to exist already (when no work tree has it checked out): it is then used as it is.
	BranchGiven bool
}

// Result is the base a worktree was created from.
type Result struct {
	BaseBranch string
	BaseCommit string
}

// Status is what removing a worktree would lose.
type Status struct {
	// UncommittedFiles counts files with uncommitted changes, untracked files included and ignored ones not.
	UncommittedFiles int
	// UnpushedCommits counts commits of the worktree's HEAD that are neither in the base on the remote nor on the
	// branch's own remote ref (without a remote: not in the local base).
	UnpushedCommits int
	// Merged reports that every commit of HEAD is in the base (on the remote when there is one).
	Merged bool
	// Missing reports that the worktree folder is gone.
	Missing bool
}

// Error is a git command that failed; Error() carries git's own message.
type Error struct {
	Args   []string
	Output string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Output)
	if msg == "" {
		msg = e.Err.Error()
	}
	return "git " + strings.Join(subcommand(e.Args), " ") + ": " + msg
}
func (e *Error) Unwrap() error { return e.Err }

// subcommand drops "-C <dir>" so a message names what git was asked to do, not where.
func subcommand(args []string) []string {
	if len(args) >= 2 && args[0] == "-C" {
		return args[2:]
	}
	return args
}

func run(ctx context.Context, git string, args ...string) (string, error) {
	if observe != nil {
		observe(args)
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A fetch starts helpers (ssh, credential helpers): a timeout ends the whole group, not only git.
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = 10 * time.Second
	if err := procgroup.Run(cmd); err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("timed out after %s: %w", Timeout, ctx.Err())
		}
		return "", &Error{Args: args, Output: stderr.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// ok runs a git command whose exit status is its answer (show-ref --verify, merge-base --is-ancestor).
func ok(ctx context.Context, git string, args ...string) (bool, error) {
	_, err := run(ctx, git, args...)
	var exit *exec.ExitError
	if e, isGit := err.(*Error); isGit && errors.As(e.Err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

// locks serializes the changes made to one repository, so tasks starting at the same moment do not trip over git's
// own locks.
var locks sync.Map

func lock(repository string) func() {
	m, _ := locks.LoadOrStore(repository, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Resolve returns the root of the main work tree of the repository path lies in, with symlinks resolved. It accepts a
// subfolder, the main work tree itself, or any linked worktree of the repository.
func Resolve(ctx context.Context, git, path string) (string, error) {
	dir, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	inside, err := run(ctx, git, "-C", dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return "", err
	}
	if inside != "true" {
		return "", fmt.Errorf("%s is not inside a git work tree", path)
	}
	trees, err := worktrees(ctx, git, dir)
	if err != nil {
		return "", err
	}
	if len(trees) == 0 || trees[0].bare {
		return "", fmt.Errorf("the repository of %s has no main work tree", path)
	}
	return filepath.EvalSymlinks(trees[0].path)
}

type worktree struct {
	path, branch string
	bare         bool
}

// worktrees lists the work trees of the repository dir is in; the first is the main one.
func worktrees(ctx context.Context, git, dir string) ([]worktree, error) {
	out, err := run(ctx, git, "-C", dir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var list []worktree
	for _, line := range strings.Split(out, "\x00") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, worktree{path: strings.TrimPrefix(line, "worktree ")})
		case len(list) == 0:
		case strings.HasPrefix(line, "branch "):
			list[len(list)-1].branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			list[len(list)-1].bare = true
		}
	}
	return list, nil
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	return ea == nil && eb == nil && ra == rb
}

// BranchName is the generated branch of a task: task/<number>-<slug>, where the slug keeps lower-case ASCII letters
// and digits of the title, turns every other run into one "-", and is at most 40 characters; task/<number> when the
// title has no ASCII letter or digit.
func BranchName(number int, title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	slug := b.String()
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return "task/" + strconv.Itoa(number)
	}
	return "task/" + strconv.Itoa(number) + "-" + slug
}

// ValidBranch applies the rules of `git check-ref-format --branch` without calling git; HEAD is refused too.
func ValidBranch(name string) bool {
	if name == "" || name == "@" || name == "HEAD" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.Contains(name, "..") ||
		strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// remote is the remote a repository's base comes from: origin when there is one, else the first; "" without one.
func remote(ctx context.Context, git, repository string) (string, error) {
	out, err := run(ctx, git, "-C", repository, "remote")
	if err != nil {
		return "", err
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return "", nil
	}
	if slices.Contains(names, "origin") {
		return "origin", nil
	}
	slices.Sort(names)
	return names[0], nil
}

func refExists(ctx context.Context, git, dir, ref string) (bool, error) {
	return ok(ctx, git, "-C", dir, "show-ref", "--verify", "--quiet", ref)
}

// base resolves the base branch and its commit: from the freshly fetched remote when there is one, else locally.
func base(ctx context.Context, git, repository, branch string) (Result, error) {
	r, err := remote(ctx, git, repository)
	if err != nil {
		return Result{}, err
	}
	if r == "" {
		if branch == "" {
			current, e := run(ctx, git, "-C", repository, "symbolic-ref", "--quiet", "--short", "HEAD")
			if e != nil {
				return Result{}, fmt.Errorf("the repository has no remote and its checkout is not on a branch; name a base branch")
			}
			branch = current
		}
		commit, e := run(ctx, git, "-C", repository, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
		if e != nil {
			return Result{}, fmt.Errorf("the base branch %s does not exist", branch)
		}
		return Result{BaseBranch: branch, BaseCommit: commit}, nil
	}
	if _, err := run(ctx, git, "-C", repository, "fetch", "--quiet", "--no-tags", r); err != nil {
		return Result{}, err
	}
	if branch == "" {
		head, e := run(ctx, git, "-C", repository, "symbolic-ref", "--quiet", "refs/remotes/"+r+"/HEAD")
		if e == nil {
			branch = strings.TrimPrefix(head, "refs/remotes/"+r+"/")
		} else {
			out, e := run(ctx, git, "-C", repository, "ls-remote", "--symref", r, "HEAD")
			if e != nil {
				return Result{}, e
			}
			for _, line := range strings.Split(out, "\n") {
				if rest, found := strings.CutPrefix(line, "ref: refs/heads/"); found {
					branch, _, _ = strings.Cut(rest, "\t")
				}
			}
			if branch == "" {
				return Result{}, fmt.Errorf("the remote %s has no default branch; name a base branch", r)
			}
		}
	}
	commit, err := run(ctx, git, "-C", repository, "rev-parse", "--verify", "--quiet", "refs/remotes/"+r+"/"+branch+"^{commit}")
	if err != nil {
		return Result{}, fmt.Errorf("the base branch %s is not on the remote %s", branch, r)
	}
	return Result{BaseBranch: branch, BaseCommit: commit}, nil
}

// Create creates the worktree at spec.Path on spec.Branch, starting from the base. When spec.Path is already a work tree
// of the repository on spec.Branch (a preparation run again after a restart), it returns the base recorded when it was
// created, without touching it.
func Create(ctx context.Context, git string, spec Spec) (Result, error) {
	if !filepath.IsAbs(spec.Path) || !ValidBranch(spec.Branch) || spec.BaseBranch != "" && !ValidBranch(spec.BaseBranch) {
		return Result{}, fmt.Errorf("invalid worktree request")
	}
	defer lock(spec.Repository)()
	trees, err := worktrees(ctx, git, spec.Repository)
	if err != nil {
		return Result{}, err
	}
	if _, err := os.Lstat(spec.Path); err == nil {
		for _, w := range trees {
			if samePath(w.path, spec.Path) && w.branch == spec.Branch {
				return recorded(ctx, git, spec.Path)
			}
		}
		return Result{}, fmt.Errorf("%s already exists and is not this task's worktree", spec.Path)
	}
	for _, w := range trees {
		if w.branch == spec.Branch {
			return Result{}, fmt.Errorf("the branch %s is checked out in %s", spec.Branch, w.path)
		}
	}
	exists, err := refExists(ctx, git, spec.Repository, "refs/heads/"+spec.Branch)
	if err != nil {
		return Result{}, err
	}
	if exists && !spec.BranchGiven {
		return Result{}, fmt.Errorf("the branch %s already exists", spec.Branch)
	}
	res, err := base(ctx, git, spec.Repository, spec.BaseBranch)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(spec.Path), 0o700); err != nil {
		return Result{}, err
	}
	args := []string{"-C", spec.Repository, "worktree", "add", "--quiet"}
	if exists {
		args = append(args, spec.Path, spec.Branch)
	} else {
		args = append(args, "--no-track", "-b", spec.Branch, spec.Path, res.BaseCommit)
	}
	if _, err := run(ctx, git, args...); err != nil {
		return Result{}, err
	}
	admin, err := adminDir(ctx, git, spec.Path)
	if err == nil {
		err = os.WriteFile(filepath.Join(admin, baseFile), []byte(res.BaseBranch+"\n"+res.BaseCommit+"\n"), 0o600)
	}
	if err != nil {
		_, _ = run(ctx, git, "-C", spec.Repository, "worktree", "remove", "--force", spec.Path)
		return Result{}, err
	}
	return res, nil
}

// adminDir is the worktree's own folder inside the repository's git directory.
func adminDir(ctx context.Context, git, path string) (string, error) {
	return run(ctx, git, "-C", path, "rev-parse", "--path-format=absolute", "--git-dir")
}

func recorded(ctx context.Context, git, path string) (Result, error) {
	admin, err := adminDir(ctx, git, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(filepath.Join(admin, baseFile))
	if err != nil {
		return Result{}, fmt.Errorf("%s is a worktree Loop Now did not finish creating: %w", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		return Result{}, fmt.Errorf("the base recorded for %s cannot be read", path)
	}
	return Result{BaseBranch: lines[0], BaseCommit: lines[1]}, nil
}

// Inspect says what removing the worktree at path would lose. With a remote it fetches first; when the fetch fails it
// counts with what is known locally, which can only make it keep more.
func Inspect(ctx context.Context, git, path, baseBranch string) (Status, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Status{Missing: true}, nil
	} else if err != nil {
		return Status{}, err
	}
	if baseBranch == "" {
		// A task cancelled before its record said which base it took: the worktree knows.
		if res, err := recorded(ctx, git, path); err == nil {
			baseBranch = res.BaseBranch
		}
	}
	var st Status
	var err error
	if st.UncommittedFiles, err = Uncommitted(ctx, git, path); err != nil {
		return Status{}, err
	}
	r, err := remote(ctx, git, path)
	if err != nil {
		return Status{}, err
	}
	var baseRef string
	var own []string
	if r == "" {
		baseRef = "refs/heads/" + baseBranch
	} else {
		_, _ = run(ctx, git, "-C", path, "fetch", "--quiet", "--no-tags", r)
		baseRef = "refs/remotes/" + r + "/" + baseBranch
		if branch, e := run(ctx, git, "-C", path, "symbolic-ref", "--quiet", "--short", "HEAD"); e == nil {
			own = append(own, "refs/remotes/"+r+"/"+branch)
			if up, e := run(ctx, git, "-C", path, "rev-parse", "--symbolic-full-name", "--verify", "--quiet", "@{upstream}"); e == nil && up != "" {
				own = append(own, up)
			}
		}
	}
	notIn := []string{}
	if baseBranch == "" || !ValidBranch(baseBranch) {
		baseRef = "" // no known base: every commit counts as existing only here
	} else if found, err := refExists(ctx, git, path, baseRef); err != nil {
		return Status{}, err
	} else if found {
		notIn = append(notIn, baseRef)
	}
	merged, err := count(ctx, git, path, notIn)
	if err != nil {
		return Status{}, err
	}
	st.Merged = merged == 0 && len(notIn) == 1
	for _, ref := range own {
		if found, err := refExists(ctx, git, path, ref); err != nil {
			return Status{}, err
		} else if found && !slices.Contains(notIn, ref) {
			notIn = append(notIn, ref)
		}
	}
	if st.UnpushedCommits, err = count(ctx, git, path, notIn); err != nil {
		return Status{}, err
	}
	return st, nil
}

// count counts the commits of HEAD that are in none of refs.
func count(ctx context.Context, git, path string, refs []string) (int, error) {
	args := append([]string{"-C", path, "rev-list", "--count", "HEAD"}, "--not")
	args = append(args, refs...)
	out, err := run(ctx, git, args...)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// SaveDetached keeps the commits of a worktree whose HEAD is detached and holds commits that are not on its task
// branch: it creates the new local branch <branch>-detached-<short sha> (the first free name) at HEAD, only creating a
// ref. It returns the branch created, or "" when nothing needed saving.
func SaveDetached(ctx context.Context, git, repository, path, branch string) (string, error) {
	if _, err := run(ctx, git, "-C", path, "symbolic-ref", "--quiet", "HEAD"); err == nil {
		return "", nil // on a branch: its commits are on that branch
	}
	head, err := run(ctx, git, "-C", path, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", nil // no commit at all
	}
	refs := []string{}
	if found, err := refExists(ctx, git, path, "refs/heads/"+branch); err != nil {
		return "", err
	} else if found {
		refs = append(refs, "refs/heads/"+branch)
	}
	extra, err := count(ctx, git, path, refs)
	if err != nil || extra == 0 {
		return "", err
	}
	defer lock(repository)()
	name := branch + "-detached-" + head[:min(len(head), 7)]
	for i := 2; ; i++ {
		found, err := refExists(ctx, git, repository, "refs/heads/"+name)
		if err != nil {
			return "", err
		}
		if !found {
			break
		}
		name = fmt.Sprintf("%s-detached-%s-%d", branch, head[:min(len(head), 7)], i)
	}
	// An empty old value makes git refuse when the ref appeared in the meantime.
	if _, err := run(ctx, git, "-C", repository, "update-ref", "refs/heads/"+name, head, ""); err != nil {
		return "", err
	}
	return name, nil
}

// Remove removes the one worktree at path with `git worktree remove`; force also removes uncommitted changes. A
// worktree that is already gone (its folder and git's record of it) is not an error.
func Remove(ctx context.Context, git, repository, path string, force bool) error {
	defer lock(repository)()
	trees, err := worktrees(ctx, git, repository)
	if err != nil {
		return err
	}
	listed := false
	for _, w := range trees[min(1, len(trees)):] {
		if samePath(w.path, path) || w.path == path {
			listed = true
		}
	}
	if !listed {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%s is not a worktree of %s", path, repository)
		}
		return nil
	}
	args := []string{"-C", repository, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	_, err = run(ctx, git, append(args, path)...)
	return err
}

// DeleteBranch deletes a local branch only when every commit of it is in the base (on the remote when the repository
// has one); the deletion is conditional on the branch still pointing where it was checked, and a branch checked out in
// any work tree is refused.
func DeleteBranch(ctx context.Context, git, repository, branch, baseBranch string) error {
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
	r, err := remote(ctx, git, repository)
	if err != nil {
		return err
	}
	baseRef := "refs/heads/" + baseBranch
	if r != "" {
		baseRef = "refs/remotes/" + r + "/" + baseBranch
	}
	merged, err := ok(ctx, git, "-C", repository, "merge-base", "--is-ancestor", tip, baseRef)
	if err != nil {
		return err
	}
	if !merged {
		return fmt.Errorf("the branch %s is not merged into %s", branch, baseRef)
	}
	_, err = run(ctx, git, "-C", repository, "update-ref", "-d", "refs/heads/"+branch, tip)
	return err
}
