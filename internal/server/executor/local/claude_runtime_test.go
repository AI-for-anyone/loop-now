package local

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/fakeclaudebin"
)

// claudeHarness is an executor that runs the Claude Code runtime on fakeclaude: the real HOME is replaced by a temp
// directory (with the user's own files put in it first), fakeclaude's session records are written under its
// .claude/projects, and the startup arguments are recorded in argsFile.
type claudeHarness struct {
	*harness
	realHome string
	before   map[string]string
	argsFile string
	logs     *bytes.Buffer
}

func newClaudeHarness(t *testing.T, mutate func(o *Options)) *claudeHarness {
	t.Helper()
	realHome, before := fakeRealHome(t)
	argsFile := filepath.Join(t.TempDir(), "args.jsonl")
	h := newHarness(t, func(o *Options) {
		o.ClaudeBin = fakeclaudebin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
		o.Passthrough = append(o.Passthrough, "FAKECLAUDE_ARGS_FILE", "FAKECLAUDE_EXIT_AT_START", "FAKECLAUDE_EXTRA_SKILLS", "FAKECLAUDE_NO_TRANSCRIPT", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDECODE")
		if mutate != nil {
			mutate(o)
		}
	}, "")
	h.env["FAKECLAUDE_ARGS_FILE"] = argsFile
	logs := &bytes.Buffer{}
	h.e.log = slog.New(slog.NewTextHandler(logs, nil))
	return &claudeHarness{harness: h, realHome: realHome, before: before, argsFile: argsFile, logs: logs}
}

func (h *claudeHarness) claudeAgent(fn func(s *agentconfig.Snapshot)) uuid.UUID {
	return h.agent(func(s *agentconfig.Snapshot) {
		s.Provider, s.ModelID = "claude-code", "sonnet"
		level := "high"
		s.ThinkingLevel = &level
		if fn != nil {
			fn(s)
		}
	})
}

// launches returns the arguments of each fakeclaude start.
func (h *claudeHarness) launches() [][]string {
	h.t.Helper()
	f, err := os.Open(h.argsFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var args []string
		if json.Unmarshal(sc.Bytes(), &args) == nil {
			out = append(out, args)
		}
	}
	return out
}

func (h *claudeHarness) claudePid(id uuid.UUID) int {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	if p := h.e.procs[id]; p != nil {
		return p.rt.Pid()
	}
	return 0
}

func hasFlag(args []string, flag string) bool { return slices.Contains(args, flag) }

// A turn's arguments, session record, usage, reuse, and continuing after a restart:
// the arguments follow the platform's decisions (do not read the user's settings and MCP, auto memory off,
// bypassPermissions without getting stuck on approvals, system prompt not frozen, the platform's plugin directory and
// tool allowlist); the session record is the file claude itself writes under .claude/projects in the real HOME; after a
// restart caused by a config change, --resume continues the session.
func TestClaudeRuntimeTurnSessionAndResume(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	res, sink := h.exec(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "echo: hello" || sink.text.String() != "echo: hello" {
		t.Fatalf("first turn: %+v %q", res, sink.text.String())
	}
	launches := h.launches()
	if len(launches) != 1 {
		t.Fatalf("should start once: %v", launches)
	}
	args, d := launches[0], h.e.dirs(id)
	want := map[string]string{"--model": "sonnet", "--effort": "high", "--setting-sources": "", "--settings": claudeSettings,
		"--permission-mode": "bypassPermissions", "--permission-prompts": "none", "--system-prompt-snapshot": "off",
		"--plugin-dir": d.claudePlugin, "--tools": strings.Join(ClaudeCodeTools, ","), "--input-format": "stream-json", "--output-format": "stream-json"}
	for flag, v := range want {
		if !hasFlag(args, flag) || argValue(args, flag) != v {
			t.Errorf("argument %s = %q, want %q", flag, argValue(args, flag), v)
		}
	}
	for _, flag := range []string{"-p", "--verbose", "--include-partial-messages", "--strict-mcp-config"} {
		if !hasFlag(args, flag) {
			t.Errorf("missing argument %s", flag)
		}
	}
	if hasFlag(args, "--mcp-config") || hasFlag(args, "--resume") {
		t.Errorf("no MCP and no old session expected: %v", args)
	}
	sid := argValue(args, "--session-id")
	if !claudecli.ValidSessionID(sid) {
		t.Fatalf("session ID: %q", sid)
	}
	transcript := filepath.Join(claudecli.DefaultProjectsDir(h.realHome), claudecli.ProjectDirName(d.workspace), sid+".jsonl")
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if recorded != transcript {
		t.Fatalf("session transcript: %q, want %q", recorded, transcript)
	}
	h.obs.mu.Lock()
	var usage []executor.Usage
	for _, u := range h.obs.usage {
		usage = append(usage, u.u)
	}
	h.obs.mu.Unlock()
	if len(usage) != 1 || usage[0].Provider != "claude-code" || usage[0].Model != "claude-sonnet-fake" || usage[0].Input != 10 || usage[0].Kind != "assistant" {
		t.Fatalf("usage: %+v", usage)
	}
	h.obs.mu.Lock()
	startedRuntime := ""
	for _, ev := range h.obs.events {
		if ev.ev.Type == store.EventPiStarted {
			startedRuntime, _ = ev.ev.Data["runtime"].(string)
		}
	}
	h.obs.mu.Unlock()
	if startedRuntime != "claude-code" {
		t.Fatalf("the process start event should record the runtime: %q", startedRuntime)
	}
	pid := h.claudePid(id)
	if res, _ = h.exec(id, "fake:history"); res.Text != "history=1" || h.claudePid(id) != pid {
		t.Fatalf("reuse the same process: %+v", res)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	if res, _ = h.exec(id, "fake:history"); res.Text != "history=2" || h.claudePid(id) == pid {
		t.Fatalf("restart and resume after a config change: %+v", res)
	}
	if args := h.launches()[1]; argValue(args, "--resume") != sid || hasFlag(args, "--session-id") {
		t.Fatalf("after the restart the original session should be resumed with --resume: %v", args)
	}
	if b, _ := os.ReadFile(d.prompt); !strings.Contains(string(b), "changed") {
		t.Fatalf("the appended prompt should be the new one: %s", b)
	}
}

// The environment given to claude: the real HOME, the Agent's TMPDIR, the platform's PATH; Anthropic/Claude variables
// are removed whether they come from pass-through, CLI credentials or plugins, and there are no Pi directory variables.
func TestClaudeEnvStripsAnthropicAndClaude(t *testing.T) {
	loopctlDir := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.LoopctlBinDir = loopctlDir })
	h.env["ANTHROPIC_API_KEY"] = "sk-from-passthrough"
	h.env["CLAUDE_CODE_USE_BEDROCK"] = "1"
	h.env["CLAUDECODE"] = "1"
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"mycli"}
		s.CLIPayloads["mycli"] = clitemplate.Payload{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://evil.example", "MYCLI_TOKEN": "kept"}}
		s.Plugins.Env = map[string]string{"CLAUDE_CONFIG_DIR": "/evil", "ANTHROPIC_AUTH_TOKEN": "plugin-token", "PLUGIN_SETTING": "kept"}
	})
	res, _ := h.exec(id, "fake:env")
	var env struct {
		HOME, TMPDIR, PATH, PI_CODING_AGENT_DIR string
		Forbidden                               []string `json:"forbidden"`
	}
	if res.Outcome != executor.OutcomeCompleted || json.Unmarshal([]byte(res.Text), &env) != nil {
		t.Fatalf("environment: %+v", res)
	}
	d := h.e.dirs(id)
	if env.HOME != h.realHome || env.TMPDIR != d.tmp || env.PI_CODING_AGENT_DIR != "" || !strings.HasPrefix(env.PATH, loopctlDir+string(os.PathListSeparator)) {
		t.Fatalf("directories and PATH: %+v", env)
	}
	if len(env.Forbidden) != 0 {
		t.Fatalf("Anthropic/Claude variables must not be given to claude: %v", env.Forbidden)
	}
	// claude's own short commands (auth status etc.) get the same base variables as the main process: USER/LOGNAME
	// (claude finds the login by them) and the passed-through LANG.
	h.env["LANG"] = "zh_CN.UTF-8"
	helper := strings.Join(h.e.ClaudeHelperEnv(context.Background(), h.realHome), "\n")
	for _, want := range []string{"USER=tester", "LOGNAME=tester", "LANG=zh_CN.UTF-8", "HOME=" + h.realHome} {
		if !strings.Contains(helper, want) {
			t.Errorf("the helper command environment is missing %s: %s", want, helper)
		}
	}
	for _, kv := range strings.Split(helper, "\n") {
		if strings.HasPrefix(kv, "ANTHROPIC_") || strings.HasPrefix(kv, "CLAUDE") {
			t.Errorf("the helper command environment should carry no Anthropic/Claude variables: %s", kv)
		}
	}
	all := claudeEnv([]string{"A=1", "ANTHROPIC_API_KEY=x", "CLAUDE_CONFIG_DIR=y", "CLAUDECODE=1", "CLAUDE_CODE_OAUTH_TOKEN=z", "PI_OFFLINE=1", "MYCLI_TOKEN=kept"})
	if strings.Join(all, ",") != "A=1,MYCLI_TOKEN=kept" {
		t.Fatalf("claudeEnv: %v", all)
	}
}

