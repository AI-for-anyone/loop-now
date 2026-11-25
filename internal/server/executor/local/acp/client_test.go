package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	timedOut []time.Duration
}

func (s *sink) toolList() string { s.mu.Lock(); defer s.mu.Unlock(); return strings.Join(s.tools, ",") }

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
func (s *sink) ToolNoResult(id, name string) {
	s.mu.Lock()
	s.tools = append(s.tools, "end:"+name+":none")
	s.mu.Unlock()
}
func (s *sink) BackgroundWaitTimedOut(limit time.Duration) {
	s.mu.Lock()
	s.timedOut = append(s.timedOut, limit)
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

func newSession(t *testing.T, c *acp.Client, h *harness) string {
	t.Helper()
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}

// A tool the agent still has open when its turn ends is closed without a result (neither a success nor a failure),
// whatever the agent.
func TestTurnEndClosesOpenTools(t *testing.T) {
	for _, flavor := range []string{"grok", "agy"} {
		h := newHarness(t, flavor)
		c, _ := h.start(acp.Options{})
		s := &sink{}
		if r := prompt(t, c, newSession(t, c, h), "fake:toolopen sleep 9", s); r.Outcome != runturn.OutcomeCompleted || r.Text != "ran" || s.toolList() != "start:execute,end:execute:none" {
			t.Fatalf("%s: %+v %s", flavor, r, s.toolList())
		}
	}
}

// background is how Antigravity's turns wait for its background commands, with short limits for tests: the agent
// takes 400ms to begin a reply of its own, longer than the settle time.
func background() *acp.Background {
	return &acp.Background{
		Holds: func(kind string, raw json.RawMessage) bool {
			var in struct{ IsDaemon bool }
			_ = json.Unmarshal(raw, &in)
			return kind == "execute" && !in.IsDaemon
		},
		ReplyQuietLimit: 5 * time.Second, Settle: 200 * time.Millisecond,
	}
}

func agyBackgroundHarness(t *testing.T) *harness {
	h := newHarness(t, "agy")
	h.env = []string{"FAKEACP_WAKE_DELAY=400ms"}
	return h
}

// agyWithTasks starts agy with Background and records its background tasks and its own replies.
func agyWithTasks(t *testing.T) (*acp.Client, string, *bgObserver, chan *selfTurn) {
	t.Helper()
	h := agyBackgroundHarness(t)
	c, _ := h.start(acp.Options{Background: background()})
	obs := newBgObserver()
	c.SetObserver(obs)
	turns := make(chan *selfTurn, 8)
	c.SetSelfTurns(func() runturn.SelfTurn {
		s := &selfTurn{sink: &sink{}, ended: make(chan runturn.Result, 1)}
		turns <- s
		return s
	})
	return c, newSession(t, c, h), obs, turns
}

// The prompt ends when the agent ends it. A command still running then goes on in the background: it is reported with
// its command line and stays open on the turn that started it, whose sink gets its end later, with the output. A
// command that ends within the prompt is a tool like any other. The reply the agent then gives by itself is a self
// turn, which ends once usage_update has come and nothing more for Settle.
func TestBackgroundCommandsGoOn(t *testing.T) {
	c, sess, obs, turns := agyWithTasks(t)
	s := &sink{}
	start := time.Now()
	r := prompt(t, c, sess, "fake:background now 300ms", s)
	if r.Outcome != runturn.OutcomeCompleted || r.Text != "backgrounded" || time.Since(start) > time.Second {
		t.Fatalf("the prompt ends at once: %+v after %v", r, time.Since(start))
	}
	if s.toolList() != "start:execute,end:execute:ok,start:execute" {
		t.Fatalf("the command running in the background stays open: %s", s.toolList())
	}
	obs.mu.Lock()
	started := append([]acp.Task(nil), obs.started...)
	obs.mu.Unlock()
	if len(started) != 1 || started[0].Command != "sleep 300ms" || started[0].ToolCallID != started[0].ID {
		t.Fatalf("background task: %+v", started)
	}
	end := recv(t, obs.ended, "its end")
	if end.ID != started[0].ID || end.Killed || end.ExitCode == nil || *end.ExitCode != 0 || end.Output == nil || *end.Output != "finished" {
		t.Fatalf("end: %+v", end)
	}
	if s.toolList() != "start:execute,end:execute:ok,start:execute,end:execute:ok" {
		t.Fatalf("its end goes to the turn that started it: %s", s.toolList())
	}
	self := recv(t, turns, "the agent's own reply")
	res := recv(t, self.ended, "its end")
	if res.Outcome != runturn.OutcomeCompleted || strings.TrimSpace(res.Text) != "reported 1" {
		t.Fatalf("own reply: %+v", res)
	}
	if c.InPrompt() {
		t.Fatal("no turn is open")
	}
}

// A daemon command does not go on as a background task (it may never end): it is closed without a result when the
// prompt ends. An agent run without Background has none.
func TestBackgroundDaemonAndOtherAgents(t *testing.T) {
	c, sess, obs, _ := agyWithTasks(t)
	s := &sink{}
	if r := prompt(t, c, sess, "fake:background daemon", s); r.Text != "backgrounded" || s.toolList() != "start:execute,end:execute:none" {
		t.Fatalf("daemon: %+v %s", r, s.toolList())
	}
	obs.mu.Lock()
	n := len(obs.started)
	obs.mu.Unlock()
	if n != 0 {
		t.Fatal("a daemon is not a background task")
	}
	g := newHarness(t, "grok")
	gc, _ := g.start(acp.Options{})
	s = &sink{}
	if r := prompt(t, gc, newSession(t, gc, g), "fake:background 2s", s); r.Text != "backgrounded" || s.toolList() != "start:execute,end:execute:none" {
		t.Fatalf("without Background: %+v %s", r, s.toolList())
	}
}

// CancelBackground ends the session's background commands with session/cancel (the agent says nothing about them):
// each is reported killed and closed without a result on the turn that started it. With a prompt open it refuses.
func TestCancelBackground(t *testing.T) {
	c, sess, obs, _ := agyWithTasks(t)
	s := &sink{}
	prompt(t, c, sess, "fake:background 10s 20s", s)
	if err := c.CancelBackground(sess); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if end := recv(t, obs.ended, "an end"); !end.Killed {
			t.Fatalf("killed: %+v", end)
		}
	}
	if s.toolList() != "start:execute,start:execute,end:execute:none,end:execute:none" {
		t.Fatalf("closed without a result: %s", s.toolList())
	}
	done := make(chan struct{})
	go func() { defer close(done); prompt(t, c, sess, "fake:sleep 1s", nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for !c.InPrompt() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.CancelBackground(sess); err == nil {
		t.Fatal("refused while a prompt runs")
	}
	<-done
}

// With RejectPermissions every permission request is turned down: the agent's own reject option when it offers one.
func TestRejectPermissions(t *testing.T) {
	h := newHarness(t, "agy")
	c, _ := h.start(acp.Options{RejectPermissions: true})
	if r := prompt(t, c, newSession(t, c, h), "fake:permission", nil); r.Text != "permission=no" {
		t.Fatalf("a permission request is turned down with the reject option: %q", r.Text)
	}
}

// usageObserver keeps the usage the client passes on.
type usageObserver struct {
	mu  sync.Mutex
	got []string
}

func (o *usageObserver) Usage(u acp.Usage) {
	opt := func(v *int64) string {
		if v == nil {
			return "-"
		}
		return fmt.Sprint(*v)
	}
	o.mu.Lock()
	o.got = append(o.got, fmt.Sprintf("%s in=%d out=%d read=%d write=%d total=%s thought=%s model=%s", u.StopReason, u.Input, u.Output,
		u.CacheRead, u.CacheWrite, opt(u.Total), opt(u.Thought), u.Model))
	o.mu.Unlock()
}

func (o *usageObserver) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.got)
}

