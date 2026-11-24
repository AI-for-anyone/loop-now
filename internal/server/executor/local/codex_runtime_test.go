package local

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/testutil/fakecodexbin"
)

// codexHarness runs the Codex runtime on fakecodex: the real HOME is a temp directory, so the Codex home is its .codex;
// every start of fakecodex is recorded in argsFile.
type codexHarness struct {
	*harness
	realHome string
	argsFile string
}

func newCodexHarness(t *testing.T, mutate func(o *Options)) *codexHarness {
	t.Helper()
	realHome, _ := fakeRealHome(t)
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	h := newHarness(t, func(o *Options) {
		o.CodexBin = fakecodexbin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
		o.Passthrough = append(o.Passthrough, "FAKECODEX_ARGS_FILE", "FAKECODEX_VERSION", "OPENAI_API_KEY", "CODEX_API_KEY")
		if mutate != nil {
			mutate(o)
		}
	}, "")
	h.env["FAKECODEX_ARGS_FILE"] = argsFile
	return &codexHarness{harness: h, realHome: realHome, argsFile: argsFile}
}

func (h *codexHarness) codexAgent(fn func(s *agentconfig.Snapshot)) uuid.UUID {
	return h.agent(func(s *agentconfig.Snapshot) {
		s.Provider, s.ModelID = "codex-cli", "gpt-test"
		level := "high"
		s.ThinkingLevel = &level
		if fn != nil {
			fn(s)
		}
	})
}

type codexStart struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

// starts returns the app-server starts recorded by fakecodex (version and login checks are left out).
func (h *codexHarness) starts() []codexStart {
	h.t.Helper()
	f, err := os.Open(h.argsFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []codexStart
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s codexStart
		if json.Unmarshal(sc.Bytes(), &s) == nil && len(s.Args) > 0 && s.Args[0] == "app-server" {
			out = append(out, s)
		}
	}
	return out
}