// The platform writes nothing in the real HOME: apart from the session records claude writes itself (under
// .claude/projects), the user's files are untouched and the managed CLIs' credential files are in the Agent's separate
// HOME; release and startup cleanup only touch the Agent's own directories.
func TestClaudeNeverTouchesRealHomeBesidesItsOwnTranscript(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"mycli"}
		s.CLIPayloads["mycli"] = clitemplate.Payload{Files: []clitemplate.PayloadFile{{Path: ".mycli/config.json", Content: []byte(`{"agent":true}`)}}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution: %+v", res)
	}
	d := h.e.dirs(id)
	if b, err := os.ReadFile(filepath.Join(d.home, ".mycli", "config.json")); err != nil || string(b) != `{"agent":true}` {
		t.Fatalf("the credential file should be in the Agent's own HOME: %q %v", b, err)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	h.src.mu.Lock()
	h.src.deleted = append(h.src.deleted, id)
	h.src.mu.Unlock()
	if err := h.e.CleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, h.realHome)
	prefix := filepath.ToSlash(filepath.Join(".claude", "projects", claudecli.ProjectDirName(d.workspace))) + "/"
	claudeOwn := func(k string) bool { // claude's own session records, the parent dirs it made for them, and fakeclaude's purge call log
		return strings.HasPrefix(k, prefix) || k == ".claude/" || k == ".claude/projects/" || k == ".fakeclaude-purge.log"
	}
	for k := range after {
		if _, ok := h.before[k]; !ok && !claudeOwn(k) {
			t.Errorf("extra %s in the real HOME", k)
		}
	}
	for k, v := range h.before {
		if after[k] != v {
			t.Errorf("%s in the real HOME was changed or deleted", k)
		}
	}
}

