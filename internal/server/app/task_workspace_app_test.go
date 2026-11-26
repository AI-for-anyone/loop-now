package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks/gitworkspace"
	"loop-now/internal/server/workflow"
)

// gitT runs the test's own git with a fixed identity.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// workspaceWorld is a running server with a Manager whose project lists a folder holding a repository with a bare
// origin.
type workspaceWorld struct {
	*server
	w                    *world
	dump                 string // where the fake Pi writes how it was started
	agent                apitypes.Agent
	folder, repo, remote string
}

func newWorkspaceWorld(t *testing.T) workspaceWorld {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	w := newWorld(t)
	dump := filepath.Join(t.TempDir(), "dump.json")
	t.Setenv("FAKEPI_DUMP", dump)
	s := w.start(func(c *config.Config) { c.ExecEnvPassthrough = []string{"FAKEPI_DUMP"} })
	t.Cleanup(s.stop)
	_, agent := s.createAgent("workspace Manager")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": agent.ID})
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ww := workspaceWorld{server: s, w: w, dump: dump, agent: agent, folder: folder, repo: filepath.Join(folder, "repo"), remote: filepath.Join(folder, "origin.git")}
	gitT(t, folder, "init", "-q", "-b", "main", ww.repo)
	if err := os.WriteFile(filepath.Join(ww.repo, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ww.repo, ".gitignore"), []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, ww.repo, "add", ".")
	gitT(t, ww.repo, "commit", "-q", "-m", "init")
	gitT(t, folder, "init", "-q", "--bare", "-b", "main", ww.remote)
	gitT(t, ww.repo, "remote", "add", "origin", ww.remote)
	gitT(t, ww.repo, "push", "-q", "origin", "main")
	if _, err := s.a.Store.PutProjectLocalDirectories(context.Background(), dc.Project.ID, 0, []store.LocalDirectoryEntry{{Path: folder}}); err != nil {
		t.Fatal(err)
	}
	return ww
}

func (ww workspaceWorld) create(title string, ws *taskmodel.WorkspaceInput) (json.RawMessage, error) {
	return ww.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: ww.agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title,
		Request: map[string]any{}, Workspace: ws, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "run",
			Description: " !sleep=30s", AgentID: ww.agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}},
			Edges: []workflow.Edge{}}})
}

func (ww workspaceWorld) mustCreate(t *testing.T, title string, ws *taskmodel.WorkspaceInput) taskmodel.Detail {
	t.Helper()
	body, err := ww.create(title, ws)
	if err != nil {
		t.Fatal(err)
	}
	d := decode[taskmodel.Detail](t, body)
	t.Cleanup(func() {
		_, _ = ww.a.Tasks.Cancel(context.Background(), auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()})
		taskWait(t, ww.server, d.Task.ID, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" || d.Cleanup == "failed" })
	})
	return d
}

func (ww workspaceWorld) retry(t *testing.T, d taskmodel.Detail) {
	t.Helper()
	if _, err := ww.a.Tasks.Retry(context.Background(), auth.Console(), d.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: *d.Task.CurrentNodeRunID}); err != nil {
		t.Fatal(err)
	}
}

func strPtr(s string) *string { return &s }

