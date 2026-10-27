package local

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

func isolatedGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(git, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func shallowFixture(t *testing.T, objectFormat ...string) (*Executor, agentDirs, []string, string, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	workspace := filepath.Join(root, "workspace")
	for _, p := range []string{home, work, workspace} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=file", "GIT_TERMINAL_PROMPT=0", "GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	initArgs := []string{"init", "--quiet", "--initial-branch=main"}
	if len(objectFormat) > 0 {
		initArgs = append(initArgs, "--object-format="+objectFormat[0])
	}
	isolatedGit(t, work, env, initArgs...)
	first := ""
	for _, text := range []string{"captured", "later", "latest"} {
		if err := os.WriteFile(filepath.Join(work, "value"), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		isolatedGit(t, work, env, "add", "value")
		isolatedGit(t, work, env, "commit", "--quiet", "-m", text)
		if first == "" {
			first = isolatedGit(t, work, env, "rev-parse", "HEAD")
		}
	}
	isolatedGit(t, work, env, "branch", "other", first)
	bare := filepath.Join(root, "fixture.git")
	isolatedGit(t, root, env, "clone", "--quiet", "--bare", work, bare)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	return &Executor{o: Options{GitBin: git, CloneTimeout: 5 * time.Second}}, agentDirs{workspace: workspace}, env, "file://" + bare, first
}

func cloneFixture(t *testing.T, e *Executor, d agentDirs, env []string, src store.FolderSource) error {
	t.Helper()
	r, err := os.OpenRoot(d.workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	return e.cloneRepo(context.Background(), r, d, src, env, "")
}

func TestShallowCloneBranchAndFrozenCommit(t *testing.T) {
	e, d, env, url, captured := shallowFixture(t)
	for _, tc := range []struct{ path, commit, text string }{{"branch", "", "latest"}, {"frozen", captured, "captured"}} {
		t.Run(tc.path, func(t *testing.T) {
			src := store.FolderSource{Path: tc.path, GitURL: url, GitBranch: "main", GitCommit: tc.commit}
			if err := cloneFixture(t, e, d, env, src); err != nil {
				t.Fatal(err)
			}
			repo := filepath.Join(d.workspace, tc.path)
			if got := isolatedGit(t, repo, env, "rev-parse", "--is-shallow-repository"); got != "true" {
				t.Fatalf("not shallow: %s", got)
			}
			if n := isolatedGit(t, repo, env, "rev-list", "--count", "HEAD"); n != "1" {
				t.Fatalf("unexpected history: %s", n)
			}
			b, err := os.ReadFile(filepath.Join(repo, "value"))
			if err != nil || string(b) != tc.text {
				t.Fatalf("wrong captured contents: %q %v", b, err)
			}
			if tc.commit == "" {
				if refs := isolatedGit(t, repo, env, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/"); strings.Contains(refs, "other") {
					t.Fatalf("other branch fetched: %s", refs)
				}
			} else {
				if got := isolatedGit(t, repo, env, "rev-parse", "HEAD"); got != captured {
					t.Fatalf("snapshot changed: %s", got)
				}
				if got := isolatedGit(t, repo, env, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
					t.Fatalf("not detached: %s", got)
				}
			}
		})
	}
}

func TestShallowCloneFrozenFailureNoFallback(t *testing.T) {
	for _, mode := range []string{"reject", "timeout", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			e, d, env, url, commit := shallowFixture(t)
			log := filepath.Join(t.TempDir(), "calls")
			wrapper := filepath.Join(t.TempDir(), "git")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FIXTURE_GIT_LOG\"\ncase \" $* \" in *' fetch '*) if [ \"$FIXTURE_MODE\" = timeout ]; then exec /bin/sleep 20; fi; exit 31;; esac\nexec \"$FIXTURE_REAL_GIT\" \"$@\"\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			env = append(env, "FIXTURE_GIT_LOG="+log, "FIXTURE_REAL_GIT="+e.o.GitBin, "FIXTURE_MODE="+mode)
			e.o.GitBin = wrapper
			if mode == "timeout" {
				e.o.CloneTimeout = 2 * time.Second
			}
			if mode == "invalid" {
				commit = "main"
			}
			err := cloneFixture(t, e, d, env, store.FolderSource{Path: "app", GitURL: url, GitBranch: "main", GitCommit: commit})
			f, ok := err.(*failure)
			if !ok || f.code != executor.ErrWorkspaceSyncFailed {
				t.Fatalf("expected scoped sync error: %v", err)
			}
			calls, _ := os.ReadFile(log)
			if mode == "invalid" {
				if len(calls) != 0 {
					t.Fatalf("invalid commit invoked git: %s", calls)
				}
			} else if strings.Count(string(calls), " fetch ") != 1 || !strings.Contains(string(calls), "fetch --depth=1 --no-tags origin "+commit) || strings.Contains(string(calls), " clone ") {
				t.Fatalf("unexpected retry/history fallback: %s", calls)
			}
			if mode == "timeout" && !strings.Contains(err.Error(), "超时") {
				t.Fatalf("timeout lost: %v", err)
			}
			entries, err := os.ReadDir(d.workspace)
			if err != nil || len(entries) != 0 {
				t.Fatalf("failed clone published or retained temporary directory: %v %v", entries, err)
			}
		})
	}
}

func TestShallowCloneFrozenSHA256(t *testing.T) {
	e, d, env, url, captured := shallowFixture(t, "sha256")
	if len(captured) != 64 {
		t.Fatalf("fixture not SHA256: %s", captured)
	}
	if err := cloneFixture(t, e, d, env, store.FolderSource{Path: "frozen256", GitURL: url, GitBranch: "main", GitCommit: captured}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(d.workspace, "frozen256")
	for _, tc := range []struct {
		args []string
		want string
	}{{[]string{"rev-parse", "HEAD"}, captured}, {[]string{"rev-parse", "--abbrev-ref", "HEAD"}, "HEAD"}, {[]string{"rev-parse", "--is-shallow-repository"}, "true"}, {[]string{"rev-list", "--count", "HEAD"}, "1"}} {
		if got := isolatedGit(t, repo, env, tc.args...); got != tc.want {
			t.Fatalf("SHA256 %v = %s, want %s", tc.args, got, tc.want)
		}
	}
}

func TestShallowCloneMissingCommitNoResidue(t *testing.T) {
	e, d, env, url, _ := shallowFixture(t)
	err := cloneFixture(t, e, d, env, store.FolderSource{Path: "missing", GitURL: url, GitBranch: "main", GitCommit: strings.Repeat("f", 40)})
	if f, ok := err.(*failure); !ok || f.code != executor.ErrWorkspaceSyncFailed {
		t.Fatalf("missing real object did not fail safely: %v", err)
	}
	entries, err := os.ReadDir(d.workspace)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing object left a source or temporary directory: %v %v", entries, err)
	}
}

func TestShallowCloneProcessGroupCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			e, d, env, url, commit := shallowFixture(t)
			marker := filepath.Join(t.TempDir(), "processes")
			wrapper := filepath.Join(t.TempDir(), "git")
			script := "#!/bin/sh\ncase \" $* \" in *' fetch '*) /bin/sleep 30 & child=$!; printf '%s %s\\n' \"$$\" \"$child\" > \"$FIXTURE_PROCESSES\"; wait \"$child\"; exit $?;; esac\nexec \"$FIXTURE_REAL_GIT\" \"$@\"\n"
			if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			env = append(env, "FIXTURE_PROCESSES="+marker, "FIXTURE_REAL_GIT="+e.o.GitBin)
			e.o.GitBin = wrapper
			if mode == "timeout" {
				e.o.CloneTimeout = 2 * time.Second
			}
			root, err := os.OpenRoot(d.workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- e.cloneRepo(ctx, root, d, store.FolderSource{Path: "app", GitURL: url, GitBranch: "main", GitCommit: commit}, env, "")
			}()
			var pids []string
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				b, _ := os.ReadFile(marker)
				pids = strings.Fields(string(b))
				if len(pids) == 2 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if len(pids) != 2 {
				t.Fatal("fixture never reached fetch with a live child")
			}
			parent, err := strconv.Atoi(pids[0])
			if err != nil {
				t.Fatal(err)
			}
			groupGone := false
			defer func() {
				if !groupGone {
					_ = syscall.Kill(-parent, syscall.SIGKILL) // fixture-only failure cleanup
				}
			}()
			if group, err := syscall.Getpgid(parent); err != nil || group != parent {
				t.Fatalf("Git fixture not in its own process group: %d %v", group, err)
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				if f, ok := err.(*failure); !ok || f.code != executor.ErrWorkspaceSyncFailed {
					t.Fatalf("cancellation was not a scoped failure: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Git cancellation hung")
			}
			for _, raw := range pids {
				pid, err := strconv.Atoi(raw)
				if err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(2 * time.Second)
				for {
					// Wait for actual OS reaping as well as termination: accepting
					// a zombie here would not prove the fixture has no residue.
					if syscall.Kill(pid, 0) == syscall.ESRCH {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("fixture process %d still present", pid)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := syscall.Kill(-parent, 0); err != syscall.ESRCH {
				t.Fatalf("fixture process group still present: %v", err)
			}
			groupGone = true
			if entries, err := os.ReadDir(d.workspace); err != nil || len(entries) != 0 {
				t.Fatalf("cancelled clone retained state: %v %v", entries, err)
			}
		})
	}
}