// What the platform gives Claude Code: the plugin directory holds loopctl, the Agent's Skills and the plugins' Skills;
// the plugins' MCP servers go through --mcp-config; the appended prompt holds the Agent's Prompt, the verbatim entry of
// its own folder and the project entry (with a note that they are read when Claude Code starts); Pi's model files are
// not written.
func TestClaudeSkillsMCPAndPrompt(t *testing.T) {
	contextDir := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.ContextDir = contextDir })
	pluginSkill := filepath.Join(t.TempDir(), "plugin-skill")
	if err := os.MkdirAll(pluginSkill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginSkill, "SKILL.md"), []byte("---\nname: plugin-skill\ndescription: d\n---\nplugin body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := h.src.blob("---\nname: review\ndescription: d\n---\nreview body\n")
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.Prompt = "You are the reviewer."
		s.Skills = []agentconfig.SkillSnapshot{{Name: "review", Files: []store.SkillFile{{Path: "SKILL.md", SHA256: skill}}}}
		s.Plugins = agentconfig.PluginAssets{Skills: []string{pluginSkill}, MCPServers: map[string]json.RawMessage{"browser": json.RawMessage(`{"command":"/bin/true"}`)}}
	})
	d := h.e.dirs(id)
	if err := os.MkdirAll(d.workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.workspace, "AGENTS.md"), []byte("OWN-ENTRY-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution: %+v", res)
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "skills/loopctl/SKILL.md", "skills/review/SKILL.md", "skills/plugin-skill/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(d.claudePlugin, filepath.FromSlash(rel))); err != nil {
			t.Errorf("the plugin directory is missing %s", rel)
		}
	}
	args := h.launches()[0]
	if argValue(args, "--mcp-config") != d.claudeMCP {
		t.Fatalf("MCP: %v", args)
	}
	if b, _ := os.ReadFile(d.claudeMCP); !strings.Contains(string(b), `"browser"`) {
		t.Fatalf("MCP file: %s", b)
	}
	if _, err := os.Stat(filepath.Join(d.pi, "mcp.json")); !os.IsNotExist(err) {
		t.Fatal("Pi's mcp.json should not be written")
	}
	prompt, _ := os.ReadFile(d.prompt)
	for _, want := range []string{"You are the reviewer.", "OWN-ENTRY-MARKER", `<context_entry path="` + filepath.Join(d.workspace, "AGENTS.md") + `">`, "its content is included below if it exists", "loaded only when Claude Code starts"} {
		if !strings.Contains(string(prompt), want) {
			t.Errorf("the appended prompt is missing %q:\n%s", want, prompt)
		}
	}
	if argValue(args, "--append-system-prompt-file") != d.prompt {
		t.Fatalf("appended prompt file: %v", args)
	}
	for _, f := range []string{"auth.json", "models.json"} {
		if _, err := os.Stat(filepath.Join(d.pi, f)); !os.IsNotExist(err) {
			t.Errorf("the Claude Code runtime should not write Pi's %s", f)
		}
	}
}

