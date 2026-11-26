package ccstream_test

import (
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor/local/ccstream"
	"loop-now/internal/server/executor/local/runturn"
)

// bgObs records the background tasks and usage a client reports.
type bgObs struct {
	mu      sync.Mutex
	started []ccstream.Task
	ended   chan ccstream.TaskEnd
	usage   []bool // inTurn of each usage report
}

func newBgObs() *bgObs { return &bgObs{ended: make(chan ccstream.TaskEnd, 8)} }

func (o *bgObs) Usage(_ ccstream.Usage, inTurn bool) {
	o.mu.Lock()
	o.usage = append(o.usage, inTurn)
	o.mu.Unlock()
}
func (o *bgObs) RateLimit(map[string]any, bool) {}
func (o *bgObs) TaskStarted(t ccstream.Task) {
	o.mu.Lock()
	o.started = append(o.started, t)
	o.mu.Unlock()
}
func (o *bgObs) TaskEnded(e ccstream.TaskEnd) { o.ended <- e }

// selfTurn records a turn Claude Code begins by itself.
type selfTurn struct {
	*sink
	ended chan runturn.Result
}

func (s *selfTurn) End(r runturn.Result) { s.ended <- r }

func withSelfTurns(c *ccstream.Client) chan *selfTurn {
	turns := make(chan *selfTurn, 4)
	c.SetSelfTurns(func() runturn.SelfTurn {
		s := &selfTurn{sink: newSink(), ended: make(chan runturn.Result, 1)}
		turns <- s
		return s
	})
	return turns
}

