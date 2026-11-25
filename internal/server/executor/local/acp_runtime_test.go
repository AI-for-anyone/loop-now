package local

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/acpcli"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/acp"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/fakeacpbin"
)

// acpHarness runs the Grok or Antigravity runtime on fakeacp: the real HOME is a temp directory (where fakeacp keeps
// its sessions and login state, under .fakeacp); every start of fakeacp is recorded in argsFile.
type acpHarness struct {
	*harness
	realHome string
	argsFile string
	flavor   string
}

func newACPHarness(t *testing.T, flavor string, mutate func(o *Options)) *acpHarness {
	t.Helper()
	realHome, _ := fakeRealHome(t)
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	h := newHarness(t, func(o *Options) {
		bin := fakeacpbin.Path(t)
		o.GrokBin, o.AntigravityBin = bin, bin
		o.RealHome = func() (string, error) { return realHome, nil }
		o.Passthrough = append(o.Passthrough, "FAKEACP_ARGS_FILE", "FAKEACP_FLAVOR", "FAKEACP_LOAD_SILENT", "XAI_API_KEY", "GEMINI_API_KEY")
		if mutate != nil {
			mutate(o)
		}
	}, "")
	h.env["FAKEACP_ARGS_FILE"] = argsFile
	h.env["FAKEACP_FLAVOR"] = flavor
	return &acpHarness{harness: h, realHome: realHome, argsFile: argsFile, flavor: flavor}
}

func (h *acpHarness) acpAgent(fn func(s *agentconfig.Snapshot)) uuid.UUID {
	return h.agent(func(s *agentconfig.Snapshot) {
		level := "high"
		s.ThinkingLevel = &level
		if h.flavor == "agy" {
			s.Provider, s.ModelID = "antigravity-cli", "fake"
		} else {
			s.Provider, s.ModelID = "grok-cli", "fake-1"
		}
		if fn != nil {
			fn(s)
		}
	})
}

type acpStart struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

// starts returns the protocol starts recorded by fakeacp (version, models and login checks are left out).
func (h *acpHarness) starts() []acpStart {
	h.t.Helper()
	f, err := os.Open(h.argsFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []acpStart
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s acpStart
		if json.Unmarshal(sc.Bytes(), &s) == nil && (slices.Contains(s.Args, "stdio") || slices.Contains(s.Args, "acp")) {
			out = append(out, s)
		}
	}
	return out
}

func (h *acpHarness) recorded(id uuid.UUID) (string, []string) {
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	return h.src.recorded[id], h.src.recordRuntimes[id]
}