// Failures: claude not found, exiting right at startup (with the version and error output), not logged in (decided by
// claude auth status; the email and organization in its output do not go into messages, events or logs), an API error
// while logged in is still runtime_error, and the process crashing during a turn.
func TestClaudeFailures(t *testing.T) {
	missing := newClaudeHarness(t, func(o *Options) { o.ClaudeBin = filepath.Join(t.TempDir(), "no-claude") })
	if res, _ := missing.exec(missing.claudeAgent(nil), "hi"); res.ErrorCode != executor.ErrRuntimeUnavailable {
		t.Fatalf("claude not found: %+v", res)
	}

	h := newClaudeHarness(t, nil)
	h.env["FAKECLAUDE_EXIT_AT_START"] = "1"
	id := h.claudeAgent(nil)
	res, _ := h.exec(id, "hi")
	if res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(res.ErrorMessage, "9.9.9") || !strings.Contains(res.ErrorMessage, "unknown option") {
		t.Fatalf("exit right at startup: %+v", res)
	}
	delete(h.env, "FAKECLAUDE_EXIT_AT_START")

	if res, _ = h.exec(id, "fake:notloggedin"); res.ErrorCode != executor.ErrRuntimeError {
		t.Fatalf("should still be runtime_error when auth status reports logged in: %+v", res)
	}
	if err := os.WriteFile(filepath.Join(h.realHome, ".fakeclaude-logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if res, _ = h.exec(id, "fake:notloggedin"); res.ErrorCode != executor.ErrRuntimeLoginRequired || !strings.Contains(res.ErrorMessage, "not logged in") {
		t.Fatalf("not logged in: %+v", res)
	}
	res, _ = h.exec(id, "fake:exit")
	if res.ErrorCode != executor.ErrRuntimeExited || !strings.Contains(res.ErrorMessage, "Claude Code 9.9.9 process exited unexpectedly") || !strings.Contains(res.ErrorMessage, "crashing on purpose") {
		t.Fatalf("crash: %+v", res)
	}
	if res, _ = h.exec(id, "after crash"); res.Text != "echo: after crash" {
		t.Fatalf("the next message after a crash should start a new process: %+v", res)
	}
	h.obs.mu.Lock()
	events, _ := json.Marshal(h.obs.events)
	h.obs.mu.Unlock()
	for _, secret := range []string{"private@example.com", "Private Org", "org-private-id"} {
		if strings.Contains(string(events), secret) || strings.Contains(h.logs.String(), secret) || strings.Contains(res.ErrorMessage, secret) {
			t.Fatalf("%q from the auth status output must not reach events, logs or error messages", secret)
		}
	}
}

// Interrupt: when the control message is confirmed, the process stays and the next message reuses it; when no
// confirmation comes and SIGINT is sent instead, that process exits afterwards and the very next message goes through a
// restart (--resume), without an error, without being lost, and without being recorded as an unexpected exit.
func TestClaudeInterruptThenNextMessage(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	interrupt := func(msg string) executor.Result {
		t.Helper()
		run := uuid.Must(uuid.NewV7())
		sink := newSink()
		done := make(chan executor.Result, 1)
		go func() {
			res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: msg}, sink)
			if err != nil {
				t.Error(err)
			}
			done <- res
		}()
		select {
		case <-sink.gotText:
		case <-time.After(15 * time.Second):
			t.Fatal("no output")
		}
		if err := h.e.Interrupt(context.Background(), id, run); err != nil {
			t.Fatal(err)
		}
		return <-done
	}
	if res := interrupt("fake:slow"); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("interrupt by control message: %+v", res)
	}
	pid := h.claudePid(id)
	if res, _ := h.exec(id, "fake:history"); res.Text != "history=1" || h.claudePid(id) != pid {
		t.Fatalf("the same process should be reused after a confirmed interrupt: %+v", res)
	}
	if res := interrupt("fake:noack"); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("SIGINT interrupt: %+v", res)
	}
	res, _ := h.exec(id, "fake:history")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "history=3" {
		t.Fatalf("the next message right after the interrupt should resume in a new process: %+v", res)
	}
	if args := h.launches()[len(h.launches())-1]; !hasFlag(args, "--resume") {
		t.Fatalf("the new process should use --resume: %v", args)
	}
	h.obs.mu.Lock()
	defer h.obs.mu.Unlock()
	for _, ev := range h.obs.events {
		if ev.ev.Type == store.EventPiExited {
			t.Fatalf("the exit after SIGINT is the platform's doing and should not be recorded as an unexpected exit: %+v", ev.ev.Data)
		}
	}
}

