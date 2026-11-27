package codexapp_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/codexapp"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/testutil/fakecodexbin"
)

type sink struct {
	mu       sync.Mutex
	text     strings.Builder
	tools    []string
	commands []string
	targets  []string
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
func (s *sink) ToolFiles(id, name string, paths []string) {
	s.mu.Lock()
	s.targets = append(s.targets, name+":"+strings.Join(paths, "+"))
	s.mu.Unlock()
}
func (s *sink) ToolQuery(id, name, query string) {
	s.mu.Lock()
	s.targets = append(s.targets, name+":"+query)
	s.mu.Unlock()
}

type observer struct {
	mu     sync.Mutex
	usage  []codexapp.Usage
	limits []map[string]any
}

func (o *observer) Usage(u codexapp.Usage, inTurn bool) {
	o.mu.Lock()
	o.usage = append(o.usage, u)
	o.mu.Unlock()
}
func (o *observer) RateLimit(info map[string]any, inTurn bool) {
	o.mu.Lock()
	o.limits = append(o.limits, info)
	o.mu.Unlock()
}

type harness struct {
	t    *testing.T
	home string
	bin  string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, home: t.TempDir(), bin: fakecodexbin.Path(t)}
}

func (h *harness) start() *codexapp.Client {
	h.t.Helper()
	cmd := exec.Command(h.bin, "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+h.home)
	c, err := codexapp.Start(cmd, codexapp.Options{})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.CloseStdin(); <-c.Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	init, err := c.Initialize(ctx, "loop-now-test", "0.0.0")
	if err != nil || init.CodexHome != h.home {
		h.t.Fatalf("initialize: %+v %v", init, err)
	}
	return c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func turn(t *testing.T, c *codexapp.Client, thread, text string, s *sink) runturn.Result {
	t.Helper()
	if s == nil {
		s = &sink{}
	}
	r, err := c.Turn(ctx(t), text, codexapp.TurnOptions{ThreadID: thread, Model: "gpt-test", Effort: "high"}, s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A thread starts with prompts off and full access; a turn streams its text and tools, and the final text is the
// whole reply, with separate message items kept apart.
func TestTurnStreamsTextAndTools(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	thread, err := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home, Model: "gpt-test", DeveloperInstructions: "platform prompt",
		Config: map[string]any{"mcp_servers.probe": map[string]any{"command": "probe"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	r := turn(t, c, thread, "hello", s)
	if r.Outcome != runturn.OutcomeCompleted || r.Text != "echo: hello" || s.text.String() != "echo: hello" {
		t.Fatalf("echo: %+v %q", r, s.text.String())
	}
	s = &sink{}
	r = turn(t, c, thread, "fake:files", s)
	if r.Text != "files" || strings.Join(s.targets, ",") != "apply_patch:/w/a.go+/w/b.go,web_search:go 1.27 release" {
		t.Fatalf("targets: %+v %v", r, s.targets)
	}
	s = &sink{}
	r = turn(t, c, thread, "fake:tool ls -la", s)
	if r.Text != "ran" || strings.Join(s.tools, ",") != "start:shell,end:shell:ok" || len(s.commands) != 1 || s.commands[0] != "ls -la" {
		t.Fatalf("tool: %+v %v %v", r, s.tools, s.commands)
	}
	s = &sink{}
	turn(t, c, thread, "fake:toolfail", s)
	if strings.Join(s.tools, ",") != "start:shell,end:shell:error" {
		t.Fatalf("failed tool: %v", s.tools)
	}
	if r := turn(t, c, thread, "fake:two", nil); r.Text != "first\n\nsecond" {
		t.Fatalf("two messages: %q", r.Text)
	}
	if r := turn(t, c, thread, "fake:whole", nil); r.Text != "whole" {
		t.Fatalf("a message without deltas: %q", r.Text)
	}
	var cfg struct {
		ApprovalPolicy        any            `json:"approvalPolicy"`
		Sandbox               any            `json:"sandbox"`
		DeveloperInstructions string         `json:"developerInstructions"`
		Config                map[string]any `json:"config"`
		Model, Effort         any
	}
	if err := json.Unmarshal([]byte(turn(t, c, thread, "fake:config", nil).Text), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ApprovalPolicy != "never" || cfg.Sandbox != "danger-full-access" || cfg.DeveloperInstructions != "platform prompt" || cfg.Config["mcp_servers.probe"] == nil ||
		cfg.Model != "gpt-test" || cfg.Effort != "high" {
		t.Fatalf("thread and turn settings: %+v", cfg)
	}
}

// Requests from Codex are answered at once: approvals granted, questions declined.
func TestServerRequestsAreAnswered(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	thread, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	if r := turn(t, c, thread, "fake:approval", nil); r.Text != "approval=accept" {
		t.Fatalf("approval: %q", r.Text)
	}
	if r := turn(t, c, thread, "fake:ask", nil); r.Text != `answers={"answers":{}}` {
		t.Fatalf("question: %q", r.Text)
	}
}

// A stop reaches the turn even when asked before the turn reported its id; a turn that ignores it keeps running until
// the caller ends the process.
func TestInterrupt(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	thread, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	results := make(chan runturn.Result, 1)
	go func() { results <- turn(t, c, thread, "fake:slow", nil) }()
	for !c.InTurn() {
		time.Sleep(time.Millisecond)
	}
	if err := c.Interrupt(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-results:
		if r.Outcome != runturn.OutcomeAborted {
			t.Fatalf("stopped turn: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not stop")
	}
	go func() { results <- turn(t, c, thread, "fake:noack", nil) }()
	for !c.InTurn() {
		time.Sleep(time.Millisecond)
	}
	_ = c.Interrupt()
	select {
	case r := <-results:
		t.Fatalf("a turn that ignores the stop must not end by itself: %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	c.Kill()
	select {
	case r := <-results:
		if r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeExited {
			t.Fatalf("killed: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not end with the process")
	}
	if _, err := c.Turn(ctx(t), "again", codexapp.TurnOptions{ThreadID: thread}, &sink{}); err != runturn.ErrExited {
		t.Fatalf("a message to an exited process: %v", err)
	}
}

// Errors: a failed turn keeps Codex's message; unauthorized means the login is missing or revoked; an error that will
// be retried does not end the turn.
func TestTurnErrors(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	thread, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	if r := turn(t, c, thread, "fake:error usageLimitExceeded", nil); r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeError || !strings.Contains(r.ErrorMessage, "usageLimitExceeded") {
		t.Fatalf("usage limit: %+v", r)
	}
	if r := turn(t, c, thread, "fake:error unauthorized", nil); r.ErrorCode != executor.ErrRuntimeLoginRequired {
		t.Fatalf("unauthorized: %+v", r)
	}
	if r := turn(t, c, thread, "fake:retry", nil); r.Outcome != runturn.OutcomeCompleted || r.Text != "ok" {
		t.Fatalf("retried: %+v", r)
	}
	if r := turn(t, c, thread, "fake:exit", nil); r.Outcome != runturn.OutcomeFailed || r.ErrorCode != executor.ErrRuntimeExited || r.Text != "partial" {
		t.Fatalf("exit: %+v", r)
	}
}

// Usage per model call and rate limits go to the observer; images go as data URLs.
func TestUsageAndImages(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	o := &observer{}
	c.SetObserver(o)
	thread, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	turn(t, c, thread, "fake:usage", nil)
	if len(o.usage) != 1 || o.usage[0] != (codexapp.Usage{Input: 100, CachedInput: 40, Output: 20, Reasoning: 5, Total: 120}) || len(o.limits) != 1 {
		t.Fatalf("usage %+v limits %+v", o.usage, o.limits)
	}
	r, err := c.Turn(ctx(t), "fake:images", codexapp.TurnOptions{ThreadID: thread, Images: []messageimage.Content{messageimage.Encode("image/png", []byte("png")), messageimage.Encode("image/png", []byte("png"))}}, &sink{})
	if err != nil || r.Text != "images=2" {
		t.Fatalf("images: %+v %v", r, err)
	}
}

// A thread is saved after its first turn: it can then be resumed in another process, forked up to a turn, listed and
// archived; one that was never saved is not found.
func TestThreadLifecycle(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	empty, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	thread, _ := c.StartThread(ctx(t), codexapp.ThreadOptions{Cwd: h.home})
	turn(t, c, thread, "one", nil)
	turn(t, c, thread, "two", nil)
	last, err := c.LastTurnID(ctx(t), thread)
	if err != nil || last == "" {
		t.Fatal(last, err)
	}
	turn(t, c, thread, "three", nil)

	other := h.start()
	if err := other.ResumeThread(ctx(t), empty, codexapp.ThreadOptions{Cwd: h.home}); !codexapp.IsThreadNotFound(err) {
		t.Fatalf("a thread without turns is not saved: %v", err)
	}
	if err := other.ResumeThread(ctx(t), thread, codexapp.ThreadOptions{Cwd: h.home}); err != nil {
		t.Fatal(err)
	}
	if r := turn(t, other, thread, "fake:history", nil); r.Text != "history=3" {
		t.Fatalf("resumed: %q", r.Text)
	}
	fork, err := other.ForkThread(ctx(t), thread, last, codexapp.ThreadOptions{Cwd: h.home})
	if err != nil || fork == thread {
		t.Fatal(fork, err)
	}
	if r := turn(t, other, fork, "fake:history", nil); r.Text != "history=2" {
		t.Fatalf("forked up to the second turn: %q", r.Text)
	}
	if err := other.Archive(ctx(t), thread); err != nil {
		t.Fatal(err)
	}
	if err := other.Archive(ctx(t), thread); !codexapp.IsThreadNotFound(err) {
		t.Fatalf("an archived thread is gone: %v", err)
	}
}

// The model list follows pages and keeps what the platform needs.
func TestListModels(t *testing.T) {
	h := newHarness(t)
	c := h.start()
	models, err := c.ListModels(ctx(t))
	if err != nil || len(models) != 3 {
		t.Fatal(models, err)
	}
	m := models[0]
	if m.ID != "gpt-test" || m.DisplayName != "GPT Test" || !m.IsDefault || m.DefaultEffort != "medium" || strings.Join(m.Efforts, ",") != "low,medium,high,xhigh,ultra" ||
		strings.Join(m.InputModalities, ",") != "text,image" || !models[2].Hidden {
		t.Fatalf("models: %+v", models)
	}
	if err := c.SetSkillRoots(ctx(t), []string{"/skills"}); err != nil {
		t.Fatal(err)
	}
}