// A turn runs in a thread of `codex app-server` with prompts off and full access, the platform prompt as developer
// instructions, the Agent's Skill directory and the granted plugins' MCP servers; the thread id becomes the session,
// and after a restart the thread continues. Codex finds the user's login through CODEX_HOME while HOME stays the
// Agent's own, and no OpenAI or Codex variable reaches it.
func TestCodexTurnThreadAndResume(t *testing.T) {
	h := newCodexHarness(t, nil)
	h.env["OPENAI_API_KEY"], h.env["CODEX_API_KEY"] = "sk-passed-through", "passed-through"
	id := h.codexAgent(func(s *agentconfig.Snapshot) {
		s.Prompt = "Be precise."
		s.Plugins.MCPServers = map[string]json.RawMessage{"probe": json.RawMessage(`{"command":"probe-mcp","args":["--x"]}`)}
	})
	res, sink := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "echo: hello" || sink.text.String() != "echo: hello" {
		t.Fatalf("first turn: %+v", res)
	}
	h.src.mu.Lock()
	thread := h.src.recorded[id]
	runtimes := h.src.recordRuntimes[id]
	h.src.mu.Unlock()
	if thread == "" || strings.ContainsAny(thread, `/\`) || len(runtimes) == 0 || runtimes[0] != "codex" {
		t.Fatalf("the session is the thread id, recorded for codex: %q %v", thread, runtimes)
	}
	res, _ = h.exec(id, "fake:config")
	var cfg struct {
		Cwd                   string `json:"cwd"`
		Model, Effort         any
		DeveloperInstructions string         `json:"developerInstructions"`
		Config                map[string]any `json:"config"`
		ApprovalPolicy        any            `json:"approvalPolicy"`
		Sandbox               any            `json:"sandbox"`
		ExtraRoots            []string       `json:"extraRoots"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil {
		t.Fatal(res.Text, err)
	}
	d := h.e.dirs(id)
	mcp, _ := cfg.Config["mcp_servers.probe"].(map[string]any)
	if cfg.Cwd != d.workspace || cfg.Model != "gpt-test" || cfg.Effort != "high" || !strings.Contains(cfg.DeveloperInstructions, "Be precise.") ||
		cfg.ApprovalPolicy != "never" || cfg.Sandbox != "danger-full-access" || len(cfg.ExtraRoots) != 1 || cfg.ExtraRoots[0] != d.codexSkills || mcp["command"] != "probe-mcp" {
		t.Fatalf("thread settings: %+v", cfg)
	}
	if _, err := os.Stat(filepath.Join(d.codexSkills, "loopctl", "SKILL.md")); err != nil {
		t.Fatalf("the loopctl Skill is in the Codex Skill directory: %v", err)
	}
	starts := h.starts()
	if len(starts) != 1 {
		t.Fatalf("one app-server for the Agent: %+v", starts)
	}
	env := starts[0].Env
	if env["CODEX_HOME"] != filepath.Join(h.realHome, ".codex") || env["HOME"] == h.realHome || env["HOME"] == "" || env["OPENAI_API_KEY"] != "" || env["CODEX_API_KEY"] != "" {
		t.Fatalf("environment: %+v", env)
	}

	h.src.update(id, func(s *agentconfig.Snapshot) { s.ConfigVersion++; s.ModelID = "gpt-mini" })
	res, _ = h.exec(id, "fake:history")
	if res.Text != "history=2" || len(h.starts()) != 2 {
		t.Fatalf("after a restart the thread continues: %+v, %d starts", res, len(h.starts()))
	}
}

// A recorded thread that Codex no longer has is a missing session, like a missing session file.
func TestCodexMissingThread(t *testing.T) {
	h := newCodexHarness(t, nil)
	id := h.codexAgent(nil)
	h.src.mu.Lock()
	h.src.recorded[id] = "01a20c06-0000-7000-8000-000000000000"
	h.src.mu.Unlock()
	res, _ := h.exec(id, "hello")
	h.src.mu.Lock()
	missing := h.src.missing[id]
	h.src.mu.Unlock()
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrSessionMissing || !missing {
		t.Fatalf("missing thread: %+v %v", res, missing)
	}
}

// Stop interrupts the turn and the process stays for the next message.
func TestCodexStopKeepsTheProcess(t *testing.T) {
	h := newCodexHarness(t, nil)
	id := h.codexAgent(nil)
	h.exec(id, "hello")
	pid := h.pid(id)
	runID := uuid.Must(uuid.NewV7())
	results := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: "fake:slow"}, newSink())
		results <- res
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.e.mu.Lock()
		p := h.e.procs[id]
		h.e.mu.Unlock()
		if p != nil && codexOf(p) != nil && codexOf(p).InTurn() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.e.Interrupt(context.Background(), id, runID); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-results:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Fatalf("stopped: %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not stop")
	}
	if res, _ := h.exec(id, "after"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatalf("the same process takes the next message: %+v", res)
	}
}

// Codex that is missing or older than the version tested is unavailable, with a message that says so; a missing or
// revoked login asks for codex login.
func TestCodexUnavailableAndLogin(t *testing.T) {
	missing := newCodexHarness(t, func(o *Options) { o.CodexBin = filepath.Join(t.TempDir(), "no-codex") })
	if res, _ := missing.exec(missing.codexAgent(nil), "hello"); res.ErrorCode != executor.ErrRuntimeUnavailable || !strings.Contains(res.ErrorMessage, "not found") {
		t.Fatalf("not installed: %+v", res)
	}
	old := newCodexHarness(t, nil)
	old.env["FAKECODEX_VERSION"] = "0.150.0"
	if res, _ := old.exec(old.codexAgent(nil), "hello"); res.ErrorCode != executor.ErrRuntimeUnavailable || !strings.Contains(res.ErrorMessage, "0.150.0") || !strings.Contains(res.ErrorMessage, "0.167.0") {
		t.Fatalf("too old: %+v", res)
	}
	h := newCodexHarness(t, nil)
	if res, _ := h.exec(h.codexAgent(nil), "fake:error unauthorized"); res.ErrorCode != executor.ErrRuntimeLoginRequired || !strings.Contains(res.ErrorMessage, "codex login") {
		t.Fatalf("login: %+v", res)
	}
}