// A task instance's first start: fork from the baseline the platform copied in a stable state (--resume <baseline file>
// --fork-session, with the new session ID given by the platform).
func TestClaudeTaskFork(t *testing.T) {
	h := newClaudeHarness(t, nil)
	baseline := filepath.Join(t.TempDir(), "baseline.jsonl")
	if err := os.WriteFile(baseline, []byte("{\"type\":\"user\",\"text\":\"a\"}\n{\"type\":\"user\",\"text\":\"b\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := h.claudeAgent(func(s *agentconfig.Snapshot) { s.ForkSessionFile = baseline })
	res, _ := h.exec(id, "fake:history")
	if res.Text != "history=2" {
		t.Fatalf("the fork should carry the history from the baseline: %+v", res)
	}
	args := h.launches()[0]
	if argValue(args, "--resume") != baseline || !hasFlag(args, "--fork-session") || !claudecli.ValidSessionID(argValue(args, "--session-id")) {
		t.Fatalf("fork arguments: %v", args)
	}
}

// Switching from Pi to Claude Code: when the old Pi process stops, it does not record its session file back (the
// session in the database has been reset).
func TestClaudeRuntimeSwitchDoesNotRecordOldSession(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.agent(nil)
	if res, _ := h.exec(id, "pi turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Pi: %+v", res)
	}
	h.src.mu.Lock()
	piSession := h.src.recorded[id]
	h.src.recorded[id] = "" // switching runtimes has reset the session in the database
	h.src.recordLog[id] = nil
	h.src.mu.Unlock()
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	p.sessMu.Lock()
	p.recorded = "" // give the old process "something to record" when it stops: if not blocked, it would record the Pi session back
	p.sessMu.Unlock()
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Provider, s.ModelID = "claude-code", "haiku" })
	if res, _ := h.exec(id, "claude turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Claude Code: %+v", res)
	}
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	if slices.Contains(h.src.recordLog[id], piSession) || !strings.HasSuffix(h.src.recorded[id], ".jsonl") || h.src.recorded[id] == piSession {
		t.Fatalf("recorded sessions: %v (Pi's is %s)", h.src.recordLog[id], piSession)
	}
}

// A saved session is recorded with the runtime that wrote it, so the platform can tell a late save by the runtime the
// Agent changed away from (a reply that was in progress during the change) from the current runtime's session.
func TestSessionRecordedWithItsRuntime(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.agent(nil)
	if res, _ := h.exec(id, "pi turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Pi: %+v", res)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Provider, s.ModelID = "claude-code", "haiku" })
	if res, _ := h.exec(id, "claude turn"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Claude Code: %+v", res)
	}
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	if got := h.src.recordRuntimes[id]; len(got) < 2 || got[0] != "pi" || got[len(got)-1] != "claude-code" {
		t.Fatalf("runtimes the sessions were recorded with: %v", got)
	}
}

