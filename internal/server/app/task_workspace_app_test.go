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
	"loop-now/internal/server/tasks/gitbin"
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
	return ww.createStep(title, ws, " !sleep=30s")
}

// createStep creates a one-step task whose step's fake Pi follows the directives in description.
func (ww workspaceWorld) createStep(title string, ws *taskmodel.WorkspaceInput, description string) (json.RawMessage, error) {
	return ww.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: ww.agent.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: title,
		Request: map[string]any{}, Workspace: ws, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "run",
			Description: description, AgentID: ww.agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}},
			Edges: []workflow.Edge{}}})
}

func (ww workspaceWorld) mustCreate(t *testing.T, title string, ws *taskmodel.WorkspaceInput) taskmodel.Detail {
	t.Helper()
	return ww.mustCreateStep(t, title, ws, " !sleep=30s")
}

func (ww workspaceWorld) mustCreateStep(t *testing.T, title string, ws *taskmodel.WorkspaceInput, description string) taskmodel.Detail {
	t.Helper()
	body, err := ww.createStep(title, ws, description)
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
	if m, ok := task["workspaceMode"]; !ok || m != nil {
		t.Fatalf("task.workspaceMode = %v (present %v), want null", m, ok)
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
		{"relative", taskmodel.WorkspaceInput{Folder: "repo"}, "workspace.folder"},
		{"missing", taskmodel.WorkspaceInput{Folder: filepath.Join(ww.folder, "nope")}, "workspace.folder"},
		{"outside the allowed folders", taskmodel.WorkspaceInput{Folder: filepath.Join(outside, "other")}, "workspace.folder"},
		{"git mode outside a repository", taskmodel.WorkspaceInput{Folder: plain, Mode: "git"}, "workspace.folder"},
		{"a branch outside a repository", taskmodel.WorkspaceInput{Folder: plain, Branch: strPtr("b")}, "workspace.branch"},
		{"a branch in copy mode", taskmodel.WorkspaceInput{Folder: ww.repo, Mode: "copy", Branch: strPtr("b")}, "workspace.branch"},
		{"a base in copy mode", taskmodel.WorkspaceInput{Folder: ww.repo, Mode: "copy", BaseBranch: strPtr("main")}, "workspace.baseBranch"},
		{"unknown mode", taskmodel.WorkspaceInput{Folder: ww.repo, Mode: "svn"}, "workspace.mode"},
		{"bad branch", taskmodel.WorkspaceInput{Folder: ww.repo, Branch: strPtr("a..b")}, "workspace.branch"},
		{"empty branch", taskmodel.WorkspaceInput{Folder: ww.repo, Branch: strPtr("")}, "workspace.branch"},
		{"bad base", taskmodel.WorkspaceInput{Folder: ww.repo, BaseBranch: strPtr("-x")}, "workspace.baseBranch"},
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
	created := ww.mustCreate(t, "Fix the login page", &taskmodel.WorkspaceInput{Folder: filepath.Join(ww.repo, "web", "src")})
	w := created.Workspace
	branch := fmt.Sprintf("task/%d-fix-the-login-page", created.Task.Number)
	if w == nil || w.State != "pending" || w.Git.Repository != ww.repo || w.Git.Branch != branch || w.Git.BaseCommit != nil || w.Kept != nil {
		t.Fatalf("created workspace = %+v, want pending on %s", w, branch)
	}
	if created.Task.WorkspaceMode == nil || *created.Task.WorkspaceMode != "git" {
		t.Fatalf("task.workspaceMode = %v", created.Task.WorkspaceMode)
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
	if w.State != "active" || w.Git.BaseBranch != "main" || w.Git.BaseCommit == nil || *w.Git.BaseCommit != tip {
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
	created := ww.mustCreate(t, "fetch fails", &taskmodel.WorkspaceInput{Folder: ww.repo})
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
	if d.Workspace.State != "active" || d.Workspace.Git.BaseCommit == nil {
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
	created := ww.mustCreate(t, "adopt", &taskmodel.WorkspaceInput{Folder: ww.repo})
	blocked := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if err := os.Rename(moved, ww.remote); err != nil {
		t.Fatal(err)
	}
	w := blocked.Workspace
	first, err := gitworkspace.Create(context.Background(), "git", gitworkspace.Spec{Repository: w.Git.Repository, Path: w.Path, Branch: w.Git.Branch})
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
	if d.Workspace.State != "active" || d.Workspace.Git.BaseCommit == nil || *d.Workspace.Git.BaseCommit != first.BaseCommit {
		t.Fatalf("workspace = %+v, want the adopted base %s", d.Workspace, first.BaseCommit)
	}
	if branches := gitT(t, ww.repo, "branch", "--list", "task/*"); strings.Count(branches, "task/") != 1 {
		t.Fatalf("branches = %q, want exactly one task branch", branches)
	}
}

// runningWorkspaceTask creates a workspace task and waits until its step runs.
func (ww workspaceWorld) runningWorkspaceTask(t *testing.T, title string) taskmodel.Detail {
	t.Helper()
	created := ww.mustCreate(t, title, &taskmodel.WorkspaceInput{Folder: ww.repo})
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
	if gitT(t, ww.repo, "branch", "--list", w.Git.Branch) == "" {
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
		if ended.Workspace.State != "removed" || !gone(d.Workspace.Path) || gitT(t, ww.repo, "branch", "--list", d.Workspace.Git.Branch) != "" {
			t.Fatalf("workspace %+v, branch %q", ended.Workspace, gitT(t, ww.repo, "branch", "--list", d.Workspace.Git.Branch))
		}
	})
	t.Run("pushed but not merged: worktree gone, branch stays", func(t *testing.T) {
		d := ww.runningWorkspaceTask(t, "pushed")
		ww.commit(t, d.Workspace.Path, "pushed.txt")
		gitT(t, d.Workspace.Path, "push", "-q", "-u", "origin", d.Workspace.Git.Branch)
		ended := ww.endTask(t, d)
		if ended.Workspace.State != "removed" || !gone(d.Workspace.Path) || gitT(t, ww.repo, "branch", "--list", d.Workspace.Git.Branch) == "" {
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
	saved := d.Workspace.Git.Branch + "-detached-" + head[:7]
	if got := gitT(t, ww.repo, "rev-parse", saved); got != head {
		t.Fatalf("the detached commit is on %s: %q", saved, got)
	}

	other := ww.runningWorkspaceTask(t, "attached")
	if err := os.WriteFile(filepath.Join(other.Workspace.Path, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ww.endTask(t, other)
	ww.must(200, "POST", "/tasks/"+other.Task.ID.String()+"/workspace/remove", map[string]any{"actionId": uuid.New()})
	if extra := gitT(t, ww.repo, "branch", "--list", other.Workspace.Git.Branch+"-detached-*"); extra != "" {
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

// docs makes a plain folder of documents in the project's local directory, with a link to a file outside it.
func (ww workspaceWorld) docs(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(ww.folder, name)
	for file, text := range map[string]string{"a.md": "alpha\n", "notes/b.md": "beta\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(ww.repo, "README"), filepath.Join(dir, "readme-link")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// treeState lists every entry of a tree with its type, size, modification time and content.
func treeState(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %v %d %d", strings.TrimPrefix(p, root), info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, _ := os.ReadFile(p)
			b.Write(data)
		}
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A plain folder is copied while the task prepares; the step works in the copy and is told so; a completed task's copy
// is removed even though files were added; the original never changes.
func TestTaskCopyWorkspaceRunsAndCompletes(t *testing.T) {
	ww := newWorkspaceWorld(t)
	src := ww.docs(t, "docs")
	before := treeState(t, src)
	created := ww.mustCreateStep(t, "Summarize the docs", &taskmodel.WorkspaceInput{Folder: src}, " !sleep=2s")
	w := created.Workspace
	if w == nil || w.Mode != "copy" || w.Folder != src || w.State != "pending" || w.Git != nil || w.Copy != nil || w.ModeNote != nil || created.Task.Branch != nil {
		t.Fatalf("created workspace = %+v, task.branch %v", w, created.Task.Branch)
	}
	if created.Task.WorkspaceMode == nil || *created.Task.WorkspaceMode != "copy" {
		t.Fatalf("task.workspaceMode = %v", created.Task.WorkspaceMode)
	}
	page, err := ww.a.Store.ListTasks(context.Background(), *ww.agent.ProjectID, "", 10)
	if err != nil || len(page.Tasks) == 0 || page.Tasks[0].WorkspaceMode == nil || *page.Tasks[0].WorkspaceMode != "copy" {
		t.Fatalf("the task list shows the mode: %+v %v", page, err)
	}
	if !strings.HasSuffix(w.Path, filepath.Join("worktrees", created.Task.ID.String(), "docs")) {
		t.Fatalf("copy path = %s", w.Path)
	}
	d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool {
		return d.Task.Status == "running" && len(d.Nodes[0].RunIDs) > 0 && ww.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
	})
	w = d.Workspace
	if w.State != "active" || w.Copy == nil || *w.Copy != (taskmodel.WorkspaceCopy{Files: 2, Bytes: 11}) {
		t.Fatalf("running workspace = %+v %+v", w, w.Copy)
	}
	if b, err := os.ReadFile(filepath.Join(w.Path, "notes", "b.md")); err != nil || string(b) != "beta\n" {
		t.Fatalf("the copy has the files: %q %v", b, err)
	}
	if _, err := os.Lstat(filepath.Join(w.Path, "readme-link")); err == nil {
		t.Fatal("a link to outside the folder is in the copy")
	}
	var started struct {
		Cwd string `json:"cwd"`
	}
	if b, err := os.ReadFile(ww.dump); err != nil || json.Unmarshal(b, &started) != nil {
		t.Fatalf("fake Pi dump: %v", err)
	}
	if real, _ := filepath.EvalSymlinks(w.Path); started.Cwd != w.Path && started.Cwd != real {
		t.Fatalf("the step works in %s, want the copy %s", started.Cwd, w.Path)
	}
	var text string
	if err := ww.a.Store.Pool.QueryRow(context.Background(), `SELECT text FROM task_turns WHERE task_id=$1 ORDER BY queue_seq LIMIT 1`, created.Task.ID).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "a copy of the folder "+src) || !strings.Contains(text, "loopctl task artifact put") || strings.Contains(text, "branch") {
		t.Fatalf("the step's turn carries the copy block:\n%s", text)
	}
	// The step writes a result in the copy and submits; the task completes.
	if err := os.WriteFile(filepath.Join(w.Path, "summary.md"), []byte("sum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	node := d.Nodes[0]
	principal := auth.Principal{Kind: auth.KindAgent, ID: ww.agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
	if _, err := ww.a.Tasks.Submit(context.Background(), principal, created.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	ended := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	if ended.Workspace.State != "removed" || ended.Workspace.Kept != nil || !gone(w.Path) || !gone(filepath.Dir(w.Path)) {
		t.Fatalf("after completion: %+v", ended.Workspace)
	}
	if treeState(t, src) != before {
		t.Fatal("the original folder changed")
	}
}

// Cancelled: kept when a file was added (the person removes it), removed when nothing changed.
func TestTaskCopyWorkspaceCancel(t *testing.T) {
	ww := newWorkspaceWorld(t)
	src := ww.docs(t, "docs")
	before := treeState(t, src)
	running := func(title string) taskmodel.Detail {
		created := ww.mustCreate(t, title, &taskmodel.WorkspaceInput{Folder: src})
		return taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "running" && d.Workspace.State == "active" })
	}
	d := running("one new file")
	if err := os.WriteFile(filepath.Join(d.Workspace.Path, "draft.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ended := ww.endTask(t, d)
	if ended.Workspace.State != "kept" || ended.Workspace.Kept == nil || *ended.Workspace.Kept != (taskmodel.WorkspaceKept{ChangedFiles: 1}) || gone(d.Workspace.Path) {
		t.Fatalf("after cancel: %+v %+v", ended.Workspace, ended.Workspace.Kept)
	}
	path := "/tasks/" + d.Task.ID.String() + "/workspace/remove"
	out := decode[taskmodel.Detail](t, ww.must(200, "POST", path, map[string]any{"actionId": uuid.New()}))
	if out.Workspace.State != "removed" || !gone(d.Workspace.Path) || !gone(filepath.Dir(d.Workspace.Path)) {
		t.Fatalf("after Remove: %+v", out.Workspace)
	}

	d = running("untouched")
	if ended := ww.endTask(t, d); ended.Workspace.State != "removed" || !gone(d.Workspace.Path) {
		t.Fatalf("an untouched copy: %+v", ended.Workspace)
	}
	if treeState(t, src) != before {
		t.Fatal("the original folder changed")
	}
}

// Two tasks on the same folder at once get two copies.
func TestTaskCopyWorkspaceTwoAtOnce(t *testing.T) {
	ww := newWorkspaceWorld(t)
	src := ww.docs(t, "docs")
	a := ww.mustCreate(t, "first", &taskmodel.WorkspaceInput{Folder: src})
	b := ww.mustCreate(t, "second", &taskmodel.WorkspaceInput{Folder: src})
	active := func(d taskmodel.Detail) bool { return d.Workspace.State == "active" }
	pa, pb := taskWait(t, ww.server, a.Task.ID, active).Workspace.Path, taskWait(t, ww.server, b.Task.ID, active).Workspace.Path
	if pa == pb {
		t.Fatal("both tasks share one copy")
	}
	if err := os.WriteFile(filepath.Join(pa, "a.md"), []byte("first wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(pb, "a.md")); string(got) != "alpha\n" {
		t.Fatalf("the second copy reads %q", got)
	}
}

// A copy that fails (an unreadable file) blocks the task with the file's name; a retry after the fix copies.
func TestTaskCopyWorkspaceFailureBlocksAndRetrySucceeds(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	ww := newWorkspaceWorld(t)
	src := ww.docs(t, "docs")
	secret := filepath.Join(src, "notes", "private.md")
	if err := os.WriteFile(secret, []byte("x"), 0); err != nil {
		t.Fatal(err)
	}
	created := ww.mustCreate(t, "unreadable", &taskmodel.WorkspaceInput{Folder: src})
	d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
	if d.Workspace.State != "pending" || d.Task.Reason == nil || !strings.Contains(*d.Task.Reason, "Could not copy the task's folder") ||
		!strings.Contains(*d.Task.Reason, "notes/private.md cannot be read") {
		t.Fatalf("blocked: %v, workspace %+v", d.Task.Reason, d.Workspace)
	}
	if err := os.Chmod(secret, 0o644); err != nil {
		t.Fatal(err)
	}
	ww.retry(t, d)
	if d = taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Workspace.State == "active" }); d.Workspace.Copy.Files != 3 {
		t.Fatalf("after retry: %+v", d.Workspace.Copy)
	}
}

// Without a working git a repository is copied and the task says why; git mode or a branch is a field error naming
// the missing git; mode copy copies a repository even when git works.
func TestTaskWorkspaceWithoutGit(t *testing.T) {
	ww := newWorkspaceWorld(t)
	forced := ww.mustCreate(t, "read the repo", &taskmodel.WorkspaceInput{Folder: ww.repo, Mode: "copy"})
	if forced.Workspace.Mode != "copy" || forced.Workspace.ModeNote != nil || forced.Task.Branch != nil {
		t.Fatalf("forced copy: %+v", forced.Workspace)
	}

	ww.a.Tasks.Source.Git = &gitbin.Finder{Dirs: func(context.Context) []string { return []string{t.TempDir()} }}
	before := treeState(t, ww.repo)
	created := ww.mustCreate(t, "no git", &taskmodel.WorkspaceInput{Folder: filepath.Join(ww.repo)})
	w := created.Workspace
	if w.Mode != "copy" || w.ModeNote == nil || *w.ModeNote != "No working git on this computer, so the folder was copied." || created.Task.Branch != nil {
		t.Fatalf("no git: %+v", w)
	}
	if d := taskWait(t, ww.server, created.Task.ID, func(d taskmodel.Detail) bool { return d.Workspace.State == "active" }); d.Workspace.Copy.Files == 0 {
		t.Fatalf("copied: %+v", d.Workspace.Copy)
	}
	for _, c := range []struct {
		in    taskmodel.WorkspaceInput
		field string
	}{{taskmodel.WorkspaceInput{Folder: ww.repo, Mode: "git"}, "workspace.mode"}, {taskmodel.WorkspaceInput{Folder: ww.repo, Branch: strPtr("b")}, "workspace.branch"}} {
		_, err := ww.create("needs git", &c.in)
		e, ok := apperr.As(err)
		if !ok || len(e.Fields) != 1 || e.Fields[0].Field != c.field || !strings.Contains(e.Fields[0].Message, "no working git") {
			t.Errorf("%+v: %v, want a field error on %s naming the missing git", c.in, err, c.field)
		}
	}
	if treeState(t, ww.repo) != before {
		t.Fatal("the repository changed")
	}
}
