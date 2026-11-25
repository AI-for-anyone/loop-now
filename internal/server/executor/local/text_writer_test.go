package local

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/testutil/fakeacpbin"
)

func writeCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func levelOf(l string) *string { return &l }

// textFailure returns err as a *executor.TextFailure, failing the test when it is not one.
func textFailure(t *testing.T, err error) *executor.TextFailure {
	t.Helper()
	var f *executor.TextFailure
	if !errors.As(err, &f) {
		t.Fatalf("want a TextFailure, got %v", err)
	}
	return f
}

// emptyDir fails the test when dir holds anything: each run's folder is removed afterwards.
func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the temporary folders are not removed: %v %v", entries, err)
	}
}

// argAfter returns the argument after flag; "" and false when flag is missing.
func argAfter(args []string, flag string) (string, bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

// Claude Code writes with claude -p in an empty temporary folder: the instructions as the system prompt and the rest on
// stdin, the chosen model and effort, no tools, Skills, MCP servers, settings files, hooks or saved session. The folder
// is removed and the real HOME is left as it was.
func TestClaudeWritesText(t *testing.T) {
	tmp := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.TextTempDir = tmp })
	req := executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", ThinkingLevel: levelOf("low"), Instructions: "Write a Prompt.", Prompt: "fake:args"}
	out, err := h.e.WriteText(writeCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal([]byte(out), &args); err != nil {
		t.Fatalf("args: %q %v", out, err)
	}
	for flag, want := range map[string]string{"--output-format": "json", "--model": "haiku", "--effort": "low", "--system-prompt": "Write a Prompt.",
		"--tools": "", "--setting-sources": "", "--permission-mode": "dontAsk"} {
		if got, ok := argAfter(args, flag); !ok || got != want {
			t.Errorf("%s: %q, want %q (%v)", flag, got, want, args)
		}
	}
	for _, flag := range []string{"-p", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"} {
		if !hasFlag(args, flag) {
			t.Errorf("missing %s: %v", flag, args)
		}
	}
	for _, flag := range []string{"--plugin-dir", "--mcp-config", "--append-system-prompt-file", "--resume", "--session-id"} {
		if hasFlag(args, flag) {
			t.Errorf("unexpected %s: %v", flag, args)
		}
	}
	settings, _ := argAfter(args, "--settings")
	var s map[string]any
	if json.Unmarshal([]byte(settings), &s) != nil || s["disableAllHooks"] != true || s["autoMemoryEnabled"] != false {
		t.Errorf("settings: %s", settings)
	}
	if out, err := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", Instructions: "x", Prompt: "fake:stdin\nmore"}); err != nil || out != "fake:stdin\nmore" {
		t.Fatalf("the rest goes on stdin: %q %v", out, err)
	}
	if out, _ := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", Instructions: "x", Prompt: "fake:args"}); strings.Contains(out, "--effort") {
		t.Fatalf("no level, no effort: %s", out)
	}
	emptyDir(t, tmp)
	sameTree(t, h.realHome, h.before)

	f := textFailure(t, func() error {
		_, err := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", Prompt: "fake:error You've hit your usage limit"})
		return err
	}())
	if f.Unavailable || !strings.Contains(f.Message, "You've hit your usage limit") || !strings.Contains(f.Message, "Claude Code") {
		t.Fatalf("the CLI's own reason: %+v", f)
	}
	emptyDir(t, tmp)
}

// Stopping the request stops the CLI at once and removes the folder; a CLI that is not on this computer is unavailable.
func TestTextWriterStopsAndMissingCLI(t *testing.T) {
	tmp := t.TempDir()
	h := newClaudeHarness(t, func(o *Options) { o.TextTempDir = tmp })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	_, err := h.e.WriteText(ctx, executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", Prompt: "fake:slow"})
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("a stop ends the run at once: %v after %v", err, time.Since(start))
	}
	emptyDir(t, tmp)

	missing := newClaudeHarness(t, func(o *Options) { o.TextTempDir = tmp; o.ClaudeBin = filepath.Join(tmp, "nowhere", "claude") })
	_, err = missing.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.ClaudeCode, Model: "haiku", Prompt: "hi"})
	if f := textFailure(t, err); !f.Unavailable || !strings.Contains(f.Message, "Claude Code") {
		t.Fatalf("not installed: %+v", f)
	}
	_, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Pi, Model: "x", Prompt: "hi"})
	if f := textFailure(t, err); !f.Unavailable {
		t.Fatalf("Pi does not write: %+v", f)
	}
	emptyDir(t, tmp)
}