// The conversation block the platform puts before the first message after a change of runtime reaches the runtime as
// it is, on Pi and on Claude Code, and the reply reports who ran the turn.
func TestHandoffBlockReachesTheRuntime(t *testing.T) {
	h := newClaudeHarness(t, nil)
	id := h.agent(nil)
	message := executor.RunIDLine(uuid.Must(uuid.NewV7())) + "<conversation-handoff>\n[user · message 1]\nremember the word apple\n\n</conversation-handoff>\n\nwhat was the word?"
	res, sink := h.exec(id, message)
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, "remember the word apple") || !strings.Contains(res.Text, "what was the word?") {
		t.Fatalf("Pi: %+v", res)
	}
	if sink.selection.Runtime != "pi" {
		t.Fatalf("Pi turn reported as %+v", sink.selection)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Provider, s.ModelID = "claude-code", "haiku" })
	res, sink = h.exec(id, message)
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, "<conversation-handoff>") || !strings.Contains(res.Text, "remember the word apple") {
		t.Fatalf("Claude Code: %+v", res)
	}
	if sink.selection.Runtime != "claude-code" || sink.selection.Provider != "claude-code" || sink.selection.ModelID != "haiku" {
		t.Fatalf("Claude Code turn reported as %+v", sink.selection)
	}
}

// purgeLog returns the purge calls logged by fakeclaude.
func (h *claudeHarness) purgeLog() []string {
	b, _ := os.ReadFile(filepath.Join(h.realHome, ".fakeclaude-purge.log"))
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Release (deleting an Agent, retiring a task instance) cleans up the data Claude Code left for the workspace: first
// the session records are archived into the Agent's sessions/claude, then claude purge <workspace> -y runs. The target
// can only be exactly this Agent's workspace: for a bound local directory, another Agent's workspace or a path outside
// the work root nothing is run; claude is not started for an Agent that never used Claude Code; when claude is not
// found the step is skipped and logged; when archiving fails, there is no purge.
func TestClaudePurgeOnRelease(t *testing.T) {
	h := newClaudeHarness(t, nil)
	localDir := t.TempDir()
	id := h.claudeAgent(func(s *agentconfig.Snapshot) {
		s.Local = agentconfig.LocalAccess{Directories: []agentconfig.LocalDirectory{{Path: localDir, Note: "user files"}}}
	})
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution: %+v", res)
	}
	d := h.e.dirs(id)
	h.src.mu.Lock()
	transcript := h.src.recorded[id]
	h.src.mu.Unlock()
	pi := h.agent(nil)
	if res, _ := h.exec(pi, "pi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("Pi: %+v", res)
	}
	if err := h.e.Release(context.Background(), pi); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 0 {
		t.Fatalf("an Agent that never used Claude Code should not be cleaned up: %v", got)
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 1 || got[0] != "purge "+d.workspace+" -y" {
		t.Fatalf("purge calls: %v (workspace %s)", got, d.workspace)
	}
	want, _ := os.ReadFile(transcript)
	if got, err := os.ReadFile(filepath.Join(d.sessions, "claude", filepath.Base(transcript))); err != nil || !bytes.Equal(got, want) || len(want) == 0 {
		t.Fatalf("the session transcript should be archived before the purge: %v", err)
	}
	bin := fakeclaudebin.Path(t)
	env := h.e.claudeHelperEnv(context.Background(), h.realHome)
	for name, path := range map[string]string{"bound local directory": localDir, "another Agent's workspace": h.e.dirs(pi).workspace, "outside the work root": filepath.Join(t.TempDir(), "agents", id.String(), "workspace"), "relative path": "workspace"} {
		if err := h.e.purgeClaudeWorkspace(context.Background(), bin, env, id, path); err == nil {
			t.Errorf("%s must not become a purge target", name)
		}
	}
	if got := h.purgeLog(); len(got) != 1 {
		t.Fatalf("a rejected path should not run claude: %v", got)
	}

	// claude not found: skip and log.
	missing := newClaudeHarness(t, nil)
	other := missing.claudeAgent(nil)
	missing.e.o.ClaudeBin = filepath.Join(t.TempDir(), "no-claude")
	if err := missing.e.Release(context.Background(), other); err != nil || !strings.Contains(missing.logs.String(), "claude not found") {
		t.Fatalf("when claude is not found the step should be skipped and logged: %v %s", err, missing.logs.String())
	}
}

// When archiving fails (the session records cannot be read), there is no purge: better to keep them than to lose
// records.
func TestClaudePurgeSkippedWhenArchiveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files without permission")
	}
	h := newClaudeHarness(t, nil)
	id := h.claudeAgent(nil)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution: %+v", res)
	}
	h.src.mu.Lock()
	transcript := h.src.recorded[id]
	h.src.mu.Unlock()
	if err := os.Chmod(transcript, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(transcript, 0o600) })
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := h.purgeLog(); len(got) != 0 {
		t.Fatalf("there should be no purge when archiving fails: %v", got)
	}
	if !strings.Contains(h.logs.String(), "failed to archive the Claude Code session transcript") {
		t.Fatalf("should log: %s", h.logs.String())
	}
}

