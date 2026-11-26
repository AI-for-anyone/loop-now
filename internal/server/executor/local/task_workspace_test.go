package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/executor"
)

func taskStep(worktree string) func(s *agentconfig.Snapshot) {
	return func(s *agentconfig.Snapshot) {
		s.TaskID, s.ConfigurationAgentID, s.TaskWorkspace = uuid.New(), uuid.New(), worktree
	}
}

// A step of a task with a worktree runs its CLI in the worktree, whatever the runtime; Claude Code keeps the session
// under the worktree's name, and the prompt says where the step works.
func TestTaskWorkspaceIsCwdForEveryRuntime(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.ContextDir = t.TempDir() })
	id := h.claudeAgent(taskStep(dir))
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	sid := argValue(h.launches()[0], "--session-id")
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if want := filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(dir), sid+".jsonl"); recorded != want {
		t.Fatalf("Claude Code's session: %q, want %q", recorded, want)
	}
	b, _ := os.ReadFile(h.e.dirs(id).prompt)
	if !strings.Contains(string(b), "Your working directory: "+dir+", this task's git worktree.") ||
		!strings.Contains(string(b), "Your folder in this task (your own context;") {
		t.Fatalf("the prompt says where the step works:\n%s", b)
	}

	ch := newCodexHarness(t, nil)
	cid := ch.codexAgent(taskStep(dir))
	res, _ := ch.exec(cid, "fake:config")
	var cfg struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Cwd != dir {
		t.Fatalf("Codex's thread: %q %v", res.Text, err)
	}

	gh := newACPHarness(t, "grok", nil)
	gid := gh.acpAgent(taskStep(dir))
	res, _ = gh.exec(gid, "fake:config")
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Cwd != dir {
		t.Fatalf("Grok's session: %q %v", res.Text, err)
	}
}

// The bundled Pi works in the worktree too, and the Agent's own entry file still reaches it: Pi reads AGENTS.md only
// from where it runs, so the platform puts the entry into the prompt.
func TestTaskWorkspacePiStartsThereWithItsEntryFile(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, nil, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(taskStep(dir))
	ws := h.e.dirs(id).workspace
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "AGENTS.md"), []byte("Always answer in haiku."), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	d := readDump(t, dumpPath)
	if real, _ := filepath.EvalSymlinks(dir); d.Cwd != dir && d.Cwd != real {
		t.Fatalf("Pi's working directory: %q, want %q", d.Cwd, dir)
	}
	if !strings.Contains(d.SystemPrompt, "Always answer in haiku.") || !strings.Contains(d.SystemPrompt, filepath.Join(ws, "AGENTS.md")) {
		t.Fatalf("the Agent's own entry file reaches Pi:\n%s", d.SystemPrompt)
	}
}

