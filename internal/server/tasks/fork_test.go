package tasks

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/db"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks/copyworkspace"
	"loop-now/internal/server/tasks/gitworkspace"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/sqlitetest"
)

// forkTask is a running task with plan → fork build {server, web} → join verify (the end); plan and verify are the
// lead's, each branch has its own Agent. Every step outputs answer; verify reads both branches' answers.
type forkTask struct {
	s      *Service
	id     uuid.UUID
	agents map[string]uuid.UUID // by step
}

func forkFixture(t *testing.T, change func(*workflow.Definition)) *forkTask {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "Acme", "Website redesign")
	if err != nil {
		t.Fatal(err)
	}
	agents := map[string]uuid.UUID{}
	instances := []store.TaskInstance{}
	id := uuid.New()
	for _, name := range []string{"lead", "server", "web"} {
		a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), name, "example", "model", "")
		if err != nil {
			t.Fatal(err)
		}
		agents[name] = a.ID
		instances = append(instances, store.TaskInstance{ID: uuid.New(), TaskID: id, AgentID: a.ID, AgentName: name, Snapshot: []byte("fixture"), Sources: []store.TaskFrozenSource{}})
	}
	if _, err := st.SetProjectManager(ctx, dc.Project.ID, agents["lead"]); err != nil {
		t.Fatal(err)
	}
	agents["plan"], agents["verify"] = agents["lead"], agents["lead"]
	answer := []workflow.Field{{Name: "answer", Type: "string", Required: true}}
	step := func(id string) workflow.Step {
		return workflow.Step{ID: id, Title: strings.ToUpper(id[:1]) + id[1:], AgentID: agents[id], InputFields: []workflow.Field{}, OutputFields: answer, Inputs: map[string]workflow.Source{}}
	}
	verify := step("verify")
	verify.InputFields = []workflow.Field{{Name: "server", Type: "string", Required: true}, {Name: "web", Type: "string", Required: true}}
	verify.Inputs = map[string]workflow.Source{"server": {Kind: "node", NodeID: ptrTo("server"), Field: "answer"}, "web": {Kind: "node", NodeID: ptrTo("web"), Field: "answer"}}
	def := workflow.Definition{StartStepID: "plan", Steps: []workflow.Step{step("plan"), step("server"), step("web"), verify},
		Edges: []workflow.Edge{{ID: "to_server", From: "plan", To: "server", Label: "Server", Fork: "build"}, {ID: "to_web", From: "plan", To: "web", Label: "Web", Fork: "build"},
			{ID: "server_done", From: "server", To: "verify", Label: "Done"}, {ID: "web_done", From: "web", To: "verify", Label: "Done"}},
		Forks: []workflow.Fork{{ID: "build", Join: "verify"}}}
	if change != nil {
		change(&def)
	}
	if err := def.Validate(workflow.Limits{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	node := freshNode(def, id, def.Steps[0], instances[0], 1, 1)
	record := taskmodel.Record{View: taskmodel.Detail{Task: taskmodel.Summary{ID: id, ProjectID: dc.Project.ID, Title: "Forked", Status: "preparing", CreatedAt: now, UpdatedAt: now, Version: 1},
		Request: map[string]any{}, Workflow: def, Nodes: []taskmodel.NodeRun{node}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}, EdgeCounts: map[string]int{}}
	record.SetCurrent(node)
	if _, err = st.CreateTask(ctx, dc.Project.ID, agents["lead"], store.TaskAction{ActorKey: "fixture", ID: uuid.New(), Operation: "create", Body: []byte(`{}`)}, func(*db.Tx) (taskmodel.Record, []store.TaskInstance, error) {
		return record, instances, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE task_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, &stubExecutor{}, Options{MaxVisits: 50, ReconcileInterval: 50 * time.Millisecond, RecordTimeout: time.Second, AbortPollInterval: 50 * time.Millisecond}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &forkTask{s: s, id: id, agents: agents}
}

func ptrTo(s string) *string { return &s }

// begin queues the plan step's node message and starts the service with the script as the executor: for each node
// turn, act says which edge the node submits (with answer = its step id) and the outcome; a nil act result submits
// nothing.
func (f *forkTask) begin(t *testing.T, act func(ctx context.Context, n taskmodel.NodeRun, r taskmodel.Record) (edge *string, outcome string, submit bool)) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.s.Store.MutateTask(ctx, f.id, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "running"
		return nil, f.s.prepareNode(ctx, m, m.Record.Current(), "", "")
	}); err != nil {
		t.Fatal(err)
	}
	f.s.Executor = &controlledExecutor{execute: func(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
		inst, err := f.s.Store.TaskInstance(ctx, req.AgentID)
		if err != nil {
			return executor.Result{}, err
		}
		r, err := f.s.Store.GetTask(ctx, inst.TaskID)
		if err != nil {
			return executor.Result{}, err
		}
		n := r.ActiveFor(inst.ID)
		if n == nil {
			return executor.Result{Outcome: executor.OutcomeCompleted}, nil
		}
		edge, outcome, submit := act(ctx, *n, r)
		if submit {
			p := auth.Principal{Kind: auth.KindAgent, ID: inst.AgentID, RuntimeID: inst.ID, TaskID: inst.TaskID}
			if _, err := f.s.Submit(ctx, p, inst.TaskID, n.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{"answer": n.StepID}, EdgeID: edge}); err != nil {
				return executor.Result{}, err
			}
		}
		return executor.Result{Outcome: outcome, Text: "reply"}, nil
	}}
	if err := f.s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Stop(context.Background()) })
}

