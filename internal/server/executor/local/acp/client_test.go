package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/acp"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/testutil/fakeacpbin"
)

type sink struct {
	mu       sync.Mutex
	text     strings.Builder
	tools    []string
	commands []string
}

func (s *sink) TextDelta(d string) { s.mu.Lock(); s.text.WriteString(d); s.mu.Unlock() }
func (s *sink) ToolStarted(id, name string) {
	s.mu.Lock()
	s.tools = append(s.tools, "start:"+name)
	s.mu.Unlock()
}
func (s *sink) ToolEnded(id, name string, isError bool) {
	s.mu.Lock()
	state := "ok"
	if isError {
		state = "error"
	}
	s.tools = append(s.tools, "end:"+name+":"+state)
	s.mu.Unlock()
}
func (s *sink) ToolCommand(id, name, command string) {
	s.mu.Lock()
	s.commands = append(s.commands, command)
	s.mu.Unlock()
}
func (s *sink) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.text.String() }

type harness struct {
	t      *testing.T
	home   string
	cwd    string
	bin    string
	flavor string
	env    []string
}

func newHarness(t *testing.T, flavor string) *harness {
	return &harness{t: t, home: t.TempDir(), cwd: t.TempDir(), bin: fakeacpbin.Path(t), flavor: flavor}
}

