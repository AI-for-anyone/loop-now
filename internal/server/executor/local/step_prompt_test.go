package local

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

const stepBriefing = "Background: the word for this step is ZEPHYR."

// cleanStep is the first start of a clean task instance: a task step with the Manager's prompt for it and no copy of
// the regular conversation.
func cleanStep(s *agentconfig.Snapshot) {
	s.TaskID, s.ConfigurationAgentID = uuid.New(), uuid.New()
	s.Prompt, s.StepPrompt = "Own prompt.", stepBriefing
}

// The step's prompt follows the Agent's own Prompt in the system prompt, before the platform's "Folders and context",
// and says what it is.
func TestStepPromptFollowsTheAgentsPrompt(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.ContextDir = t.TempDir() }, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(cleanStep)
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	sp := readDump(t, dumpPath).SystemPrompt
	own, step, folders := strings.Index(sp, "Own prompt."), strings.Index(sp, stepBriefing), strings.Index(sp, "## Folders and context")
	if own < 0 || step < own || folders < step || !strings.Contains(sp[own:step], "## Your prompt for this task step") {
		t.Fatalf("the step's prompt goes after the Agent's Prompt, under its own heading:\n%s", sp)
	}
	// Without a step prompt the system prompt is as before.
	plain := h.agent(func(s *agentconfig.Snapshot) { cleanStep(s); s.StepPrompt = "" })
	if res, _ := h.exec(plain, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	if sp := readDump(t, dumpPath).SystemPrompt; strings.Contains(sp, "task step") || !strings.HasPrefix(sp, "Own prompt.\n\n## Folders and context") {
		t.Fatalf("no step section without a step prompt:\n%s", sp)
	}
}

// The instance moves to a step with another prompt (its configuration version stays the frozen one): the next
// dispatch restarts the runtime with the new prompt; the same prompt keeps the process.
func TestStepPromptChangeRestarts(t *testing.T) {
	h := newHarness(t, nil, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(cleanStep)
	if res, _ := h.exec(id, "first"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	pid := h.pid(id)
	if res, _ := h.exec(id, "again"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatal("the same step prompt keeps the process")
	}
	h.src.mu.Lock()
	h.src.snaps[id].StepPrompt = "Background for the next step: the word is QUILL."
	h.src.mu.Unlock()
	if res, _ := h.exec(id, "next step"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("a new step prompt restarts the runtime")
	}
	if sp := readDump(t, dumpPath).SystemPrompt; !strings.Contains(sp, "QUILL") || strings.Contains(sp, "ZEPHYR") {
		t.Fatalf("the restarted runtime has the new step's prompt:\n%s", sp)
	}
	if starts := h.obs.eventsOf(id, store.EventPiStarted); len(starts) != 2 || starts[1].ev.Data["reason"] != "config_changed" {
		t.Fatalf("the restart is a configuration change: %+v", starts)
	}
}

// Claude Code: the first start of a clean step is a new session (no --resume, no --fork-session), with the step's
// prompt in the appended system prompt file; in a worktree with the Agent's own configuration, the regular
// conversation's memory is still passed.
func TestClaudeCleanStepStartsNewSessionWithStepPrompt(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		cleanStep(s)
		s.TaskWorkspace, s.UseLocalConfig = t.TempDir(), true
	})
	if res, _ := h.exec(id, "hello"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	args := h.launches()[0]
	if hasFlag(args, "--resume") || hasFlag(args, "--fork-session") || !hasFlag(args, "--session-id") {
		t.Fatalf("a clean step starts a new session: %v", args)
	}
	b, err := os.ReadFile(argValue(args, "--append-system-prompt-file"))
	if err != nil || !strings.Contains(string(b), stepBriefing) || !strings.Contains(string(b), "Own prompt.") {
		t.Fatalf("the appended system prompt carries the step's prompt: %v\n%s", err, b)
	}
	if v := argValue(args, "--settings"); !strings.HasPrefix(v, `{"autoMemoryDirectory":`) {
		t.Fatalf("the regular conversation's memory is still passed: %q", v)
	}
}

// Codex: the step's prompt is part of the developer instructions of the new thread.
func TestCodexCleanStepHasStepPrompt(t *testing.T) {
	h := newCodexHarness(t, nil)
	id := h.codexAgent(cleanStep)
	res, _ := h.exec(id, "fake:config")
	var cfg struct {
		DeveloperInstructions string `json:"developerInstructions"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil {
		t.Fatal(res.Text, err)
	}
	if !strings.Contains(cfg.DeveloperInstructions, stepBriefing) {
		t.Fatalf("developer instructions: %q", cfg.DeveloperInstructions)
	}
}

// ACP (Grok): the step's prompt is in the standing instructions that go with the session's first message, and there
// is no handoff of a regular conversation.
func TestACPCleanStepHasStepPrompt(t *testing.T) {
	h := newACPHarness(t, "grok", nil)
	id := h.acpAgent(cleanStep)
	res, _ := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, stepBriefing) || !strings.HasSuffix(res.Text, "</loop-now-instructions>\n\nhello") {
		t.Fatalf("first message: %+v", res)
	}
}