func (f *forkTask) record(t *testing.T) taskmodel.Record { return current(t, f.s, f.id) }

// nodesOf are the task's node runs of the step, in order.
func nodesOf(r taskmodel.Record, step string) []taskmodel.NodeRun {
	out := []taskmodel.NodeRun{}
	for _, n := range r.View.Nodes {
		if n.StepID == step {
			out = append(out, n)
		}
	}
	return out
}

// exits is the edge each step takes in the fixture.
func exits(step string) *string {
	switch step {
	case "plan":
		return ptrTo("build")
	case "verify":
		return nil
	}
	return ptrTo(step + "_done")
}

// Both branches run at the same time; the join waits for the slower one, then reads both branches' outputs.
func TestForkRunsBranchesAtOnceAndJoinWaits(t *testing.T) {
	f := forkFixture(t, nil)
	started := make(chan string, 4)
	release := map[string]chan struct{}{"server": make(chan struct{}), "web": make(chan struct{})}
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, _ taskmodel.Record) (*string, string, bool) {
		if ch := release[n.StepID]; ch != nil {
			started <- n.StepID
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the branches did not run at the same time")
		}
	}
	r := f.record(t)
	if task := r.View.Task; len(task.CurrentNodes) != 2 || task.CurrentStepID != nil || task.CurrentNodeRunID != nil || task.Status != "running" || r.Fork == nil {
		t.Fatalf("two branches running: %+v fork %+v", task, r.Fork)
	}
	for _, n := range r.Active() {
		if n.Branch == nil || *n.Branch != n.StepID {
			t.Fatalf("branch node %s has branch %v", n.StepID, n.Branch)
		}
	}
	close(release["server"])
	eventually(t, func() bool {
		r := f.record(t)
		return r.Fork != nil && r.Fork.BranchRun("server").State == taskmodel.BranchArrived && len(r.View.Task.CurrentNodes) == 1
	})
	r = f.record(t)
	if r.View.Task.CurrentStepID == nil || *r.View.Task.CurrentStepID != "web" || len(nodesOf(r, "verify")) != 0 {
		t.Fatalf("the join must wait for web: current %v, verify nodes %d", r.View.Task.CurrentStepID, len(nodesOf(r, "verify")))
	}
	close(release["web"])
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
	r = f.record(t)
	v := nodesOf(r, "verify")
	if len(v) != 1 || v[0].Inputs["server"] != "server" || v[0].Inputs["web"] != "web" || v[0].Branch != nil || r.Fork != nil {
		t.Fatalf("join node %+v, fork %+v", v, r.Fork)
	}
	if r.EdgeCounts["to_server"] != 1 || r.EdgeCounts["to_web"] != 1 || r.EdgeCounts["server_done"] != 1 {
		t.Fatalf("edge counts %v", r.EdgeCounts)
	}
}