// The only Skills an Agent sees are the platform's: --settings turns off bundled Skills (disableBundledSkills) and sets
// the few that cannot be turned off to off; after startup the Skills the process reports are checked, and any not
// starting with loop-now: produce one warning log (once per process) without blocking the run.
func TestClaudeSkillsVisibleToAgent(t *testing.T) {
	h := newClaudeHarness(t, nil)
	clean := h.claudeAgent(nil)
	if res, _ := h.exec(clean, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution: %+v", res)
	}
	var settings struct {
		AutoMemoryEnabled    *bool             `json:"autoMemoryEnabled"`
		DisableBundledSkills bool              `json:"disableBundledSkills"`
		SkillOverrides       map[string]string `json:"skillOverrides"`
	}
	if err := json.Unmarshal([]byte(argValue(h.launches()[0], "--settings")), &settings); err != nil || settings.AutoMemoryEnabled == nil || *settings.AutoMemoryEnabled ||
		!settings.DisableBundledSkills || len(settings.SkillOverrides) != len(ClaudeCodeHiddenSkills) {
		t.Fatalf("--settings: %+v %v", settings, err)
	}
	for _, name := range ClaudeCodeHiddenSkills {
		if settings.SkillOverrides[name] != "off" {
			t.Errorf("bundled skill %s should be set to off", name)
		}
	}
	if strings.Contains(h.logs.String(), "Skills from outside the platform") {
		t.Fatalf("no warning should be logged when there are only platform skills: %s", h.logs.String())
	}
	h.env["FAKECLAUDE_EXTRA_SKILLS"] = "update-config,dataviz"
	drifted := h.claudeAgent(nil)
	for range 2 {
		if res, _ := h.exec(drifted, "hi"); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("extra skills should not block the run: %+v", res)
		}
	}
	logs := h.logs.String()
	if strings.Count(logs, "Skills from outside the platform") != 1 || !strings.Contains(logs, "update-config") || !strings.Contains(logs, "dataviz") || strings.Contains(logs, "loop-now:loopctl") {
		t.Fatalf("extra skills should log one warning (listing only the extra ones): %s", logs)
	}
}

// The session record written by claude is not found (for example on Windows, where it encodes the working directory
// differently from the platform): the turn completes normally without reporting
// "Could not save the session record."; one warning is logged; the conversation continues as usual
// in this process, and after a restart a new session is opened (--session-id, not --resume).
func TestClaudeTranscriptNotFoundDegrades(t *testing.T) {
	h := newClaudeHarness(t, nil)
	h.env["FAKECLAUDE_NO_TRANSCRIPT"] = "1"
	id := h.claudeAgent(nil)
	for _, msg := range []string{"hi", "fake:history"} {
		if res, _ := h.exec(id, msg); res.Outcome != executor.OutcomeCompleted {
			t.Fatalf("%s: %+v", msg, res)
		}
	}
	if n := strings.Count(h.logs.String(), "Claude Code session transcript not found"); n != 1 {
		t.Fatalf("should log one warning, logged %d times: %s", n, h.logs.String())
	}
	h.src.mu.Lock()
	recorded := h.src.recorded[id]
	h.src.mu.Unlock()
	if recorded != "" {
		t.Fatalf("there should be no session transcript to record: %q", recorded)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	if res, _ := h.exec(id, "after restart"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("after the restart: %+v", res)
	}
	if args := h.launches()[1]; hasFlag(args, "--resume") || !claudecli.ValidSessionID(argValue(args, "--session-id")) {
		t.Fatalf("a new session should be opened after the restart: %v", args)
	}
}
