package ccstream_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/ccstream"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/testutil/fakeclaudebin"
)

// start starts fakeclaude in a temp HOME and working directory (session records are written under the temp HOME).
func start(t *testing.T, o ccstream.Options, env ...string) *ccstream.Client {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	cmd := exec.Command(fakeclaudebin.Path(t), "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--model", "haiku", "--session-id", "11111111-2222-3333-4444-555555555555", "--tools", "Bash,Read")
	cmd.Dir = cwd
	cmd.Env = append([]string{"HOME=" + home, "PATH=/usr/bin:/bin"}, env...)
	c, err := ccstream.Start(cmd, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.CloseStdin()
		select {
		case <-c.Done():
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return c
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

// sink records the events of a turn.
type sink struct {
	mu       sync.Mutex
	text     strings.Builder
	events   []string
	firstOut chan struct{}
	once     sync.Once
}

func newSink() *sink { return &sink{firstOut: make(chan struct{})} }

func (s *sink) TextDelta(d string) {
	s.mu.Lock()
	s.text.WriteString(d)
	s.mu.Unlock()
	s.once.Do(func() { close(s.firstOut) })
}
func (s *sink) ToolStarted(id, name string) { s.add("start " + id + " " + name) }
func (s *sink) ToolEnded(id, name string, isError bool) {
	if isError {
		s.add("end-error " + id + " " + name)
		return
	}
	s.add("end " + id + " " + name)
}
func (s *sink) ToolCommand(id, name, command string) {
	s.add("command " + id + " " + name + " " + command)
}
func (s *sink) ToolFiles(id, name string, paths []string) {
	s.add("files " + id + " " + strings.Join(paths, "+"))
}
func (s *sink) ToolQuery(id, name, query string) {
	s.add("query " + id + " " + query)
}
func (s *sink) add(e string) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}
func (s *sink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String() + " | " + strings.Join(s.events, "; ")
}

type observer struct {
	mu     sync.Mutex
	usages []ccstream.Usage
	limits []map[string]any
}

func (o *observer) Usage(u ccstream.Usage, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if inTurn {
		o.usages = append(o.usages, u)
	}
}
func (o *observer) RateLimit(info map[string]any, inTurn bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.limits = append(o.limits, info)
}

// One process handles many turns in a row: text is streamed to the sink (not sub-Agent text), plus the Bash tool's
// start, end and command (recorded as bash), per-model usage and init info; unknown output types are ignored.
func TestTurnsTextToolsUsageAndInit(t *testing.T) {
	c := start(t, ccstream.Options{})
	obs := &observer{}
	c.SetObserver(obs)
	s := newSink()
	res, err := c.Prompt(ctx(t), "fake:tool echo hi", s)
	if err != nil || res.Outcome != runturn.OutcomeCompleted || res.Text != "ran" {
		t.Fatalf("tool turn: %+v %v", res, err)
	}
	if got := s.String(); got != "ran | start toolu_fake_1 Bash; command toolu_fake_1 bash echo hi; end toolu_fake_1 Bash" {
		t.Fatalf("sink: %s", got)
	}
	init := c.Init()
	if init == nil || init.Version != "9.9.9" || init.SessionID != "11111111-2222-3333-4444-555555555555" || init.Model != "claude-haiku-fake" || strings.Join(init.Tools, ",") != "Bash,Read" {
		t.Fatalf("init: %+v", init)
	}
	s = newSink()
	if res, err = c.Prompt(ctx(t), "fake:subagent", s); err != nil || res.Text != "main" || strings.Contains(s.String(), "SUBAGENT") {
		t.Fatalf("sub-Agent text should not reach the sink: %+v %v %s", res, err, s)
	}
	if res, err = c.Prompt(ctx(t), "fake:usage2", nil); err != nil || res.Outcome != runturn.OutcomeCompleted {
		t.Fatalf("two models: %+v %v", res, err)
	}
	if res, err = c.Prompt(ctx(t), "fake:ratelimit", nil); err != nil || res.Text != "limited" {
		t.Fatalf("rate limit event: %+v %v", res, err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.usages) != 5 || obs.usages[0].Model != "claude-haiku-fake" || obs.usages[0].Input != 10 || obs.usages[0].Output != 5 || obs.usages[0].CacheRead != 3 || obs.usages[0].CacheWrite != 2 || obs.usages[0].Reasoning != nil {
		t.Fatalf("usage: %+v", obs.usages)
	}
	if two := obs.usages[2:4]; two[0].Model != "claude-haiku-fake" || two[1].Model != "claude-helper-fake" || two[1].Reasoning == nil || *two[1].Reasoning != 7 {
		t.Fatalf("one turn should have one entry per model: %+v", two)
	}
	if len(obs.limits) != 1 || obs.limits[0]["rateLimitType"] != "five_hour" {
		t.Fatalf("rate limit event: %+v", obs.limits)
	}
}

// Images are sent as base64 blocks along with the text.
func TestImages(t *testing.T) {
	c := start(t, ccstream.Options{})
	img := messageimage.Encode("image/png", []byte{0x89, 'P', 'N', 'G'})
	if res, err := c.Prompt(ctx(t), "fake:images", nil, img, img); err != nil || res.Text != "images=2" {
		t.Fatalf("images: %+v %v", res, err)
	}
}

// Errors claude reports are runtime_error with the message taken from result; for cases that need a closer look, such
// as not logged in, the raw fields are kept.
func TestFailures(t *testing.T) {
	c := start(t, ccstream.Options{})
	res, err := c.Prompt(ctx(t), "fake:error boom", nil)
	if err != nil || res.Outcome != runturn.OutcomeFailed || res.ErrorCode != executor.ErrRuntimeError || res.ErrorMessage != "boom" {
		t.Fatalf("error: %+v %v", res, err)
	}
	res, err = c.Prompt(ctx(t), "fake:notloggedin", nil)
	last := c.LastResult()
	if err != nil || res.ErrorCode != executor.ErrRuntimeError || last.TerminalReason != "api_error" || last.APIErrorStatus != nil || !strings.HasPrefix(last.Result, "Not logged in") {
		t.Fatalf("not logged in: %+v %+v %v", res, last, err)
	}
	if res, err = c.Prompt(ctx(t), "still alive", nil); err != nil || res.Text != "echo: still alive" {
		t.Fatalf("the process should keep working after an error: %+v %v", res, err)
	}
}

// Interrupt: when the control message is confirmed, the turn ends as interrupted, the process stays, and the next turn
// works normally.
func TestInterruptByControlMessage(t *testing.T) {
	c := start(t, ccstream.Options{})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:slow", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.Outcome != runturn.OutcomeAborted || !strings.HasPrefix(res.Text, ".") {
		t.Fatalf("interrupt: %+v", res)
	}
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatalf("interrupting with no turn in progress should do nothing: %v", err)
	}
	if res, err := c.Prompt(ctx(t), "after", nil); err != nil || res.Text != "echo: after" {
		t.Fatalf("next turn after the interrupt: %+v %v", res, err)
	}
}

// When the control message gets no confirmation, SIGINT is sent instead: the turn ends as interrupted and the process
// exits afterwards.
func TestInterruptFallsBackToSignal(t *testing.T) {
	c := start(t, ccstream.Options{InterruptGrace: 200 * time.Millisecond})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:noack", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Outcome != runturn.OutcomeAborted {
		t.Fatalf("SIGINT interrupt: %+v", res)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the process should exit after SIGINT")
	}
	if _, err := c.Prompt(ctx(t), "x", nil); !errors.Is(err, runturn.ErrExited) {
		t.Fatalf("message sent after exit: %v", err)
	}
}