func recv[T any](t *testing.T, ch chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// A Bash command Claude Code moves to the background is reported once, with the command as its label; its end is
// reported with the output file, and the turn Claude Code then begins by itself goes to a self turn, with its usage.
func TestBackgroundTaskThenSelfTurn(t *testing.T) {
	c := start(t, ccstream.Options{})
	obs := newBgObs()
	c.SetObserver(obs)
	turns := withSelfTurns(c)
	res, err := c.Prompt(ctx(t), "fake:background 200ms sleep 20; echo finished", newSink())
	if err != nil || res.Outcome != runturn.OutcomeCompleted || res.Text != "started" {
		t.Fatalf("%+v %v", res, err)
	}
	obs.mu.Lock()
	started := append([]ccstream.Task(nil), obs.started...)
	obs.mu.Unlock()
	if len(started) != 1 || started[0] != (ccstream.Task{ID: "b1", ToolUseID: "toolu_bg_1", Type: "local_bash", Description: "Run it in the background", Command: "sleep 20; echo finished"}) {
		t.Fatalf("started once, with its command: %+v", started)
	}
	end := recv(t, obs.ended, "the end")
	if end.ID != "b1" || end.Status != "completed" || end.OutputFile == "" {
		t.Fatalf("end: %+v", end)
	}
	self := recv(t, turns, "a self turn")
	r := recv(t, self.ended, "the self turn's end")
	if r.Outcome != runturn.OutcomeCompleted || r.Text != "background done" || self.text.String() != "background done" {
		t.Fatalf("self turn: %+v %q", r, self.text.String())
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.usage) != 2 || !obs.usage[1] {
		t.Fatalf("the self turn's usage is in a turn: %v", obs.usage)
	}
}

// stop_task stops a background task: Claude Code answers the control request and reports the task as stopped.
func TestStopTask(t *testing.T) {
	c := start(t, ccstream.Options{})
	obs := newBgObs()
	c.SetObserver(obs)
	withSelfTurns(c)
	if _, err := c.Prompt(ctx(t), "fake:background 30s sleep 60", newSink()); err != nil {
		t.Fatal(err)
	}
	if err := c.StopTask(ctx(t), "b1"); err != nil {
		t.Fatal(err)
	}
	if end := recv(t, obs.ended, "the end"); end.ID != "b1" || end.Status != "stopped" {
		t.Fatalf("stopped: %+v", end)
	}
}

// A message sent while Claude Code is in a turn of its own waits for that turn to end: the turn's output is not taken
// for the reply to the message.
func TestPromptWaitsForSelfTurn(t *testing.T) {
	c := start(t, ccstream.Options{})
	c.SetObserver(newBgObs())
	turns := withSelfTurns(c)
	if _, err := c.Prompt(ctx(t), "fake:background 100ms true", newSink()); err != nil {
		t.Fatal(err)
	}
	self := recv(t, turns, "a self turn")
	res, err := c.Prompt(ctx(t), "hello", newSink())
	if err != nil || res.Text != "echo: hello" {
		t.Fatalf("the reply to the message: %+v %v", res, err)
	}
	if r := recv(t, self.ended, "the self turn's end"); r.Text != "background done" {
		t.Fatalf("self turn: %+v", r)
	}
}

// A task that ends while a turn is going on is reported, and Claude Code tells about it in that turn: no self turn.
func TestTaskEndedDuringTurn(t *testing.T) {
	c := start(t, ccstream.Options{})
	obs := newBgObs()
	c.SetObserver(obs)
	turns := withSelfTurns(c)
	if _, err := c.Prompt(ctx(t), "fake:background 100ms true", newSink()); err != nil {
		t.Fatal(err)
	}
	if res, err := c.Prompt(ctx(t), "fake:wait 500ms", newSink()); err != nil || res.Text != "waited" {
		t.Fatalf("%+v %v", res, err)
	}
	recv(t, obs.ended, "the end")
	select {
	case s := <-turns:
		t.Fatalf("no self turn, got one: %+v", s)
	case <-time.After(300 * time.Millisecond):
	}
}

// A turn Claude Code began by itself that never ends (its result is lost) ends after SelfQuiet without output, so the
// next message does not wait for it for good.
func TestSelfTurnEndsWhenQuiet(t *testing.T) {
	c := start(t, ccstream.Options{SelfQuiet: 300 * time.Millisecond}, "FAKECLAUDE_FOLLOWUP_NO_RESULT=1")
	c.SetObserver(newBgObs())
	turns := withSelfTurns(c)
	if _, err := c.Prompt(ctx(t), "fake:background 100ms true", newSink()); err != nil {
		t.Fatal(err)
	}
	self := recv(t, turns, "a self turn")
	if r := recv(t, self.ended, "its end"); r.Outcome != runturn.OutcomeCompleted || r.Text != "background done" {
		t.Fatalf("ended when quiet: %+v", r)
	}
	if res, err := c.Prompt(ctx(t), "hello", newSink()); err != nil || res.Text != "echo: hello" {
		t.Fatalf("the next message runs: %+v %v", res, err)
	}
}

// Continuing a session in which background tasks were still running, Claude Code 2.1.332 first reports them as stopped
// and settles them in an empty turn of its own (result success, empty, num_turns 0), even when a message is already
// waiting: that empty result is not the reply to the message, the tasks are not reported (this process never started
// them), and no turn of its own is recorded.
func TestStaleTasksTurnIsNotTheReply(t *testing.T) {
	c := start(t, ccstream.Options{}, "FAKECLAUDE_STALE_TASK=bold1,bold2")
	obs := newBgObs()
	c.SetObserver(obs)
	turns := withSelfTurns(c)
	s := newSink()
	res, err := c.Prompt(ctx(t), "hello", s)
	if err != nil || res.Outcome != runturn.OutcomeCompleted || res.Text != "echo: hello" || s.text.String() != "echo: hello" {
		t.Fatalf("the reply to the message: %+v %v %q", res, err, s.text.String())
	}
	if res, err := c.Prompt(ctx(t), "again", newSink()); err != nil || res.Text != "echo: again" {
		t.Fatalf("the next message: %+v %v", res, err)
	}
	noStaleReport(t, obs, turns)
}

// The same with no message waiting: the empty turn Claude Code gives to settle earlier tasks is not recorded as a turn
// of its own, and the message sent after it gets its reply.
func TestStaleTasksTurnWithNoMessageMakesNoTurn(t *testing.T) {
	c := start(t, ccstream.Options{}, "FAKECLAUDE_STALE_TASK=bold1")
	obs := newBgObs()
	c.SetObserver(obs)
	turns := withSelfTurns(c)
	deadline := time.Now().Add(10 * time.Second)
	for c.Init() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Init() == nil {
		t.Fatal("the settling turn did not start")
	}
	if res, err := c.Prompt(ctx(t), "hello", newSink()); err != nil || res.Outcome != runturn.OutcomeCompleted || res.Text != "echo: hello" {
		t.Fatalf("the reply to the message: %+v %v", res, err)
	}
	noStaleReport(t, obs, turns)
}

func noStaleReport(t *testing.T, obs *bgObs, turns chan *selfTurn) {
	t.Helper()
	select {
	case s := <-turns:
		t.Fatalf("a turn of its own was recorded: %q", s.text.String())
	case e := <-obs.ended:
		t.Fatalf("a task this process never started was reported: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}