// A branch that blocks blocks only itself: the other goes on and arrives, the join waits; a retry of the blocked node
// runs it again, and the task then joins and completes.
func TestBlockedBranchBlocksOnlyItself(t *testing.T) {
	f := forkFixture(t, nil)
	releaseServer := make(chan struct{})
	var mu sync.Mutex
	webRuns := 0
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, _ taskmodel.Record) (*string, string, bool) {
		switch n.StepID {
		case "web":
			mu.Lock()
			webRuns++
			first := webRuns == 1
			mu.Unlock()
			if first {
				return nil, executor.OutcomeFailed, false
			}
		case "server":
			select {
			case <-releaseServer:
			case <-ctx.Done():
			}
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	eventually(t, func() bool {
		ws := nodesOf(f.record(t), "web")
		return len(ws) == 1 && ws[0].Status == "blocked"
	})
	if r := f.record(t); r.View.Task.Status != "running" || r.View.Task.Reason != nil {
		t.Fatalf("with server still running the task runs: %s %v", r.View.Task.Status, r.View.Task.Reason)
	}
	close(releaseServer)
	eventually(t, func() bool { return f.record(t).View.Task.Status == "blocked" })
	r := f.record(t)
	if r.Fork.BranchRun("server").State != taskmodel.BranchArrived || len(nodesOf(r, "verify")) != 0 || r.View.Task.Reason == nil {
		t.Fatalf("the join must wait for the blocked branch: %+v", r.Fork)
	}
	web := nodesOf(r, "web")[0]
	if _, err := f.s.Retry(context.Background(), auth.Console(), f.id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: nodesOf(r, "server")[0].ID}); err == nil {
		t.Fatal("a node that is not blocked was retried")
	}
	if _, err := f.s.Retry(context.Background(), auth.Console(), f.id, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: web.ID}); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
	ws := nodesOf(f.record(t), "web")
	if len(ws) != 2 || ws[1].Attempt != 2 || ws[1].Status != "completed" || *ws[1].Branch != "web" {
		t.Fatalf("web nodes %+v", ws)
	}
}

// With maxParallelBranches 1 the second branch starts only when the first arrives at the join.
func TestMaxParallelBranches(t *testing.T) {
	f := forkFixture(t, func(d *workflow.Definition) { one := 1; d.MaxParallelBranches = &one })
	release := make(chan struct{})
	started := make(chan string, 4)
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, _ taskmodel.Record) (*string, string, bool) {
		if n.StepID == "server" {
			started <- n.StepID
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first branch did not start")
	}
	r := f.record(t)
	if len(nodesOf(r, "web")) != 0 || r.Fork.BranchRun("web").State != taskmodel.BranchWaiting || len(r.View.Task.CurrentNodes) != 1 {
		t.Fatalf("web must wait for the limit: %+v", r.Fork)
	}
	close(release)
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
}

// A restart while both branches run blocks each of them, as it blocks the one node of a task without forks.
func TestRestartBlocksEveryRunningBranch(t *testing.T) {
	f := forkFixture(t, nil)
	started := make(chan string, 4)
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, _ taskmodel.Record) (*string, string, bool) {
		if n.StepID == "server" || n.StepID == "web" {
			started <- n.StepID
			<-ctx.Done()
			return nil, executor.OutcomeInterrupted, false
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the branches did not start")
		}
	}
	if err := f.s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := New(f.s.Store, nil, &stubExecutor{}, f.s.Options, f.s.Log)
	if err := again.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer again.Stop(context.Background())
	r := current(t, again, f.id)
	for _, n := range r.Active() {
		if n.Status != "blocked" {
			t.Fatalf("branch %s not blocked after the restart: %s", n.StepID, n.Status)
		}
	}
	if len(r.Active()) != 2 || r.View.Task.Status != "blocked" {
		t.Fatalf("after the restart: %d current, %s", len(r.Active()), r.View.Task.Status)
	}
}

// gitForkTask is the fork fixture with a git workspace: the task's worktree on task/1-forked from a repository with a
// README.
func gitForkTask(t *testing.T) (*forkTask, string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1"} {
		t.Setenv(k, v)
	}
	f := forkFixture(t, nil)
	ctx := context.Background()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.s.Source = &Source{Store: f.s.Store, WorkRoot: work, GitBin: git}
	repo := filepath.Join(work, "repo")
	run := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command(git, append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	path := filepath.Join(f.s.Source.WorktreeRoot(), f.id.String(), "repo")
	res, err := gitworkspace.Create(ctx, git, gitworkspace.Spec{Repository: repo, Path: path, Branch: "task/1-forked"})
	if err != nil {
		t.Fatal(err)
	}
	setWorkspace(t, f.s, f.id, &taskmodel.Workspace{Mode: taskmodel.WorkspaceModeGit, Folder: repo, Path: path, State: taskmodel.WorkspaceStateActive,
		Git: &taskmodel.WorkspaceGit{Repository: repo, Branch: "task/1-forked", BaseBranch: res.BaseBranch, BaseCommit: &res.BaseCommit}})
	return f, git, path
}