// Codex writes with codex exec: ephemeral, outside a repository, read-only, without the user's config.toml (and with it
// their MCP servers, hooks and plugins), the chosen model and effort, everything on stdin. A command it tries to run
// ends the run as a failure at once.
func TestCodexWritesText(t *testing.T) {
	tmp := t.TempDir()
	h := newCodexHarness(t, func(o *Options) { o.TextTempDir = tmp })
	h.env["OPENAI_API_KEY"] = "sk-must-not-pass"
	req := executor.TextRequest{Runtime: agentruntime.Codex, Model: "gpt-test-mini", ThinkingLevel: levelOf("low"), Instructions: "Write a Prompt.", Prompt: "fake:args"}
	out, err := h.e.WriteText(writeCtx(t), req)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal([]byte(out), &args); err != nil {
		t.Fatalf("args: %q %v", out, err)
	}
	if args[0] != "exec" || args[len(args)-1] != "-" {
		t.Fatalf("codex exec reading stdin: %v", args)
	}
	for _, flag := range []string{"--ephemeral", "--skip-git-repo-check", "--ignore-user-config", "--json"} {
		if !hasFlag(args, flag) {
			t.Errorf("missing %s: %v", flag, args)
		}
	}
	for flag, want := range map[string]string{"--sandbox": "read-only", "--model": "gpt-test-mini", "--color": "never"} {
		if got, ok := argAfter(args, flag); !ok || got != want {
			t.Errorf("%s: %q, want %q (%v)", flag, got, want, args)
		}
	}
	joined := strings.Join(args, " ")
	for _, c := range []string{`model_reasoning_effort="low"`, "features.plugins=false", "features.apps=false", "features.hooks=false"} {
		if !strings.Contains(joined, "-c "+c) {
			t.Errorf("missing -c %s: %v", c, args)
		}
	}
	if file, _ := argAfter(args, "--output-last-message"); file == "" || strings.HasPrefix(file, tmp+string(filepath.Separator)) && strings.Contains(file, string(filepath.Separator)+"work"+string(filepath.Separator)) {
		t.Errorf("the last message goes to a file outside the working folder: %q", file)
	}
	out, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Codex, Model: "gpt-test-mini", Instructions: "Write a Prompt.", Prompt: "fake:stdin"})
	if err != nil || out != "Write a Prompt.\n\nfake:stdin" {
		t.Fatalf("the instructions and the rest on stdin: %q %v", out, err)
	}
	if out, _ := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Codex, Model: "m", Prompt: "fake:args"}); strings.Contains(out, "model_reasoning_effort") {
		t.Fatalf("no level, no effort: %s", out)
	}
	var env map[string]string
	out, _ = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Codex, Model: "m", Prompt: "fake:env"})
	if json.Unmarshal([]byte(out), &env) != nil || env["CODEX_HOME"] != filepath.Join(h.realHome, ".codex") || env["HOME"] != h.realHome || env["OPENAI_API_KEY"] != "" {
		t.Fatalf("env: %s", out)
	}

	start := time.Now()
	_, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Codex, Model: "m", Prompt: "fake:tool cat ~/.ssh/id_rsa"})
	if f := textFailure(t, err); f.Unavailable || !strings.Contains(f.Message, "Codex") || !strings.Contains(f.Message, "command") || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("a command ends the run at once: %+v after %v", f, time.Since(start))
	}
	_, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.Codex, Model: "m", Prompt: "fake:error stream disconnected: usage limit"})
	if f := textFailure(t, err); f.Unavailable || !strings.Contains(f.Message, "stream disconnected: usage limit") {
		t.Fatalf("the CLI's own reason: %+v", f)
	}
	emptyDir(t, tmp)
	if _, err := os.Stat(filepath.Join(h.realHome, ".codex", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("nothing is saved in Codex's history: %v", err)
	}
}

// acpWriterHarness runs fakeacp as Grok or agy with its native records (the ones the real CLI keeps under HOME) going
// to the fake real home.
func acpWriterHarness(t *testing.T, flavor, tmp string) *acpHarness {
	h := newACPHarness(t, flavor, func(o *Options) {
		o.TextTempDir = tmp
		o.Passthrough = append(o.Passthrough, "FAKEACP_NATIVE_HOME", "FAKEACP_HOME")
	})
	h.env["FAKEACP_NATIVE_HOME"] = h.realHome
	h.env["FAKEACP_HOME"] = t.TempDir()
	return h
}