// A Grok turn runs in an ACP session of `grok agent ... stdio` with prompts off, the Agent's model and effort, the
// platform's Skills as a plugin directory and the granted plugins' MCP servers; the platform prompt goes with the
// session's first message, and only then. The session id becomes the session; after a restart the session continues
// (loaded, its history not repeated as the reply). Grok uses this computer's login: the real HOME, no xAI key.
func TestGrokTurnSessionAndResume(t *testing.T) {
	h := newACPHarness(t, "grok", nil)
	h.env["XAI_API_KEY"] = "xai-passed-through"
	id := h.acpAgent(func(s *agentconfig.Snapshot) {
		s.Prompt = "Be precise."
		s.Plugins.MCPServers = map[string]json.RawMessage{"probe": json.RawMessage(`{"command":"probe-mcp","args":["--x"]}`)}
	})
	res, sink := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || !strings.HasPrefix(res.Text, "echo: <loop-now-instructions>") || !strings.Contains(res.Text, "Be precise.") ||
		!strings.HasSuffix(res.Text, "</loop-now-instructions>\n\nhello") || sink.text.String() != res.Text {
		t.Fatalf("first turn: %+v", res)
	}
	session, runtimes := h.recorded(id)
	if session == "" || strings.ContainsAny(session, `/\`) || len(runtimes) == 0 || runtimes[0] != "grok" {
		t.Fatalf("the session is the ACP session id, recorded for grok: %q %v", session, runtimes)
	}
	if res, _ := h.exec(id, "hello again"); res.Text != "echo: hello again" {
		t.Fatalf("the platform prompt goes only with the first message: %q", res.Text)
	}
	res, _ = h.exec(id, "fake:config")
	var cfg struct {
		Cwd   string   `json:"cwd"`
		MCP   []string `json:"mcp"`
		Model string   `json:"model"`
		Args  []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil {
		t.Fatal(res.Text, err)
	}
	d := h.e.dirs(id)
	want := []string{"agent", "--always-approve", "--model", "fake-1", "--reasoning-effort", "high", "--plugin-dir", d.acpPlugin, "stdio"}
	if cfg.Cwd != d.workspace || strings.Join(cfg.MCP, ",") != "probe" || !slices.Equal(cfg.Args, want) {
		t.Fatalf("session settings: %+v", cfg)
	}
	for _, f := range []string{".claude-plugin/plugin.json", "skills/loopctl/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(d.acpPlugin, f)); err != nil {
			t.Fatalf("the Skills are in Grok's plugin directory: %v", err)
		}
	}
	starts := h.starts()
	if len(starts) != 1 || starts[0].Env["HOME"] != h.realHome || starts[0].Env["XAI_API_KEY"] != "" {
		t.Fatalf("one process with this computer's login: %+v", starts)
	}

	h.src.update(id, func(s *agentconfig.Snapshot) { s.ConfigVersion++; s.ModelID = "fake-2" })
	if res, _ := h.exec(id, "fake:history"); res.Text != "history=3" || len(h.starts()) != 2 || !slices.Contains(h.starts()[1].Args, "fake-2") {
		t.Fatalf("after a restart with another model the session continues: %+v, %d starts", res, len(h.starts()))
	}
	if res, _ := h.exec(id, "same prompt"); res.Text != "echo: same prompt" {
		t.Fatalf("a continued session that has the prompt does not get it again: %q", res.Text)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.ConfigVersion++; s.Prompt = "Be brief." })
	if res, _ := h.exec(id, "new prompt"); !strings.Contains(res.Text, "Be brief.") || !strings.HasSuffix(res.Text, "new prompt") {
		t.Fatalf("a changed prompt goes with the next message: %q", res.Text)
	}
}

// Antigravity runs as `agy acp --dangerously-skip-permissions`; the model is chosen in the session by the id agy lists
// for the model at the Agent's thinking level (fake at high is fake-high). Antigravity takes no Skill directory: the
// platform prompt lists the Skills and where their SKILL.md is. No Gemini key reaches it.
func TestAntigravityTurnAndModel(t *testing.T) {
	h := newACPHarness(t, "agy", nil)
	h.env["GEMINI_API_KEY"] = "gemini-passed-through"
	id := h.acpAgent(nil)
	res, _ := h.exec(id, "hello")
	d := h.e.dirs(id)
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, filepath.Join(d.acpSkills, "loopctl", "SKILL.md")) || !strings.HasSuffix(res.Text, "hello") {
		t.Fatalf("first turn: %+v", res)
	}
	if session, runtimes := h.recorded(id); session == "" || len(runtimes) == 0 || runtimes[0] != "antigravity" {
		t.Fatalf("recorded for antigravity: %q %v", session, runtimes)
	}
	res, _ = h.exec(id, "fake:config")
	var cfg struct {
		Model string   `json:"model"`
		Args  []string `json:"args"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Model != "fake-high" || !slices.Equal(cfg.Args, []string{"acp", "--dangerously-skip-permissions"}) {
		t.Fatalf("model and arguments: %+v %v", cfg, err)
	}
	if starts := h.starts(); len(starts) != 1 || starts[0].Env["HOME"] != h.realHome || starts[0].Env["GEMINI_API_KEY"] != "" {
		t.Fatalf("environment: %+v", starts)
	}
	low := "low"
	h.src.update(id, func(s *agentconfig.Snapshot) { s.ThinkingLevel = &low })
	res, _ = h.exec(id, "fake:config")
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Model != "fake-low" {
		t.Fatalf("another thinking level after a restart: %+v %v", cfg, err)
	}
	if res, _ := h.exec(id, "fake:history"); res.Text != "history=3" {
		t.Fatalf("the session continues: %q", res.Text)
	}
}

// A recorded session the runtime no longer has is a missing session: Grok fails to load it, Antigravity loads it
// empty.
func TestACPMissingSession(t *testing.T) {
	for _, flavor := range []string{"grok", "agy"} {
		h := newACPHarness(t, flavor, nil)
		id := h.acpAgent(nil)
		h.src.mu.Lock()
		h.src.recorded[id] = "01a20c06-0000-7000-8000-000000000000"
		h.src.mu.Unlock()
		res, _ := h.exec(id, "hello")
		h.src.mu.Lock()
		missing := h.src.missing[id]
		h.src.mu.Unlock()
		if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrSessionMissing || !missing {
			t.Fatalf("%s: missing session: %+v %v", flavor, res, missing)
		}
	}
}

// Stop: Grok's process is ended and the next message continues the session in a new one; Antigravity cancels the
// prompt and keeps the process.
func TestACPStop(t *testing.T) {
	for _, flavor := range []string{"grok", "agy"} {
		h := newACPHarness(t, flavor, nil)
		id := h.acpAgent(nil)
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
			if p != nil && acpOf(p) != nil && acpOf(p).InPrompt() {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the slow turn did not start", flavor)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := h.e.Interrupt(context.Background(), id, runID); err != nil {
			t.Fatal(err)
		}
		select {
		case res := <-results:
			if res.Outcome != executor.OutcomeInterrupted {
				t.Fatalf("%s: stopped: %+v", flavor, res)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the turn did not stop", flavor)
		}
		res, _ := h.exec(id, "fake:history")
		samePID := h.pid(id) == pid
		if res.Outcome != executor.OutcomeCompleted || res.Text != "history=2" || (flavor == "grok") == samePID {
			t.Fatalf("%s: after the stop the session continues (same process: %v): %+v", flavor, samePID, res)
		}
	}
}

// The models each runtime lists for the catalog: Grok's from its initialize, with reasoning efforts; Antigravity's from
// agy models, one id per thinking level, merged into one model with those levels.
func TestACPModels(t *testing.T) {
	g := newACPHarness(t, "grok", nil)
	models, err := g.e.ACPModels(context.Background(), "grok")
	if err != nil || len(models) != 2 || models[0].ID != "fake-1" || models[0].Name != "Fake One" || !slices.Equal(models[0].Efforts, []string{"high", "low"}) ||
		models[0].DefaultEffort != "high" || models[1].ID != "fake-2" || len(models[1].Efforts) != 0 {
		t.Fatalf("Grok: %+v %v", models, err)
	}
	a := newACPHarness(t, "agy", nil)
	models, err = a.e.ACPModels(context.Background(), "antigravity")
	if err != nil || len(models) != 1 || models[0].ID != "fake" || models[0].Name != "Fake" || !slices.Equal(models[0].Efforts, []string{"low", "high"}) || !models[0].LevelIDs || !models[0].Images {
		t.Fatalf("Antigravity: %+v %v", models, err)
	}
}

// Without a login a turn fails as such and names the command to log in.
func TestACPLoginRequired(t *testing.T) {
	for flavor, command := range map[string]string{"grok": "grok login", "agy": "agy acp --login"} {
		h := newACPHarness(t, flavor, nil)
		if err := os.MkdirAll(filepath.Join(h.realHome, ".fakeacp"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.realHome, ".fakeacp", ".logged-out"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if res, _ := h.exec(h.acpAgent(nil), "hello"); res.ErrorCode != executor.ErrRuntimeLoginRequired || !strings.Contains(res.ErrorMessage, command) {
			t.Fatalf("%s: %+v", flavor, res)
		}
	}
}

// A local ACP command a person added runs like the others: its executable with its own arguments and the environment
// they gave it, the program's default model, the Skills listed in the platform prompt; changing the command restarts
// the process and the session continues.
func TestLocalACPCommand(t *testing.T) {
	h := newACPHarness(t, "agy", nil)
	cmdID := uuid.Must(uuid.NewV7())
	set := func(args ...string) {
		agentruntime.SetLocalCommands([]agentruntime.Runtime{agentruntime.LocalCommandRuntime(cmdID, "My Agent",
			agentruntime.LocalCommand{Command: fakeacpbin.Path(t), Args: args, Env: map[string]string{"FAKEACP_PROBE": "given"}})})
	}
	set("acp")
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Provider, s.ModelID = agentruntime.LocalCommandRuntimeID(cmdID), "default"
	})
	res, _ := h.exec(id, "hello")
	d := h.e.dirs(id)
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, filepath.Join(d.acpSkills, "loopctl", "SKILL.md")) || !strings.HasSuffix(res.Text, "hello") {
		t.Fatalf("first turn: %+v", res)
	}
	if session, runtimes := h.recorded(id); session == "" || len(runtimes) == 0 || runtimes[0] != agentruntime.LocalCommandRuntimeID(cmdID) {
		t.Fatalf("recorded for the command's runtime: %q %v", session, runtimes)
	}
	starts := h.starts()
	if len(starts) != 1 || !slices.Equal(starts[0].Args, []string{"acp"}) || starts[0].Env["FAKEACP_PROBE"] != "given" || starts[0].Env["HOME"] != h.realHome {
		t.Fatalf("the command as given: %+v", starts)
	}
	res, _ = h.exec(id, "fake:config")
	var cfg struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Model != "" {
		t.Fatalf("the program's default model is left as it is: %+v %v", cfg, err)
	}
	set("--verbose", "acp")
	if res, _ := h.exec(id, "fake:history"); res.Text != "history=2" || len(h.starts()) != 2 || !slices.Equal(h.starts()[1].Args, []string{"--verbose", "acp"}) {
		t.Fatalf("a changed command restarts and the session continues: %+v %+v", res, h.starts())
	}
}

// The background read of the model lists keeps each runtime's login conclusion (Grok's from grok models, Antigravity's
// from agy models), and a turn corrects it; the status reads it instead of going online.
func TestACPLoginConclusions(t *testing.T) {
	g := newACPHarness(t, "grok", nil)
	if got := g.e.ACPLoginState("grok"); got != "" {
		t.Fatalf("no conclusion before a read: %q", got)
	}
	if _, err := g.e.ACPModels(context.Background(), "grok"); err != nil {
		t.Fatal(err)
	}
	if got := g.e.ACPLoginState("grok"); got != acpcli.LoggedIn {
		t.Fatalf("grok after the read: %q", got)
	}
	if err := os.MkdirAll(filepath.Join(g.realHome, ".fakeacp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.realHome, ".fakeacp", ".logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ := g.exec(g.acpAgent(nil), "hello"); res.ErrorCode != executor.ErrRuntimeLoginRequired || g.e.ACPLoginState("grok") != acpcli.LoggedOut {
		t.Fatalf("a turn that needs a login corrects it: %+v %q", res, g.e.ACPLoginState("grok"))
	}

	a := newACPHarness(t, "agy", nil)
	if err := os.MkdirAll(filepath.Join(a.realHome, ".fakeacp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.realHome, ".fakeacp", ".logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.ACPModels(context.Background(), "antigravity"); err == nil || a.e.ACPLoginState("antigravity") != acpcli.LoggedOut {
		t.Fatalf("agy logged out: %v %q", err, a.e.ACPLoginState("antigravity"))
	}
	if err := os.Remove(filepath.Join(a.realHome, ".fakeacp", ".logged-out")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.e.ACPModels(context.Background(), "antigravity"); err != nil || a.e.ACPLoginState("antigravity") != acpcli.LoggedIn {
		t.Fatalf("agy logged in again: %v %q", err, a.e.ACPLoginState("antigravity"))
	}
}

// In a task instance, Antigravity's commands moved to the background keep the turn going after the prompt returns:
// their ends and the reply agy then gives by itself belong to the turn. A command whose end never comes is stopped and
// closed without a result once the turn has heard nothing for the background wait, and the run record says why.
// Another runtime's tool left open when the turn ends is closed without a result.
func TestACPBackgroundCommands(t *testing.T) {
	h := newACPHarness(t, "agy", func(o *Options) { o.ACPBackgroundWait, o.BackgroundStopWait = 800*time.Millisecond, time.Second })
	id := h.acpAgent(asTask)
	res, sink := h.exec(id, "fake:background 300ms")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "backgrounded\n\nreported 1" || strings.Join(sink.tools, ",") != "execute:started,execute:succeeded" {
		t.Fatalf("background command: %+v %v", res, sink.tools)
	}
	res, sink = h.exec(id, "fake:background lost")
	if res.Outcome != executor.OutcomeCompleted || strings.Join(sink.tools, ",") != "execute:started,execute:no_result" {
		t.Fatalf("lost end: %+v %v", res, sink.tools)
	}
	evs := h.obs.eventsOf(id, store.EventBackgroundWaitTimeout)
	if len(evs) != 1 || evs[0].run == nil || evs[0].ev.Data["limitMs"] != int64(800) {
		t.Fatalf("the run record says why: %+v", evs)
	}
	g := newACPHarness(t, "grok", nil)
	gid := g.acpAgent(nil)
	if res, sink := g.exec(gid, "fake:toolopen sleep 9"); res.Outcome != executor.OutcomeCompleted || strings.Join(sink.tools, ",") != "execute:started,execute:no_result" {
		t.Fatalf("Grok's open tool: %+v %v", res, sink.tools)
	}
}

// An Agent recorded before Antigravity's levels were merged names the model at one level by its full id, with no level
// or off (as do task instances started then): it runs by that id as it is.
func TestAntigravityRecordedLevelID(t *testing.T) {
	h := newACPHarness(t, "agy", nil)
	off := "off"
	for _, level := range []*string{&off, nil} {
		id := h.acpAgent(func(s *agentconfig.Snapshot) { s.ModelID, s.ThinkingLevel = "fake-high", level })
		res, _ := h.exec(id, "fake:config")
		var cfg struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(res.Text), &cfg); err != nil || cfg.Model != "fake-high" {
			t.Fatalf("level %v: %+v %v", level, res, err)
		}
	}
}

// Each turn of Grok, Antigravity or a local ACP command records the usage the CLI reports with the prompt's result: one
// model call on the turn, for the Agent's provider and model. Grok's input count includes its cache reads,
// Antigravity's leaves them out (a local command's is decided by its total); reasoning is part of output; the model Grok
// says it used is the response model.
func TestACPUsage(t *testing.T) {
	cmdID := uuid.Must(uuid.NewV7())
	agentruntime.SetLocalCommands([]agentruntime.Runtime{agentruntime.LocalCommandRuntime(cmdID, "My Agent",
		agentruntime.LocalCommand{Command: fakeacpbin.Path(t), Args: []string{"acp"}})})
	t.Cleanup(func() { agentruntime.SetLocalCommands(nil) })
	local := agentruntime.LocalCommandRuntimeID(cmdID)
	for _, c := range []struct {
		flavor, provider, model, response string
		input, read, output, reasoning    int64
	}{
		{"agy", "antigravity-cli", "fake", "fake", 1200, 800, 30, 12},
		{"grok", "grok-cli", "fake-1", "fake-1-build", 1931 - 217, 217, 31, 30},
		{"agy", local, "default", "default", 1200, 800, 30, 12},
	} {
		h := newACPHarness(t, c.flavor, nil)
		id := h.agent(func(s *agentconfig.Snapshot) { s.Provider, s.ModelID = c.provider, c.model })
		run, res := h.execRun(id, "hello")
		h.obs.mu.Lock()
		usage := slices.Clone(h.obs.usage)
		h.obs.mu.Unlock()
		if res.Outcome != executor.OutcomeCompleted || len(usage) != 1 {
			t.Fatalf("%s: %+v %+v", c.provider, res, usage)
		}
		u := usage[0]
		if u.agent != id || u.run == nil || *u.run != run || u.u.Kind != "assistant" || u.u.Provider != c.provider || u.u.Model != c.model ||
			u.u.ResponseModel != c.response || u.u.StopReason != "end_turn" || u.u.Input != c.input || u.u.CacheRead != c.read ||
			u.u.CacheWrite != 0 || u.u.Output != c.output || u.u.Reasoning == nil || *u.u.Reasoning != c.reasoning {
			t.Fatalf("%s: %+v run %v", c.provider, u.u, u.run)
		}
	}
}

func count(v int64) *int64 { return &v }

// How the counts an ACP agent reports become a usage record: the input leaves out the cached part, recorded as cache
// reads and writes. Antigravity's input never has it, Grok's always does, and another agent's has it when its input
// and output alone make its total. Reasoning is part of output.
func TestACPUsageRecord(t *testing.T) {
	other := acpFlavor{runtime: "acp-other"}
	for _, c := range []struct {
		name                       string
		f                          acpFlavor
		u                          acp.Usage
		input, read, write, output int64
		reasoning                  *int64
		response                   string
	}{
		{"agy, even when input and output make the total", antigravityFlavor, acp.Usage{Input: 1200, Output: 30, CacheRead: 800, Total: count(1230), Thought: count(12)},
			1200, 800, 0, 30, count(12), "m"},
		{"grok 1.0.92", grokFlavor, acp.Usage{Input: 19319, Output: 31, CacheRead: 2176, Total: count(19350), Thought: count(30), Model: "grok-4.7-build"},
			17143, 2176, 0, 31, count(30), "grok-4.7-build"},
		{"grok without a total", grokFlavor, acp.Usage{Input: 100, Output: 5, CacheRead: 40, CacheWrite: 10}, 50, 40, 10, 5, nil, "m"},
		{"grok, more cache than input", grokFlavor, acp.Usage{Input: 10, CacheRead: 40}, 0, 40, 0, 0, nil, "m"},
		{"another agent: in the input", other, acp.Usage{Input: 100, Output: 5, CacheRead: 40, Total: count(105)}, 60, 40, 0, 5, nil, "m"},
		{"another agent: beside the input", other, acp.Usage{Input: 100, Output: 5, CacheRead: 40, Total: count(145)}, 100, 40, 0, 5, nil, "m"},
		{"another agent: no total", other, acp.Usage{Input: 100, Output: 5, CacheRead: 40}, 100, 40, 0, 5, nil, "m"},
		{"reasoning over output", other, acp.Usage{Input: 1, Output: 5, Thought: count(9)}, 1, 0, 0, 5, count(5), "m"},
	} {
		r := c.f.usageRecord(c.u, "prov", "m")
		if r.Kind != "assistant" || r.Provider != "prov" || r.Model != "m" || r.ResponseModel != c.response || r.Input != c.input ||
			r.CacheRead != c.read || r.CacheWrite != c.write || r.Output != c.output || (r.Reasoning == nil) != (c.reasoning == nil) ||
			(r.Reasoning != nil && *r.Reasoning != *c.reasoning) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}
