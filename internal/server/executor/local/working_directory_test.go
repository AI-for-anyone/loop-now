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

// An Agent with a working folder runs its CLI there: Claude Code keeps the session under that folder's name, Codex's
// thread and Grok's session work in it, and the system prompt says where the Agent works.
func TestWorkingDirectoryIsCwd(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.WorkingDirectory = dir })
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
	if b, _ := os.ReadFile(h.e.dirs(id).prompt); !strings.Contains(string(b), "Your working directory: "+dir) {
		t.Fatalf("the prompt says where the Agent works:\n%s", b)
	}

	ch := newCodexHarness(t, nil)
	cid := ch.codexAgent(func(s *agentconfig.Snapshot) { s.WorkingDirectory = dir })
	res, _ := ch.exec(cid, "fake:config")
	var cfg struct {
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Cwd != dir {
		t.Fatalf("Codex's thread: %q %v", res.Text, err)
	}

	gh := newACPHarness(t, "grok", nil)
	gid := gh.acpAgent(func(s *agentconfig.Snapshot) { s.WorkingDirectory = dir })
	res, _ = gh.exec(gid, "fake:config")
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Cwd != dir {
		t.Fatalf("Grok's session: %q %v", res.Text, err)
	}
}

// With the person's own configuration, Claude Code runs as in their terminal: their settings and MCP servers are read,
// every built-in tool is available, auto memory and Claude Code's own Skills stay on; Loop Now still adds its plugin
// (Skills, loopctl), its MCP servers and its system prompt.
func TestClaudeLocalConfigFlags(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.WorkingDirectory, s.UseLocalConfig, s.Prompt = dir, true, "Be precise."
		s.Plugins.MCPServers = map[string]json.RawMessage{"probe": json.RawMessage(`{"command":"probe-mcp"}`)}
	})
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args, d := h.launches()[0], h.e.dirs(id)
	for _, flag := range []string{"--setting-sources", "--settings", "--strict-mcp-config", "--tools"} {
		if hasFlag(args, flag) {
			t.Errorf("%s limits the person's own configuration: %v", flag, args)
		}
	}
	if argValue(args, "--plugin-dir") != d.claudePlugin || argValue(args, "--mcp-config") != d.claudeMCP || argValue(args, "--append-system-prompt-file") != d.prompt {
		t.Fatalf("Loop Now's own pieces are still added: %v", args)
	}
}

// A working folder alone changes only where the Agent works: without the person's own configuration, Claude Code keeps
// today's limits.
func TestClaudeDefaultFlagsUnchanged(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.WorkingDirectory = t.TempDir() })
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args := h.launches()[0]
	want := map[string]string{"--setting-sources": "", "--settings": claudeSettings, "--tools": strings.Join(ClaudeCodeTools, ",")}
	for flag, v := range want {
		if !hasFlag(args, flag) || argValue(args, flag) != v {
			t.Errorf("argument %s = %q, want %q", flag, argValue(args, flag), v)
		}
	}
	if !hasFlag(args, "--strict-mcp-config") {
		t.Errorf("missing --strict-mcp-config: %v", args)
	}
}

// A task step of an Agent with a working folder runs in the step's own copy of the Agent's folder, never in the
// working folder.
func TestTaskStepUsesOwnWorkspaceCopy(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.WorkingDirectory, s.UseLocalConfig = dir, true
		s.TaskID, s.ConfigurationAgentID = uuid.New(), uuid.New()
	})
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args := h.launches()[0]
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if want := claudecli.ProjectDirName(h.e.dirs(id).workspace); filepath.Base(filepath.Dir(recorded)) != want {
		t.Fatalf("the step works in its own folder: %q", recorded)
	}
	if !hasFlag(args, "--strict-mcp-config") || argValue(args, "--tools") != strings.Join(ClaudeCodeTools, ",") {
		t.Fatalf("a task step keeps today's limits: %v", args)
	}
}

// An imported Agent's first turn continues the copy taken at import: Claude Code forks the copied session into a new
// one in the working folder, which later turns resume; Codex forks the thread from the recorded turn.
func TestImportedFirstTurnForks(t *testing.T) {
	dir := t.TempDir()
	h := newClaudeHarness(t, nil)
	capture := filepath.Join(t.TempDir(), "imported-session.jsonl")
	if err := os.WriteFile(capture, []byte(`{"type":"user","sessionId":"x","message":{"role":"user","content":"earlier"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.WorkingDirectory, s.UseLocalConfig, s.ForkSessionFile = dir, true, capture
	})
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args := h.launches()[0]
	sid := argValue(args, "--session-id")
	if argValue(args, "--resume") != capture || !hasFlag(args, "--fork-session") || !claudecli.ValidSessionID(sid) {
		t.Fatalf("the first turn forks the copy: %v", args)
	}
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if filepath.Base(recorded) != sid+".jsonl" || filepath.Base(filepath.Dir(recorded)) != claudecli.ProjectDirName(dir) {
		t.Fatalf("the fork is the Agent's session, in the working folder: %q", recorded)
	}

	ch := newCodexHarness(t, nil)
	regular := ch.codexAgent(nil)
	if res, _ := ch.exec(regular, "one"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("regular turn: %+v", res)
	}
	ch.src.mu.Lock()
	thread := ch.src.recorded[regular]
	ch.src.mu.Unlock()
	base := filepath.Join(t.TempDir(), "imported-thread.json")
	b, _ := json.Marshal(map[string]string{"threadId": thread})
	if err := os.WriteFile(base, b, 0o600); err != nil {
		t.Fatal(err)
	}
	imported := ch.codexAgent(func(s *agentconfig.Snapshot) {
		s.WorkingDirectory, s.UseLocalConfig, s.ForkSessionFile = dir, true, base
	})
	if res, _ := ch.exec(imported, "fake:history"); res.Text != "history=1" {
		t.Fatalf("the imported Agent continues the thread: %+v", res)
	}
	ch.src.mu.Lock()
	forked := ch.src.recorded[imported]
	ch.src.mu.Unlock()
	if forked == "" || forked == thread {
		t.Fatalf("a fork, not the original thread: %q / %q", forked, thread)
	}
}