// A task step without a worktree starts exactly as before: in its own copy of the Agent's folder, with today's limits,
// even when its Agent uses the person's own configuration in its regular conversation.
func TestTaskStepWithoutWorkspaceUnchanged(t *testing.T) {
	h := newHarness(t, nil, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(taskStep(""))
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	if d := readDump(t, dumpPath); d.Cwd != h.e.dirs(id).workspace {
		t.Fatalf("Pi's working directory: %q, want the instance's folder %q", d.Cwd, h.e.dirs(id).workspace)
	}

	ch := newClaudeHarness(t, func(o *Options) { o.ContextDir = t.TempDir() })
	cid := ch.claudeAgent(func(s *agentconfig.Snapshot) {
		taskStep("")(s)
		s.WorkingDirectory, s.UseLocalConfig = t.TempDir(), true
	})
	if res, _ := ch.exec(cid, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args := ch.launches()[0]
	if !hasFlag(args, "--strict-mcp-config") || argValue(args, "--tools") != strings.Join(ClaudeCodeTools, ",") || argValue(args, "--settings") != claudeSettings {
		t.Fatalf("a task step without a worktree keeps today's limits: %v", args)
	}
	b, _ := os.ReadFile(ch.e.dirs(cid).prompt)
	if !strings.Contains(string(b), "Your folder in this task (your working directory;") || strings.Contains(string(b), "git worktree") {
		t.Fatalf("the prompt of a step without a worktree is unchanged:\n%s", b)
	}
}

// In a task with a worktree, the Agent's own CLI configuration applies to the step as to its regular conversation.
func TestTaskWorkspaceUsesTheAgentsOwnConfiguration(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, nil)
	own := h.claudeAgent(func(s *agentconfig.Snapshot) { taskStep(dir)(s); s.UseLocalConfig = true })
	if res, _ := h.exec(own, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	for _, flag := range []string{"--setting-sources", "--strict-mcp-config", "--tools"} {
		if hasFlag(h.launches()[0], flag) {
			t.Errorf("%s limits the person's own configuration: %v", flag, h.launches()[0])
		}
	}
	if v := argValue(h.launches()[0], "--settings"); !strings.HasPrefix(v, `{"autoMemoryDirectory":`) {
		t.Errorf("the only setting added is the regular conversation's memory: %q", v)
	}
	limited := h.claudeAgent(taskStep(dir))
	if res, _ := h.exec(limited, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	if args := h.launches()[1]; !hasFlag(args, "--strict-mcp-config") || argValue(args, "--settings") != claudeSettings {
		t.Fatalf("without the Agent's own configuration the step keeps today's limits: %v", args)
	}
}

// A step whose worktree has gone does not start, and says why.
func TestTaskWorkspaceMissingFailsTheStart(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(taskStep(gone))
	res, _ := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(res.ErrorMessage, "task's worktree") {
		t.Fatalf("a start without the worktree: %+v", res)
	}
}

// What Claude Code keeps for a task's worktree is purged once the task's steps are released; nothing outside
// <WorkRoot>/worktrees is ever purged this way.
func TestPurgeClaudeWorktree(t *testing.T) {
	h := newClaudeHarness(t, nil)
	worktree := filepath.Join(h.root, "worktrees", uuid.NewString(), "app")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := h.e.PurgeClaudeWorktree(ctx, worktree); err != nil || len(h.purgeLog()) != 0 {
		t.Fatalf("nothing kept for the worktree, nothing purged: %v %v", err, h.purgeLog())
	}
	records := filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(worktree))
	if err := os.MkdirAll(records, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := h.e.PurgeClaudeWorktree(ctx, worktree); err != nil {
		t.Fatal(err)
	}
	if log := h.purgeLog(); len(log) != 1 || !strings.Contains(log[0], worktree) {
		t.Fatalf("purge calls: %v", log)
	}
	for _, path := range []string{filepath.Join(h.root, "worktrees"), h.root, t.TempDir(), "relative/app"} {
		if err := h.e.PurgeClaudeWorktree(ctx, path); err == nil {
			t.Errorf("%s must be refused", path)
		}
	}
	if len(h.purgeLog()) != 1 {
		t.Fatalf("refused paths were purged: %v", h.purgeLog())
	}
}

// With the Agent's own configuration, a step in a worktree uses the auto memory of the Agent's regular conversation:
// Claude Code would otherwise key it by the worktree. The folder follows where the regular conversation works.
func TestTaskWorkspaceSharesTheRegularMemory(t *testing.T) {
	dir, regular := t.TempDir(), t.TempDir()
	h := newClaudeHarness(t, nil)
	settings := func(id uuid.UUID, n int) map[string]string {
		t.Helper()
		if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("turn: %+v", res)
		}
		var v map[string]string
		if err := json.Unmarshal([]byte(argValue(h.launches()[n], "--settings")), &v); err != nil {
			t.Fatalf("--settings: %v %v", err, h.launches()[n])
		}
		return v
	}
	base := uuid.New()
	withFolder := h.claudeAgent(func(s *agentconfig.Snapshot) {
		taskStep(dir)(s)
		s.ConfigurationAgentID, s.UseLocalConfig, s.RegularWorkingDirectory = base, true, regular
	})
	want := filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(regular), "memory")
	if got := settings(withFolder, 0); len(got) != 1 || got["autoMemoryDirectory"] != want {
		t.Fatalf("settings = %v, want only autoMemoryDirectory %s", got, want)
	}
	ownFolder := h.claudeAgent(func(s *agentconfig.Snapshot) {
		taskStep(dir)(s)
		s.ConfigurationAgentID, s.UseLocalConfig = base, true
	})
	want = filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(h.e.dirs(base).workspace), "memory")
	if got := settings(ownFolder, 1); got["autoMemoryDirectory"] != want {
		t.Fatalf("settings = %v, want the base Agent's own folder memory %s", got, want)
	}
}
