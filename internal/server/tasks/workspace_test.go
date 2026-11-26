package tasks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/db"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/tasks/gitworkspace"
)

// lastTurnText runs prepareNode on the task (with change applied to its record first) and returns the text of the turn
// it queued.
func lastTurnText(t *testing.T, s *Service, id uuid.UUID, change func(r *taskmodel.Record)) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if change != nil {
			change(m.Record)
		}
		return nil, s.prepareNode(ctx, m, "")
	}); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT text FROM task_turns WHERE task_id=$1 ORDER BY queue_seq DESC LIMIT 1`, id).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// A task without a workspace keeps the step text it had before workspaces, word for word.
func TestStepTextWithoutWorkspaceIsUnchanged(t *testing.T) {
	s, id, _ := fixture(t, false)
	text := lastTurnText(t, s, id, nil)
	r := current(t, s, id)
	n := r.Current()
	want := "You are executing task " + id.String() + ", node work (run " + n.ID.String() + ").\nNode description: \nInputs: {}\n" +
		"The current working directory is a clean copy generated from the task's start configuration; it does not contain the files " +
		"accumulated in your regular folder. Check unread Inbox messages first. When you are done, explicitly submit output/edgeId with " +
		"loopctl; if it fails, you must state the reason; ask questions through the Inbox.\n"
	if text != want {
		t.Fatalf("step text changed:\n%q\nwant\n%q", text, want)
	}
}

// A step of a task with an active workspace is told where it works and the rules of the worktree.
func TestStepTextWithWorkspace(t *testing.T) {
	s, id, _ := fixture(t, false)
	commit := "0123456789abcdef0123456789abcdef01234567"
	text := lastTurnText(t, s, id, func(r *taskmodel.Record) {
		r.View.Workspace = &taskmodel.Workspace{Repository: "/code/app", Path: "/data/worktrees/t/app", Branch: "task/3-fix", BaseBranch: "main",
			BaseCommit: &commit, State: taskmodel.WorkspaceStateActive}
	})
	for _, want := range []string{"/data/worktrees/t/app", "branch task/3-fix", "started from main at 0123456789ab", "do not switch branches",
		"main checkout (/code/app)", "push it to this branch", "one pull request", "Your own folder is still at its usual place"} {
		if !strings.Contains(text, want) {
			t.Errorf("step text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "clean copy") {
		t.Errorf("the clean-copy sentence is replaced:\n%s", text)
	}
}

// setInstanceSnapshot stores a task snapshot with the given configuration on the fixture's instance.
func setInstanceSnapshot(t *testing.T, s *Service, id uuid.UUID, p auth.Principal, cfg taskruntime.Configuration) {
	t.Helper()
	scope, err := taskruntime.TaskScope(p.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	var captured taskruntime.Configuration
	if err := s.Store.Pool.InTx(context.Background(), func(tx *db.Tx) error {
		a, err := s.Store.GetAgent(context.Background(), p.ID)
		if err != nil {
			return err
		}
		project := prompts.ProjectRules{ID: *a.ProjectID}
		if err := tx.QueryRow(context.Background(), `SELECT prompt,prompt_version FROM projects WHERE id=$1`, project.ID).Scan(&project.Prompt, &project.Version); err != nil {
			return err
		}
		c, err := s.Store.CaptureTaskAgent(context.Background(), tx, p.ID, p.RuntimeID, project)
		captured = c.Configuration
		return err
	}); err != nil {
		t.Fatal(err)
	}
	captured.Provider, captured.ModelID, captured.CLIs, captured.UseLocalConfig = cfg.Provider, cfg.ModelID, cfg.CLIs, cfg.UseLocalConfig
	cfg = captured
	snap, err := taskruntime.Capture(scope, p.RuntimeID, taskruntime.CaptureInput{Configuration: cfg,
		Credentials: taskruntime.Credentials{Model: map[string]string{}, CLI: map[string]taskruntime.CredentialPayload{"gh": {Env: map[string]string{"GH_TOKEN": "t"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := snap.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Pool.Exec(context.Background(), `UPDATE task_instances SET snapshot=$2 WHERE id=$1`, p.RuntimeID, data); err != nil {
		t.Fatal(err)
	}
}

func setWorkspace(t *testing.T, s *Service, id uuid.UUID, w *taskmodel.Workspace) {
	t.Helper()
	if _, err := s.Store.MutateTask(context.Background(), id, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Workspace = w
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A step's start snapshot points at the task's worktree only while it is active, and carries the Agent's own CLI
// configuration from task start; with it, project accounts do not apply, as in the regular conversation.
func TestExecutionSnapshotWorkspace(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	work := t.TempDir()
	src := &Source{Store: s.Store, WorkRoot: work}
	setInstanceSnapshot(t, s, id, p, taskruntime.Configuration{ConfigVersion: 1, Provider: agentruntime.ClaudeCodeProvider, ModelID: "m", CLIs: []string{"gh"}, UseLocalConfig: true})
	path := filepath.Join(work, "worktrees", id.String(), "app")
	for _, c := range []struct {
		state string
		want  string
	}{{taskmodel.WorkspaceStatePending, ""}, {taskmodel.WorkspaceStateActive, path}, {taskmodel.WorkspaceStateKept, ""}} {
		setWorkspace(t, s, id, &taskmodel.Workspace{Repository: "/code/app", Path: path, Branch: "b", BaseBranch: "main", State: c.state})
		got, err := src.ExecutionSnapshot(ctx, p.RuntimeID)
		if err != nil || got.TaskWorkspace != c.want {
			t.Fatalf("%s: TaskWorkspace = %q, %v; want %q", c.state, got.TaskWorkspace, err, c.want)
		}
		if c.want != "" && (!got.UseLocalConfig || got.CLIs != nil || len(got.CLIPayloads) != 0) {
			t.Fatalf("own configuration in the worktree: %+v", got)
		}
		if c.want == "" && (got.UseLocalConfig || len(got.CLIs) != 1 || len(got.CLIPayloads) != 1) {
			t.Fatalf("outside an active worktree the step keeps its task configuration: %v %v %v", got.UseLocalConfig, got.CLIs, got.CLIPayloads)
		}
	}
	// A tampered record pointing outside Loop Now's worktree folder does not start.
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: "/code/app", Path: "/code/app", Branch: "b", BaseBranch: "main", State: taskmodel.WorkspaceStateActive})
	if _, err := src.ExecutionSnapshot(ctx, p.RuntimeID); err == nil {
		t.Fatal("a worktree outside <WorkRoot>/worktrees must be refused")
	}
	// Without the Agent's own configuration the step only moves.
	setInstanceSnapshot(t, s, id, p, taskruntime.Configuration{ConfigVersion: 1, Provider: agentruntime.ClaudeCodeProvider, ModelID: "m", CLIs: []string{"gh"}})
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: "/code/app", Path: path, Branch: "b", BaseBranch: "main", State: taskmodel.WorkspaceStateActive})
	got, err := src.ExecutionSnapshot(ctx, p.RuntimeID)
	if err != nil || got.TaskWorkspace != path || got.UseLocalConfig || len(got.CLIs) != 1 {
		t.Fatalf("worktree without own configuration: %+v %v", got, err)
	}
}

// An imported Agent that has had no regular turn carries its imported conversation into a task with a worktree: the
// step forks from the copy taken at import, by path. Other tasks behave as before.
func TestImportedCaptureForWorkspaceTasks(t *testing.T) {
	s, id, p := fixture(t, false)
	ctx := context.Background()
	src := &Source{Store: s.Store, WorkRoot: t.TempDir()}
	capture := filepath.Join(t.TempDir(), "imported-session.jsonl")
	if err := os.WriteFile(capture, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Pool.Exec(ctx, `UPDATE agents SET imported_cli='claude', imported_session_id='s', imported_path='/orig.jsonl', imported_capture=$2, imported_at=$3 WHERE id=$1`, p.ID, capture, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	inst := store.TaskInstance{ID: p.RuntimeID, TaskID: id, AgentID: p.ID}
	none := store.AgentSession{State: "none", Epoch: 1}
	claude, codex := agentruntime.ForProvider(agentruntime.ClaudeCodeProvider), agentruntime.ForProvider(agentruntime.CodexProvider)
	if got, err := src.importedCapture(ctx, inst, none, claude); err != nil || got != "" {
		t.Fatalf("a task without a worktree: %q %v", got, err)
	}
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: "/code/app", Path: "/w", Branch: "b", State: taskmodel.WorkspaceStatePending})
	if got, err := src.importedCapture(ctx, inst, none, claude); err != nil || got != capture {
		t.Fatalf("a workspace task: %q %v; want the capture", got, err)
	}
	for name, c := range map[string]struct {
		session store.AgentSession
		rt      agentruntime.Runtime
	}{"after a regular turn": {store.AgentSession{State: "active", Epoch: 1}, claude}, "after /new": {store.AgentSession{State: "none", Epoch: 2}, claude},
		"on another runtime": {none, codex}} {
		if got, err := src.importedCapture(ctx, inst, c.session, c.rt); err != nil || got != "" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if err := os.Remove(capture); err != nil {
		t.Fatal(err)
	}
	if _, err := src.importedCapture(ctx, inst, none, claude); err == nil {
		t.Fatal("a missing capture must fail the preparation, not start a step without its conversation")
	}
}

// Cleanup of a workspace runs safely again (a retry after a failure, a restart): its first decision stands.
func TestCleanupWorkspaceIsIdempotent(t *testing.T) {
	s, id, _ := fixture(t, false)
	ctx := context.Background()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Source = &Source{Store: s.Store, WorkRoot: work, GitBin: git}
	repo := filepath.Join(work, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command(git, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	path := filepath.Join(s.Source.WorktreeRoot(), id.String(), "repo")
	res, err := gitworkspace.Create(ctx, git, gitworkspace.Spec{Repository: repo, Path: path, Branch: "task/1-x"})
	if err != nil {
		t.Fatal(err)
	}
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: repo, Path: path, Branch: "task/1-x", BaseBranch: res.BaseBranch, BaseCommit: &res.BaseCommit, State: taskmodel.WorkspaceStateActive})
	if err := os.WriteFile(filepath.Join(path, "dirty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.cleanupWorkspace(ctx, id); err != nil {
			t.Fatal(err)
		}
		w := current(t, s, id).View.Workspace
		if w.State != taskmodel.WorkspaceStateKept || w.Kept == nil || w.Kept.UncommittedFiles != 1 {
			t.Fatalf("workspace %+v", w)
		}
	}
	// A worktree whose folder is gone is recorded removed, and git's record of it goes too.
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: repo, Path: path, Branch: "task/1-x", BaseBranch: "main", State: taskmodel.WorkspaceStateActive})
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupWorkspace(ctx, id); err != nil {
		t.Fatal(err)
	}
	if w := current(t, s, id).View.Workspace; w.State != taskmodel.WorkspaceStateRemoved || w.Kept != nil {
		t.Fatalf("workspace %+v", w)
	}
	if out, _ := exec.Command(git, "-C", repo, "worktree", "list").CombinedOutput(); strings.Contains(string(out), path) {
		t.Fatalf("git still lists the worktree: %s", out)
	}
	// A record pointing outside Loop Now's worktree folder is refused, and the folder is left alone.
	setWorkspace(t, s, id, &taskmodel.Workspace{Repository: repo, Path: repo, Branch: "main", BaseBranch: "main", State: taskmodel.WorkspaceStateActive})
	if err := s.cleanupWorkspace(ctx, id); err == nil {
		t.Fatal("a tampered path must be refused")
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("the repository must be untouched")
	}
}