// /compact compacts the thread; usage per model call is recorded for the codex-cli provider, with cached input as
// cache reads.
func TestCodexCompactAndUsage(t *testing.T) {
	h := newCodexHarness(t, nil)
	id := h.codexAgent(nil)
	h.exec(id, "fake:usage")
	h.obs.mu.Lock()
	usage := h.obs.usage
	h.obs.mu.Unlock()
	if len(usage) != 1 || usage[0].u.Provider != "codex-cli" || usage[0].u.Model != "gpt-test" || usage[0].u.Input != 60 || usage[0].u.CacheRead != 40 || usage[0].u.Output != 20 {
		t.Fatalf("usage: %+v", usage)
	}
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Origin: executor.OriginConsoleCompact}, newSink())
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Compaction == nil || !res.Compaction.NoCounts {
		t.Fatalf("compact: %+v %v", res, err)
	}
}

// A task instance forks the regular thread as it was when the task started, and deleting it archives its threads.
func TestCodexTaskForkAndArchive(t *testing.T) {
	h := newCodexHarness(t, nil)
	regular := h.codexAgent(nil)
	h.exec(regular, "one")
	h.exec(regular, "two")
	h.src.mu.Lock()
	thread := h.src.recorded[regular]
	h.src.mu.Unlock()
	baseline, err := codexDriver{e: h.e}.forkBaseline(context.Background(), regular, thread, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	h.exec(regular, "three, after the task started")
	file := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(file, baseline, 0o600); err != nil {
		t.Fatal(err)
	}
	instance := h.codexAgent(func(s *agentconfig.Snapshot) { s.ForkSessionFile = file })
	if res, _ := h.exec(instance, "fake:history"); res.Text != "history=2" {
		t.Fatalf("the instance starts from the regular thread at the task's start: %+v", res)
	}
	if _, err := (codexDriver{e: h.e}).forkBaseline(context.Background(), regular, "01a20c06-0000-7000-8000-000000000000", 1<<20); err == nil {
		t.Fatal("a regular thread Codex does not have cannot be forked")
	}
	if err := h.e.Release(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	archived, _ := filepath.Glob(filepath.Join(h.realHome, ".codex", "fake-archived", "*.json"))
	if len(archived) != 1 {
		t.Fatalf("releasing the instance archives its thread: %v", archived)
	}
	if _, err := os.Stat(filepath.Join(h.realHome, ".codex", "fake-threads", thread+".json")); err != nil {
		t.Fatalf("the regular thread stays: %v", err)
	}
}

// Codex keeps its sessions itself (a retired task instance's archive records the thread); Pi and Claude Code do not.
func TestKeepsSessions(t *testing.T) {
	e := New(Options{}, nil, nil, nil, nil)
	for runtime, want := range map[string]bool{"codex": true, "pi": false, "claude-code": false, "unknown": false} {
		if got := e.KeepsSessions(runtime); got != want {
			t.Errorf("%s: %v", runtime, got)
		}
	}
}

// When Codex cannot refresh its model list online it still answers, from its own cache; the executor tells that from
// Codex's log, and tells a rejected login (401) apart.
func TestCodexModelsNoticesARejectedRefresh(t *testing.T) {
	h := newCodexHarness(t, nil)
	codexHome := filepath.Join(h.realHome, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	list, err := h.e.CodexModels(context.Background())
	if err != nil || len(list.Models) == 0 || list.Cached || list.LoginRejected {
		t.Fatalf("a fresh list: %+v %v", list, err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, ".fakecodex-revoked"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	list, err = h.e.CodexModels(context.Background())
	if err != nil || len(list.Models) == 0 || !list.Cached || !list.LoginRejected {
		t.Fatalf("a list from the cache after the login was rejected: %+v %v", list, err)
	}
}