// Grok and Antigravity write over ACP: started without their approve-everything flags and without plugin folders, a
// new session in the temporary folder with no MCP servers, the model (and level) set as the drivers do, every
// permission request turned down. Afterwards the folder is removed, and so is the session the CLI kept under HOME
// (only that one).
func TestACPWritesText(t *testing.T) {
	for _, flavor := range []string{"grok", "agy"} {
		t.Run(flavor, func(t *testing.T) {
			tmp := t.TempDir()
			h := acpWriterHarness(t, flavor, tmp)
			realTmp, _ := filepath.EvalSymlinks(tmp)
			runtime, model := agentruntime.Grok, "fake-1"
			if flavor == "agy" {
				runtime, model = agentruntime.Antigravity, "fake"
			}
			// Another session of the user's, kept by the CLI, stays.
			other := filepath.Join(h.realHome, ".grok", "sessions", url.PathEscape("/elsewhere"), "01-other")
			if flavor == "agy" {
				other = filepath.Join(h.realHome, ".gemini", "antigravity-cli", "conversations", "other.db")
			}
			if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(other, []byte("theirs"), 0o600); err != nil {
				t.Fatal(err)
			}
			if flavor == "agy" { // agy's own folders exist on a computer where it has been used
				if err := os.MkdirAll(filepath.Join(h.realHome, ".gemini", "antigravity-cli", "brain"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, h.realHome)

			out, err := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: runtime, Model: model, ThinkingLevel: levelOf("low"), Instructions: "Write a Prompt.", Prompt: "fake:config"})
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				Cwd   string   `json:"cwd"`
				MCP   []string `json:"mcp"`
				Model string   `json:"model"`
			}
			if err := json.Unmarshal([]byte(out), &cfg); err != nil || len(cfg.MCP) != 0 || !strings.HasPrefix(cfg.Cwd, realTmp) {
				t.Fatalf("session: %+v %v (%s)", cfg, err, out)
			}
			starts := h.starts()
			args := starts[len(starts)-1].Args
			if flavor == "agy" {
				if cfg.Model != "fake-low" || !slices.Equal(args, []string{"acp"}) {
					t.Fatalf("agy: the model by the id of its level, no flags: %q %v", cfg.Model, args)
				}
			} else if !slices.Equal(args, []string{"--permission-mode", "default", "agent", "--model", "fake-1", "--reasoning-effort", "low", "stdio"}) {
				t.Fatalf("grok: %v", args)
			}
			if out, err := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: runtime, Model: model, Instructions: "Write a Prompt.", Prompt: "fake:permission"}); err != nil || out != "permission=no" {
				t.Fatalf("a permission request is turned down: %q %v", out, err)
			}
			out, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: runtime, Model: model, Instructions: "Write a Prompt.", Prompt: "hello"})
			if err != nil || out != "echo: Write a Prompt.\n\nhello" {
				t.Fatalf("the instructions come first: %q %v", out, err)
			}
			_, err = h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: runtime, Model: model, Prompt: "fake:tool ls -la"})
			if f := textFailure(t, err); f.Unavailable || !strings.Contains(f.Message, "tool") {
				t.Fatalf("a tool ends the run: %+v", f)
			}
			emptyDir(t, tmp)
			sameTree(t, h.realHome, before)
			if after := snapshotTree(t, h.realHome); len(after) != len(before) {
				t.Fatalf("records left under HOME:\n%v\nbefore:\n%v", after, before)
			}
		})
	}
}

// A local ACP command writes with its own arguments and environment, and its default model is not set.
func TestLocalCommandWritesText(t *testing.T) {
	tmp := t.TempDir()
	h := acpWriterHarness(t, "agy", tmp)
	id := uuid.New()
	agentruntime.SetLocalCommands([]agentruntime.Runtime{agentruntime.LocalCommandRuntime(id, "Mine",
		agentruntime.LocalCommand{Command: fakeacpbin.Path(t), Args: []string{"serve", "acp"}, Env: map[string]string{"MINE_KEY": "k"}})})
	defer agentruntime.SetLocalCommands(nil)
	out, err := h.e.WriteText(writeCtx(t), executor.TextRequest{Runtime: agentruntime.LocalCommandRuntimeID(id), Model: "default", Prompt: "fake:config"})
	if err != nil || !strings.Contains(out, `"model":""`) {
		t.Fatalf("the program's own model: %q %v", out, err)
	}
	starts := h.starts()
	if s := starts[len(starts)-1]; !slices.Equal(s.Args, []string{"serve", "acp"}) {
		t.Fatalf("its own arguments: %v", s.Args)
	}
	emptyDir(t, tmp)
}