// A task without a workspace reads as before, with workspace and branch null.
func TestTaskWithoutWorkspaceHasNullWorkspace(t *testing.T) {
	ww := newWorkspaceWorld(t)
	body, err := ww.create("plain task", nil)
	if err != nil {
		t.Fatal(err)
	}
	var view map[string]any
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	task, _ := view["task"].(map[string]any)
	if w, ok := view["workspace"]; !ok || w != nil {
		t.Fatalf("workspace = %v (present %v), want null", w, ok)
	}
	if b, ok := task["branch"]; !ok || b != nil {
		t.Fatalf("task.branch = %v (present %v), want null", b, ok)
	}
	if _, ok := view["workspaceBranchGiven"]; ok {
		t.Fatal("internal fields must not reach the view")
	}
	id := uuid.MustParse(task["id"].(string))
	d := taskWait(t, ww.server, id, func(d taskmodel.Detail) bool { return d.Task.Status == "running" })
	if d.Workspace != nil {
		t.Fatal("a task without a workspace never gets one")
	}
	if _, err := os.Stat(filepath.Join(ww.w.cfg.WorkRoot, "worktrees")); !os.IsNotExist(err) {
		t.Fatal("a task without a workspace creates no worktree folder")
	}
	if _, err := ww.a.Tasks.Cancel(context.Background(), auth.Console(), id, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	taskWait(t, ww.server, id, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" })
}

func TestTaskWorkspaceFieldErrors(t *testing.T) {
	ww := newWorkspaceWorld(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitT(t, outside, "init", "-q", "-b", "main", filepath.Join(outside, "other"))
	plain := filepath.Join(ww.folder, "plain")
	if err := os.Mkdir(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		in    taskmodel.WorkspaceInput
		field string
	}{
		{"relative", taskmodel.WorkspaceInput{Repository: "repo"}, "workspace.repository"},
		{"missing", taskmodel.WorkspaceInput{Repository: filepath.Join(ww.folder, "nope")}, "workspace.repository"},
		{"outside the allowed folders", taskmodel.WorkspaceInput{Repository: filepath.Join(outside, "other")}, "workspace.repository"},
		{"not a repository", taskmodel.WorkspaceInput{Repository: plain}, "workspace.repository"},
		{"bad branch", taskmodel.WorkspaceInput{Repository: ww.repo, Branch: strPtr("a..b")}, "workspace.branch"},
		{"empty branch", taskmodel.WorkspaceInput{Repository: ww.repo, Branch: strPtr("")}, "workspace.branch"},
		{"bad base", taskmodel.WorkspaceInput{Repository: ww.repo, BaseBranch: strPtr("-x")}, "workspace.baseBranch"},
	}
	for _, c := range cases {
		_, err := ww.create("bad "+c.name, &c.in)
		e, ok := apperr.As(err)
		if !ok || e.Code != apperr.CodeValidationFailed || len(e.Fields) != 1 || e.Fields[0].Field != c.field {
			t.Errorf("%s: %v, want a field error on %s", c.name, err, c.field)
		}
	}
	page, err := ww.a.Store.ListTasks(context.Background(), *ww.agent.ProjectID, "", 10)
	if err == nil && len(page.Tasks) != 0 {
		t.Fatalf("no task should have been created: %d", len(page.Tasks))
	}
}

// The worktree is created while the task prepares, from the remote's commit, on the generated branch, under Loop Now's
// own folder; a subfolder of the repository names the repository.
func TestTaskWorkspaceIsCreatedBeforeTheFirstStep(t *testing.T) {
	ww := newWorkspaceWorld(t)
	tip := gitT(t, ww.repo, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(ww.repo, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	created := ww.mustCreate(t, "Fix the login page", &taskmodel.WorkspaceInput{Repository: filepath.Join(ww.repo, "web", "src")})
	w := created.Workspace
	branch := fmt.Sprintf("task/%d-fix-the-login-page", created.Task.Number)
	if w == nil || w.State != "pending" || w.Repository != ww.repo || w.Branch != branch || w.BaseCommit != nil || w.Kept != nil {
		t.Fatalf("created workspace = %+v, want pending on %s", w, branch)
	}
	if created.Task.Branch == nil || *created.Task.Branch != branch {
		t.Fatalf("task.branch = %v", created.Task.Branch)
	}
	root, err := filepath.EvalSymlinks(ww.w.cfg.WorkRoot)
	if err != nil {
		t.Fatal(err)
	}
	suffix := filepath.Join("worktrees", created.Task.ID.String(), "repo")
	if base, err := filepath.EvalSymlinks(strings.TrimSuffix(w.Path, suffix)); err != nil || !strings.HasSuffix(w.Path, suffix) || base != root {
		t.Fatalf("worktree path = %s, want %s/%s", w.Path, root, suffix)
	}
	d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" })
	w = d.Workspace
	if w.State != "active" || w.BaseBranch != "main" || w.BaseCommit == nil || *w.BaseCommit != tip {
		t.Fatalf("running workspace = %+v, want active from main at %s", w, tip)
	}
	if got := gitT(t, w.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Fatalf("worktree is on %s", got)
	}
	if got := gitT(t, ww.repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("the person's checkout moved to %s", got)
	}
	// The step's Pi runs in the worktree and is told so; its turn carries the workspace block.
	taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return ww.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy" })
	var started struct {
		Cwd          string `json:"cwd"`
		SystemPrompt string `json:"systemPrompt"`
	}
	b, err := os.ReadFile(ww.dump)
	if err != nil || json.Unmarshal(b, &started) != nil {
		t.Fatalf("fake Pi dump: %v", err)
	}
	if real, _ := filepath.EvalSymlinks(w.Path); started.Cwd != w.Path && started.Cwd != real {
		t.Fatalf("the step's Pi works in %s, want the worktree %s", started.Cwd, w.Path)
	}
	if !strings.Contains(started.SystemPrompt, "this task's git worktree") {
		t.Fatalf("the step's prompt says where it works:\n%s", started.SystemPrompt)
	}
	var text string
	if err := ww.a.Store.Pool.QueryRow(context.Background(), `SELECT text FROM task_turns WHERE task_id=$1 ORDER BY queue_seq LIMIT 1`, created.Task.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "on branch "+branch) || strings.Contains(text, "clean copy") {
		t.Fatalf("the step's turn carries the workspace block:\n%s", text)
	}
}

// A git failure blocks the task with git's own message; a retry prepares again.
func TestTaskWorkspaceFetchFailureBlocksAndRetrySucceeds(t *testing.T) {
	ww := newWorkspaceWorld(t)
	moved := ww.remote + ".away"
	if err := os.Rename(ww.remote, moved); err != nil {
		t.Fatal(err)
	}
	created := ww.mustCreate(t, "fetch fails", &taskmodel.WorkspaceInput{Repository: ww.repo})
	blocked := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if blocked.Task.Reason == nil || !strings.Contains(*blocked.Task.Reason, "git fetch") || !strings.Contains(*blocked.Task.Reason, "origin.git") {
		t.Fatalf("blocked reason = %v, want git's fetch message", blocked.Task.Reason)
	}
	if blocked.Workspace.State != "pending" {
		t.Fatalf("workspace = %+v", blocked.Workspace)
	}
	if err := os.Rename(moved, ww.remote); err != nil {
		t.Fatal(err)
	}
	ww.retry(t, blocked)
	d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" })
	if d.Workspace.State != "active" || d.Workspace.BaseCommit == nil {
		t.Fatalf("workspace after retry = %+v", d.Workspace)
	}
}

// When the worktree was created but the record still says pending (the server stopped in between), the next
// preparation adopts it: the same base, no second branch, no error.
func TestTaskWorkspaceAdoptsAnExistingWorktree(t *testing.T) {
	ww := newWorkspaceWorld(t)
	moved := ww.remote + ".away"
	if err := os.Rename(ww.remote, moved); err != nil {
		t.Fatal(err)
	}
	created := ww.mustCreate(t, "adopt", &taskmodel.WorkspaceInput{Repository: ww.repo})
	blocked := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if err := os.Rename(moved, ww.remote); err != nil {
		t.Fatal(err)
	}
	w := blocked.Workspace
	first, err := gitworkspace.Create(context.Background(), "git", gitworkspace.Spec{Repository: w.Repository, Path: w.Path, Branch: w.Branch})
	if err != nil {
		t.Fatal(err)
	}
	// The remote moves on; a fresh preparation would start from the new commit, an adopted one keeps its base.
	clone := filepath.Join(ww.folder, "clone")
	gitT(t, ww.folder, "clone", "-q", ww.remote, clone)
	if err := os.WriteFile(filepath.Join(clone, "later"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, clone, "add", ".")
	gitT(t, clone, "commit", "-q", "-m", "later")
	gitT(t, clone, "push", "-q", "origin", "main")
	ww.retry(t, blocked)
	d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" })
	if d.Workspace.State != "active" || d.Workspace.BaseCommit == nil || *d.Workspace.BaseCommit != first.BaseCommit {
		t.Fatalf("workspace = %+v, want the adopted base %s", d.Workspace, first.BaseCommit)
	}
	if branches := gitT(t, ww.repo, "branch", "--list", "task/*"); strings.Count(branches, "task/") != 1 {
		t.Fatalf("branches = %q, want exactly one task branch", branches)
	}
}

// runningWorkspaceTask creates a workspace task and waits until its step runs.
func (ww workspaceWorld) runningWorkspaceTask(t *testing.T, title string) taskmodel.Detail {
	t.Helper()
	created := ww.mustCreate(t, title, &taskmodel.WorkspaceInput{Repository: ww.repo})
	return taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" && d.Workspace.State == "active" })
}

// endTask cancels the task and waits for its cleanup.
func (ww workspaceWorld) endTask(t *testing.T, d taskmodel.Detail) taskmodel.Detail {
	t.Helper()
	if _, err := ww.a.Tasks.Cancel(context.Background(), auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
	return taskWait(t, ww.server, d.Task.ID, func(d taskmodel.Detail) bool { return d.Cleanup == "archived" || d.Cleanup == "failed" })
}

func (ww workspaceWorld) commit(t *testing.T, path, file string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, file), []byte(file+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, path, "add", ".")
	gitT(t, path, "commit", "-q", "-m", file)
}

func gone(path string) bool { _, err := os.Stat(path); return os.IsNotExist(err) }

// Cancelled with edits: the worktree is kept with the counts and nothing is deleted; the person's Remove deletes only
// that worktree, and the branch stays.
func TestTaskWorkspaceKeptOnCancelThenRemoved(t *testing.T) {
	ww := newWorkspaceWorld(t)
	d := ww.runningWorkspaceTask(t, "edits")
	w := d.Workspace
	if err := os.WriteFile(filepath.Join(w.Path, "README"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ended := ww.endTask(t, d)
	if ended.Cleanup != "archived" || ended.Workspace.State != "kept" || ended.Workspace.Kept == nil ||
		*ended.Workspace.Kept != (taskmodel.WorkspaceKept{UncommittedFiles: 2}) {
		t.Fatalf("after cancel: cleanup %s, workspace %+v %+v", ended.Cleanup, ended.Workspace, ended.Workspace.Kept)
	}
	if b, err := os.ReadFile(filepath.Join(w.Path, "README")); err != nil || string(b) != "edited\n" {
		t.Fatal("a kept worktree keeps its files")
	}
	path := "/tasks/" + d.Task.ID.String() + "/workspace/remove"
	action := map[string]any{"actionId": uuid.New()}
	out := decode[taskmodel.Detail](t, ww.must(200, "POST", path, action))
	if out.Workspace.State != "removed" || out.Workspace.Kept != nil {
		t.Fatalf("after Remove: %+v", out.Workspace)
	}
	if !gone(w.Path) || !gone(filepath.Dir(w.Path)) {
		t.Fatal("the worktree and its task folder are gone")
	}
	if _, err := os.Stat(filepath.Join(ww.repo, "README")); err != nil {
		t.Fatal("the person's checkout is untouched")
	}
	if gitT(t, ww.repo, "branch", "--list", w.Branch) == "" {
		t.Fatal("Remove leaves the branch alone")
	}
	if again := decode[taskmodel.Detail](t, ww.must(200, "POST", path, action)); again.Workspace.State != "removed" {
		t.Fatalf("replaying the action: %+v", again.Workspace)
	}
	code, body := ww.call("POST", path, map[string]any{"actionId": uuid.New()})
	if code != 409 || !strings.Contains(string(body), "task_workspace_not_kept") {
		t.Fatalf("a second Remove: %d %s", code, body)
	}
	if code, _ := ww.call("POST", "/tasks/"+uuid.NewString()+"/workspace/remove", map[string]any{"actionId": uuid.New()}); code != 404 {
		t.Fatalf("an unknown task: %d", code)
	}
}

// What cleanup does with each kind of worktree at the end.
func TestTaskWorkspaceCleanup(t *testing.T) {
	ww := newWorkspaceWorld(t)
	t.Run("merged: worktree and local branch gone", func(t *testing.T) {
		d := ww.runningWorkspaceTask(t, "merged")
		ww.commit(t, d.Workspace.Path, "merged.txt")
		gitT(t, d.Workspace.Path, "push", "-q", "origin", "HEAD:main")
		ended := ww.endTask(t, d)
		if ended.Workspace.State != "removed" || !gone(d.Workspace.Path) || gitT(t, ww.repo, "branch", "--list", d.Workspace.Branch) != "" {
			t.Fatalf("workspace %+v, branch %q", ended.Workspace, gitT(t, ww.repo, "branch", "--list", d.Workspace.Branch))
		}
	})
	t.Run("pushed but not merged: worktree gone, branch stays", func(t *testing.T) {
		d := ww.runningWorkspaceTask(t, "pushed")
		ww.commit(t, d.Workspace.Path, "pushed.txt")
		gitT(t, d.Workspace.Path, "push", "-q", "-u", "origin", d.Workspace.Branch)
		ended := ww.endTask(t, d)
		if ended.Workspace.State != "removed" || !gone(d.Workspace.Path) || gitT(t, ww.repo, "branch", "--list", d.Workspace.Branch) == "" {
			t.Fatalf("workspace %+v", ended.Workspace)
		}
	})
	t.Run("unpushed commit: kept", func(t *testing.T) {
		d := ww.runningWorkspaceTask(t, "unpushed")
		ww.commit(t, d.Workspace.Path, "local.txt")
		ended := ww.endTask(t, d)
		if ended.Workspace.State != "kept" || ended.Workspace.Kept == nil || *ended.Workspace.Kept != (taskmodel.WorkspaceKept{UnpushedCommits: 1}) || gone(d.Workspace.Path) {
			t.Fatalf("workspace %+v %+v", ended.Workspace, ended.Workspace.Kept)
		}
	})
	t.Run("only ignored files: removed", func(t *testing.T) {
		d := ww.runningWorkspaceTask(t, "ignored")
		if err := os.MkdirAll(filepath.Join(d.Workspace.Path, "node_modules", "x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.Workspace.Path, "node_modules", "x", "index.js"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ended := ww.endTask(t, d)
		if ended.Workspace.State != "removed" || !gone(d.Workspace.Path) {
			t.Fatalf("workspace %+v %+v", ended.Workspace, ended.Workspace.Kept)
		}
	})
}

// A detached HEAD's own commits are saved as a branch before the person's Remove; the usual case adds no branch.
func TestTaskWorkspaceRemoveSavesADetachedHead(t *testing.T) {
	ww := newWorkspaceWorld(t)
	d := ww.runningWorkspaceTask(t, "detached")
	path := d.Workspace.Path
	gitT(t, path, "checkout", "-q", "--detach")
	ww.commit(t, path, "detached.txt")
	head := gitT(t, path, "rev-parse", "HEAD")
	if ended := ww.endTask(t, d); ended.Workspace.State != "kept" || ended.Workspace.Kept.UnpushedCommits != 1 {
		t.Fatalf("workspace %+v", ended.Workspace)
	}
	ww.must(200, "POST", "/tasks/"+d.Task.ID.String()+"/workspace/remove", map[string]any{"actionId": uuid.New()})
	saved := d.Workspace.Branch + "-detached-" + head[:7]
	if got := gitT(t, ww.repo, "rev-parse", saved); got != head {
		t.Fatalf("the detached commit is on %s: %q", saved, got)
	}

	other := ww.runningWorkspaceTask(t, "attached")
	if err := os.WriteFile(filepath.Join(other.Workspace.Path, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ww.endTask(t, other)
	ww.must(200, "POST", "/tasks/"+other.Task.ID.String()+"/workspace/remove", map[string]any{"actionId": uuid.New()})
	if extra := gitT(t, ww.repo, "branch", "--list", other.Workspace.Branch+"-detached-*"); extra != "" {
		t.Fatalf("no branch is added for a worktree on its branch: %q", extra)
	}
}

// A record whose worktree path points outside Loop Now's worktree folder is never removed.
func TestTaskWorkspaceRemoveRefusesATamperedPath(t *testing.T) {
	ww := newWorkspaceWorld(t)
	d := ww.runningWorkspaceTask(t, "tampered")
	if err := os.WriteFile(filepath.Join(d.Workspace.Path, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ww.endTask(t, d)
	if _, err := ww.a.Store.MutateTask(context.Background(), d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Workspace.Path = ww.repo
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if code, _ := ww.call("POST", "/tasks/"+d.Task.ID.String()+"/workspace/remove", map[string]any{"actionId": uuid.New()}); code != 409 {
		t.Fatalf("a tampered path: %d", code)
	}
	if _, err := os.Stat(filepath.Join(ww.repo, "README")); err != nil {
		t.Fatal("the person's checkout must survive a tampered record")
	}
}