func (h *harness) start(o acp.Options) (*acp.Client, acp.Agent) {
	h.t.Helper()
	cmd := exec.Command(h.bin, "agent", "stdio")
	cmd.Env = append(append(os.Environ(), "FAKEACP_HOME="+h.home, "FAKEACP_FLAVOR="+h.flavor), h.env...)
	c, err := acp.Start(cmd, o)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.CloseStdin(); c.Kill(); <-c.Done() })
	a, err := c.Initialize(ctx(h.t), "loop-now-test", "0.0.0")
	if err != nil || a.ProtocolVersion != 1 || !a.LoadSession || !a.Images {
		h.t.Fatalf("initialize: %+v %v", a, err)
	}
	return c, a
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func prompt(t *testing.T, c *acp.Client, session, text string, s *sink, images ...messageimage.Content) runturn.Result {
	t.Helper()
	if s == nil {
		s = &sink{}
	}
	r, err := c.Prompt(ctx(t), session, text, s, images...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A new session streams its replies and tool calls; the agent's own _meta and its models are passed on.
func TestPromptStreamsTextAndTools(t *testing.T) {
	h := newHarness(t, "grok")
	c, a := h.start(acp.Options{})
	if !strings.Contains(string(a.Meta), "reasoningEfforts") {
		t.Fatalf("Grok's _meta lists its models: %s", a.Meta)
	}
	sess, err := c.NewSession(ctx(t), h.cwd, []acp.MCPServer{{Name: "loopctl", Command: "/bin/true", Env: map[string]string{"A": "1"}}})
	if err != nil || sess.ID == "" || len(sess.Models) != 2 || sess.Models[0].ID != "fake-1" || sess.Models[0].Name != "Fake One" {
		t.Fatalf("new session: %+v %v", sess, err)
	}
	s := &sink{}
	if r := prompt(t, c, sess.ID, "hello", s); r.Outcome != runturn.OutcomeCompleted || r.Text != "echo: hello" || s.String() != "echo: hello" {
		t.Fatalf("echo: %+v %q", r, s.String())
	}
	s = &sink{}
	if r := prompt(t, c, sess.ID, "fake:tool ls -la", s); r.Text != "ran" || strings.Join(s.tools, ",") != "start:execute,end:execute:ok" || strings.Join(s.commands, ",") != "ls -la" {
		t.Fatalf("tool: %+v %v %v", r, s.tools, s.commands)
	}
	s = &sink{}
	prompt(t, c, sess.ID, "fake:toolfail", s)
	if strings.Join(s.tools, ",") != "start:execute,end:execute:error" {
		t.Fatalf("failed tool: %v", s.tools)
	}
	if err := c.SetModel(ctx(t), sess.ID, "fake-2"); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Cwd   string   `json:"cwd"`
		MCP   []string `json:"mcp"`
		Model string   `json:"model"`
	}
	if err := json.Unmarshal([]byte(prompt(t, c, sess.ID, "fake:config", nil).Text), &cfg); err != nil || cfg.Cwd != h.cwd || strings.Join(cfg.MCP, ",") != "loopctl" || cfg.Model != "fake-2" {
		t.Fatalf("session settings: %+v %v", cfg, err)
	}
	if r := prompt(t, c, sess.ID, "fake:images", nil, messageimage.Encode("image/png", []byte("png"))); r.Text != "images=1" {
		t.Fatalf("images: %q", r.Text)
	}
	if r := prompt(t, c, sess.ID, "fake:permission", nil); r.Text != "permission=yes" {
		t.Fatalf("a permission request is answered with the allow option: %q", r.Text)
	}
}

// A saved session loads in a new process: its replayed history is counted and not passed on as the reply, and the
// conversation goes on.
func TestLoadSessionReplaysQuietly(t *testing.T) {
	h := newHarness(t, "grok")
	c, _ := h.start(acp.Options{})
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt(t, c, sess.ID, "one", nil)
	prompt(t, c, sess.ID, "two", nil)

	c2, _ := h.start(acp.Options{})
	loaded, err := c2.LoadSession(ctx(t), sess.ID, h.cwd, nil)
	if err != nil || loaded.ID != sess.ID || loaded.Replayed != 4 {
		t.Fatalf("load: %+v %v", loaded, err)
	}
	s := &sink{}
	if r := prompt(t, c2, sess.ID, "fake:history", s); r.Text != "history=2" || s.String() != "history=2" {
		t.Fatalf("the conversation goes on, without the replay in the reply: %+v %q", r, s.String())
	}
}

// Grok fails to load a session it does not have; Antigravity loads it empty.
func TestLoadUnknownSession(t *testing.T) {
	h := newHarness(t, "grok")
	c, _ := h.start(acp.Options{})
	var rpc *acp.RPCError
	if _, err := c.LoadSession(ctx(t), "01a20c33-0000-7000-8000-000000000000", h.cwd, nil); !errors.As(err, &rpc) || !strings.Contains(string(rpc.Data), "FS_NOT_FOUND") {
		t.Fatalf("Grok: %v", err)
	}
	g := newHarness(t, "agy")
	c, _ = g.start(acp.Options{})
	if s, err := c.LoadSession(ctx(t), "00000000-0000-4000-8000-000000000000", g.cwd, nil); err != nil || s.Replayed != 0 {
		t.Fatalf("Antigravity: %+v %v", s, err)
	}
}

// An agent that replays a session but never answers session/load counts as loaded once it has been quiet.
func TestLoadSessionWithoutAnAnswer(t *testing.T) {
	h := newHarness(t, "grok")
	c, _ := h.start(acp.Options{})
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt(t, c, sess.ID, "one", nil)
	h.env = []string{"FAKEACP_LOAD_SILENT=1"}
	c2, _ := h.start(acp.Options{LoadQuiet: 300 * time.Millisecond})
	start := time.Now()
	loaded, err := c2.LoadSession(ctx(t), sess.ID, h.cwd, nil)
	if err != nil || loaded.Replayed != 2 || time.Since(start) > 5*time.Second {
		t.Fatalf("load without an answer: %+v %v %v", loaded, err, time.Since(start))
	}
	if r := prompt(t, c2, sess.ID, "fake:history", nil); r.Text != "history=1" {
		t.Fatalf("after the quiet load: %q", r.Text)
	}
}

// A cancel ends the prompt as stopped; an agent that ignores the cancel is still stopped when it is killed.
func TestCancel(t *testing.T) {
	h := newHarness(t, "agy")
	c, _ := h.start(acp.Options{})
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan runturn.Result, 1)
	s := &sink{}
	go func() { done <- prompt(t, c, sess.ID, "fake:slow", s) }()
	waitText(t, s)
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	if r := result(t, done); r.Outcome != runturn.OutcomeAborted || !strings.HasPrefix(r.Text, ".") {
		t.Fatalf("cancelled: %+v", r)
	}
	if c.InPrompt() {
		t.Fatal("no prompt after the cancel")
	}

	s = &sink{}
	go func() { done <- prompt(t, c, sess.ID, "fake:noack", s) }()
	waitText(t, s)
	if err := c.Cancel(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		t.Fatalf("the agent ignored the cancel, so the prompt goes on: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	c.Kill()
	if r := result(t, done); r.Outcome != runturn.OutcomeAborted {
		t.Fatalf("killed after a cancel, the prompt counts as stopped: %+v", r)
	}
}

func result(t *testing.T, done chan runturn.Result) runturn.Result {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("the prompt did not end")
		return runturn.Result{}
	}
}

func waitText(t *testing.T, s *sink) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.String() == "" {
		if time.Now().After(deadline) {
			t.Fatal("no text")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A refusal fails the turn; an agent that exits fails it with what it said so far; a missing login fails it as such.
func TestPromptFailures(t *testing.T) {
	h := newHarness(t, "grok")
	c, _ := h.start(acp.Options{})
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := prompt(t, c, sess.ID, "fake:refusal", nil); r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeError {
		t.Fatalf("refusal: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(h.home, ".logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := prompt(t, c, sess.ID, "hello", nil); r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeLoginRequired {
		t.Fatalf("logged out: %+v", r)
	}
	if err := os.Remove(filepath.Join(h.home, ".logged-out")); err != nil {
		t.Fatal(err)
	}
	if r := prompt(t, c, sess.ID, "fake:exit", nil); r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeExited || r.Text != "partial" {
		t.Fatalf("exit: %+v", r)
	}
	<-c.Done()
	if _, err := c.Prompt(ctx(t), sess.ID, "again", &sink{}); !errors.Is(err, acp.ErrExited) {
		t.Fatalf("after the exit: %v", err)
	}
}