// The process exits during a turn: runtime_exited, with the text received so far; messages sent afterwards return
// ErrExited.
func TestExitMidTurn(t *testing.T) {
	c := start(t, ccstream.Options{})
	if _, err := c.Prompt(ctx(t), "warm up", nil); err != nil {
		t.Fatal(err)
	}
	res, err := c.Prompt(ctx(t), "fake:exit", nil)
	if err != nil || res.Outcome != runturn.OutcomeFailed || res.ErrorCode != executor.ErrRuntimeExited || res.Text != "partial" {
		t.Fatalf("process exit: %+v %v", res, err)
	}
	<-c.Done()
	if c.ExitErr() == nil || !strings.Contains(c.Stderr(), "crashing on purpose") {
		t.Fatalf("exit code and error output: %v %q", c.ExitErr(), c.Stderr())
	}
	if _, err := c.Prompt(ctx(t), "x", nil); !errors.Is(err, runturn.ErrExited) {
		t.Fatalf("message sent after exit: %v", err)
	}
}

// Exiting right at startup (for example, an unknown argument) and no init for too long: both are runtime_start_failed,
// and the error output is left for the caller to add to the message.
func TestStartFailures(t *testing.T) {
	c := start(t, ccstream.Options{}, "FAKECLAUDE_EXIT_AT_START=1")
	<-c.Done()
	res, err := c.Prompt(ctx(t), "hi", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(c.Stderr(), "unknown option") {
		t.Fatalf("exit right at startup: %+v %v %q", res, err, c.Stderr())
	}
	slow := start(t, ccstream.Options{InitTimeout: 300 * time.Millisecond}, "FAKECLAUDE_NO_INIT=1")
	res, err = slow.Prompt(ctx(t), "hi", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeStartFailed || !strings.Contains(res.ErrorMessage, "timed out while starting") {
		t.Fatalf("startup timeout: %+v %v", res, err)
	}
	select {
	case <-slow.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the process should be ended after the startup timeout")
	}
}

// A process that was sent SIGINT is sure to exit afterwards, and a message written to it would be lost: sending the
// next message right after the interrupted turn ends does not write it, but waits for the process to exit and returns
// ErrExited, so the caller starts a new process with --resume.
func TestPromptAfterSignalWaitsForExit(t *testing.T) {
	c := start(t, ccstream.Options{InterruptGrace: 200 * time.Millisecond})
	s := newSink()
	done := make(chan runturn.Result, 1)
	go func() {
		res, _ := c.Prompt(ctx(t), "fake:noack", s)
		done <- res
	}()
	<-s.firstOut
	if err := c.Abort(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Outcome != runturn.OutcomeAborted {
		t.Fatalf("interrupt: %+v", res)
	}
	if !c.Exiting() {
		t.Fatal("should be marked as exiting after SIGINT was sent")
	}
	begin := time.Now()
	res, err := c.Prompt(ctx(t), "right after", nil)
	if !errors.Is(err, runturn.ErrExited) || res.Text != "" {
		t.Fatalf("the next message right after should not be written to the exiting process: %+v %v", res, err)
	}
	if time.Since(begin) > 4*time.Second {
		t.Fatalf("should return as soon as the process exits, took %v", time.Since(begin))
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("the process should have exited on return")
	}
}

// When a tool call appears only in the full assistant message, it is registered from there: start, command and end
// all match up.
func TestToolUseOnlyInAssistantMessage(t *testing.T) {
	c := start(t, ccstream.Options{})
	s := newSink()
	res, err := c.Prompt(ctx(t), "fake:tool2 ls -la", s)
	if err != nil || res.Text != "ran2" {
		t.Fatalf("result: %+v %v", res, err)
	}
	if got := s.String(); got != "ran2 | start toolu_fake_2 Bash; command toolu_fake_2 bash ls -la; end-error toolu_fake_2 Bash" {
		t.Fatalf("sink: %s", got)
	}
}

// Claude Code's file tools report their file and its search tools their pattern or query; nothing else of the input
// or the results.
func TestToolTargets(t *testing.T) {
	c := start(t, ccstream.Options{})
	s := newSink()
	res, err := c.Prompt(ctx(t), "fake:files", s)
	if err != nil || res.Text != "files" {
		t.Fatalf("result: %+v %v", res, err)
	}
	got := s.String()
	for _, want := range []string{"files toolu_read /w/a.go", "files toolu_edit /w/b.go", "files toolu_nb /w/n.ipynb", "query toolu_grep func main", "query toolu_glob **/*.go", "query toolu_web go 1.27 release"} {
		if !strings.Contains(got, want+";") {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "PRIVATE") {
		t.Fatalf("private input escaped: %s", got)
	}
}

// A line over the limit stops reading and ends the process: the turn ends with runtime_exited and does not hang.
func TestOverlongLineEndsTurn(t *testing.T) {
	c := start(t, ccstream.Options{MaxLine: 1024})
	if _, err := c.Prompt(ctx(t), "warm up", nil); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	res, err := c.Prompt(ctx(t), "fake:bigline", nil)
	if err != nil || res.ErrorCode != executor.ErrRuntimeExited {
		t.Fatalf("overlong line: %+v %v", res, err)
	}
	if time.Since(begin) > 10*time.Second {
		t.Fatalf("should not wait for the outer timeout: %v", time.Since(begin))
	}
}