// commitIn writes the file in dir and commits it.
func commitIn(t *testing.T, git, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", file}} {
		if out, err := exec.Command(git, append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// nodeText is the node message of the node.
func nodeText(t *testing.T, s *Service, node uuid.UUID) string {
	t.Helper()
	var text string
	if err := s.Store.Pool.QueryRow(context.Background(), `SELECT text FROM task_turns WHERE node_run_id=$1 AND origin='task_node' ORDER BY queue_seq LIMIT 1`, node).Scan(&text); err != nil {
		t.Fatal(err)
	}
	return text
}

// Git mode: each branch works in its own worktree on its own branch cut from the task branch; at the join both are
// merged into the task branch, their worktrees and branches removed, and the join step is told so.
func TestForkGitWorktreesMergeAtJoin(t *testing.T) {
	f, git, task := gitForkTask(t)
	commitIn(t, git, task, "plan.md", "plan\n")
	var mu sync.Mutex
	seen := map[string]string{}
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, r taskmodel.Record) (*string, string, bool) {
		if n.Branch != nil {
			path := instanceWorkspace(&r, n.AgentID, false)
			mu.Lock()
			seen[n.StepID] = path
			mu.Unlock()
			commitIn(t, git, path, n.StepID+".txt", n.StepID+"\n")
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
	r := f.record(t)
	if seen["server"] != task+"--server" || seen["web"] != task+"--web" {
		t.Fatalf("the branches worked in %v", seen)
	}
	for _, file := range []string{"plan.md", "server.txt", "web.txt"} {
		if _, err := os.Stat(filepath.Join(task, file)); err != nil {
			t.Fatalf("%s is not in the task worktree after the join", file)
		}
	}
	w := r.View.Workspace
	for _, step := range []string{"server", "web"} {
		b := w.Branch(step)
		if b == nil || b.State != taskmodel.WorkspaceStateRemoved || b.Merge == nil || *b.Merge != taskmodel.BranchMerged || b.Git.Branch != "task/1-forked--"+step {
			t.Fatalf("branch %s workspace %+v", step, b)
		}
		if _, err := os.Stat(b.Path); !os.IsNotExist(err) {
			t.Fatalf("the merged branch's worktree is still there: %v", err)
		}
		if out, _ := exec.Command(git, "-C", task, "branch", "--list", b.Git.Branch).CombinedOutput(); strings.TrimSpace(string(out)) != "" {
			t.Fatalf("the merged branch %s is still there", b.Git.Branch)
		}
	}
	verify := nodesOf(r, "verify")[0]
	text := nodeText(t, f.s, verify.ID)
	if !strings.Contains(text, "merged them into the task branch task/1-forked in this order: server (task/1-forked--server), web (task/1-forked--web)") {
		t.Fatalf("join message:\n%s", text)
	}
	server := nodesOf(r, "server")[0]
	if text := nodeText(t, f.s, server.ID); !strings.Contains(text, "branch's own git worktree: "+task+"--server") || !strings.Contains(text, "do not push it") || !strings.Contains(text, "join step verify") {
		t.Fatalf("branch message:\n%s", text)
	}
}

// Git mode: two branches changing the same line. The second merge stops on the conflict, which is left in progress in
// the task worktree and handed to the join step; once the join step resolves it, cleanup removes what is left.
func TestForkGitConflictGoesToTheJoinStep(t *testing.T) {
	f, git, task := gitForkTask(t)
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, r taskmodel.Record) (*string, string, bool) {
		switch {
		case n.Branch != nil:
			commitIn(t, git, instanceWorkspace(&r, n.AgentID, false), "README", "from "+n.StepID+"\n")
		case n.StepID == "verify":
			text := nodeText(t, f.s, n.ID)
			if !strings.Contains(text, "Merging branch web (task/1-forked--web) into the task branch stopped on a conflict in: README") || !strings.Contains(text, "Already merged: server (task/1-forked--server)") {
				t.Errorf("join message:\n%s", text)
			}
			if files, inProgress, err := gitworkspace.MergeInProgress(ctx, git, task); err != nil || !inProgress || len(files) != 1 {
				t.Errorf("merge in progress = %v %v %v", files, inProgress, err)
			}
			commitIn(t, git, task, "README", "from server and web\n")
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
	w := f.record(t).View.Workspace
	if b := w.Branch("web"); *b.Merge != taskmodel.BranchConflict || b.State != taskmodel.WorkspaceStateActive {
		t.Fatalf("web %+v", b)
	}
	if b := w.Branch("server"); *b.Merge != taskmodel.BranchMerged || b.State != taskmodel.WorkspaceStateRemoved {
		t.Fatalf("server %+v", b)
	}
	if err := f.s.cleanupWorkspace(context.Background(), f.id); err != nil {
		t.Fatal(err)
	}
	w = f.record(t).View.Workspace
	if b := w.Branch("web"); b.State != taskmodel.WorkspaceStateRemoved {
		t.Fatalf("web after cleanup %+v", b)
	}
	if _, err := os.Stat(task + "--web"); !os.IsNotExist(err) {
		t.Fatal("the web branch's worktree is still there")
	}
	if out, _ := exec.Command(git, "-C", w.Git.Repository, "branch", "--list", "task/1-forked--web").CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Fatal("the web branch, merged by the join step, is still there")
	}
}

// Copy mode: each branch works in its own copy of the task's copy; nothing is put back, and the join step is told where
// the copies are.
func TestForkCopyWorkspaces(t *testing.T) {
	f := forkFixture(t, nil)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.s.Source = &Source{Store: f.s.Store, WorkRoot: work}
	src := filepath.Join(work, "docs")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "notes.md"), []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.s.Source.WorktreeRoot(), f.id.String(), "docs")
	if _, err := copyworkspace.Make(src, path); err != nil {
		t.Fatal(err)
	}
	setWorkspace(t, f.s, f.id, &taskmodel.Workspace{Mode: taskmodel.WorkspaceModeCopy, Folder: src, Path: path, State: taskmodel.WorkspaceStateActive, Copy: &taskmodel.WorkspaceCopy{Files: 1, Bytes: 6}})
	f.begin(t, func(ctx context.Context, n taskmodel.NodeRun, r taskmodel.Record) (*string, string, bool) {
		if n.Branch != nil {
			dir := instanceWorkspace(&r, n.AgentID, false)
			if _, err := os.Stat(filepath.Join(dir, "notes.md")); err != nil {
				t.Errorf("the branch's copy lacks the task's files: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, n.StepID+".md"), []byte(n.StepID), 0o644); err != nil {
				t.Error(err)
			}
		}
		return exits(n.StepID), executor.OutcomeCompleted, true
	})
	eventually(t, func() bool { return f.record(t).View.Task.Status == "completed" })
	r := f.record(t)
	for _, step := range []string{"server", "web"} {
		b := r.View.Workspace.Branch(step)
		if b == nil || b.Path != filepath.Join(filepath.Dir(path), "docs--"+step, "docs") || b.Git != nil || b.Merge != nil {
			t.Fatalf("branch %s workspace %+v", step, b)
		}
		if _, err := os.Stat(filepath.Join(path, step+".md")); !os.IsNotExist(err) {
			t.Fatalf("a branch's file was put back into the task's copy")
		}
	}
	text := nodeText(t, f.s, nodesOf(r, "verify")[0].ID)
	if !strings.Contains(text, "nothing was put back into the task's copy") || !strings.Contains(text, "server: "+r.View.Workspace.Branch("server").Path) {
		t.Fatalf("join message:\n%s", text)
	}
	// A completed task's copies all go.
	if err := f.s.cleanupWorkspace(context.Background(), f.id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.s.Source.WorktreeRoot(), f.id.String())); !os.IsNotExist(err) {
		t.Fatalf("the task's folder is still there: %v", err)
	}
}

// A task stored before forks (no list of current nodes, no branch on its nodes) runs as it did: its one current node is
// found, submits and moves on, and the record is then written with the list.
func TestTaskStoredBeforeForksRunsAsBefore(t *testing.T) {
	s, id, p := fixture(t, true)
	ctx := context.Background()
	if _, err := s.Store.Pool.Exec(ctx, `UPDATE task_records SET document=json_remove(document,'$.view.task.currentNodes','$.view.nodes[0].branch') WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT document FROM task_records WHERE id=$1`, id).Scan(&stored); err != nil || strings.Contains(stored, "currentNodes") {
		t.Fatalf("the fixture still has the list: %v", err)
	}
	v := turn(t, s, id)
	submit(t, s, id, p, ptrTo("again"))
	if err := s.settle(ctx, v, executor.Result{Outcome: executor.OutcomeCompleted}); err != nil {
		t.Fatal(err)
	}
	r := current(t, s, id)
	if r.Current() == nil || r.Current().Visit != 2 || r.View.Task.Status != "running" || len(r.View.Task.CurrentNodes) != 1 || r.Current().Branch != nil {
		t.Fatalf("after the move: %+v", r.View.Task)
	}
	if err := s.Store.Pool.QueryRow(ctx, `SELECT document FROM task_records WHERE id=$1`, id).Scan(&stored); err != nil || !strings.Contains(stored, `"currentNodes":[{`) {
		t.Fatalf("the record is not written with the list: %v", err)
	}
}