// The usage an agent reports with a prompt's result is passed on once per prompt, as the agent reports it: agy's in the
// ACP field, Grok's in _meta (Grok's copy in its own turn_completed notification is not counted again). A cancelled
// prompt reports none, and agy's own replies after background commands carry no token counts, so they add none.
func TestPromptReportsUsage(t *testing.T) {
	for _, c := range []struct{ flavor, want string }{
		{"agy", "end_turn in=1200 out=30 read=800 write=0 total=2030 thought=12 model="},
		{"grok", "end_turn in=1931 out=31 read=217 write=0 total=1962 thought=30 model=fake-1-build"},
	} {
		h := agyBackgroundHarness(t)
		h.flavor = c.flavor
		cl, _ := h.start(acp.Options{Background: background()})
		obs := &usageObserver{}
		cl.SetObserver(obs)
		sess := newSession(t, cl, h)
		prompt(t, cl, sess, "hello", nil)
		if got := obs.list(); len(got) != 1 || got[0] != c.want {
			t.Fatalf("%s: %q", c.flavor, got)
		}
		done := make(chan runturn.Result, 1)
		s := &sink{}
		go func() { done <- prompt(t, cl, sess, "fake:slow", s) }()
		waitText(t, s)
		if err := cl.Cancel(); err != nil {
			t.Fatal(err)
		}
		if r := result(t, done); r.Outcome != runturn.OutcomeAborted || len(obs.list()) != 1 {
			t.Fatalf("%s: a cancelled prompt: %+v %q", c.flavor, r, obs.list())
		}
		if c.flavor != "agy" {
			continue
		}
		if r := prompt(t, cl, sess, "fake:background 300ms", nil); r.Text != "backgrounded" || len(obs.list()) != 2 {
			t.Fatalf("a prompt that leaves a command running: %+v %q", r, obs.list())
		}
		time.Sleep(1500 * time.Millisecond) // the command ends, and agy replies by itself
		if len(obs.list()) != 2 {
			t.Fatalf("agy's own reply: %q", obs.list())
		}
	}
}
