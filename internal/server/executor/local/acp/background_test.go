package acp_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/executor/local/acp"
	"loop-now/internal/server/executor/local/runturn"
)

// bgObserver records the background tasks and usage a client reports.
type bgObserver struct {
	mu      sync.Mutex
	started []acp.Task
	ended   chan acp.TaskEnd
	usage   []acp.Usage
}

func newBgObserver() *bgObserver { return &bgObserver{ended: make(chan acp.TaskEnd, 4)} }

func (o *bgObserver) Usage(u acp.Usage) {
	o.mu.Lock()
	o.usage = append(o.usage, u)
	o.mu.Unlock()
}
func (o *bgObserver) TaskStarted(t acp.Task) {
	o.mu.Lock()
	o.started = append(o.started, t)
	o.mu.Unlock()
}
func (o *bgObserver) TaskEnded(e acp.TaskEnd) { o.ended <- e }

type selfTurn struct {
	*sink
	ended chan runturn.Result
}

func (s *selfTurn) End(r runturn.Result) { s.ended <- r }

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

func grokWithTasks(t *testing.T) (*acp.Client, string, *bgObserver, chan *selfTurn) {
	t.Helper()
	h := newHarness(t, "grok")
	c, _ := h.start(acp.Options{})
	obs := newBgObserver()
	c.SetObserver(obs)
	turns := make(chan *selfTurn, 4)
	c.SetSelfTurns(func(string) runturn.SelfTurn {
		s := &selfTurn{sink: &sink{}, ended: make(chan runturn.Result, 1)}
		turns <- s
		return s
	})
	sess, err := c.NewSession(ctx(t), h.cwd, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, sess.ID, obs, turns
}

// Grok's command moved to the background is reported with its command and tool call; its end comes with the command's
// output and exit code, and the prompt Grok then runs by itself (task-completed-<id>) goes to a self turn, which ends
// at its turn_completed, with that prompt's usage.
func TestGrokBackgroundTaskAndSelfTurn(t *testing.T) {
	c, sid, obs, turns := grokWithTasks(t)
	r := prompt(t, c, sid, "fake:bgtask 200ms sleep 20; echo finished", nil)
	if r.Outcome != runturn.OutcomeCompleted || r.Text != "started" {
		t.Fatalf("%+v", r)
	}
	obs.mu.Lock()
	started := append([]acp.Task(nil), obs.started...)
	promptUsage := len(obs.usage)
	obs.mu.Unlock()
	if len(started) != 1 || started[0].Command != "sleep 20; echo finished" || started[0].ToolCallID == "" || started[0].ID == "" || started[0].Description != "Run it" {
		t.Fatalf("started: %+v", started)
	}
	end := recv(t, obs.ended, "the end")
	if end.ID != started[0].ID || end.ExitCode == nil || *end.ExitCode != 0 || end.Killed || end.Output == nil || !strings.HasSuffix(*end.Output, "finished\n") {
		t.Fatalf("end: %+v", end)
	}
	self := recv(t, turns, "a self turn")
	res := recv(t, self.ended, "the self turn's end")
	if res.Outcome != runturn.OutcomeCompleted || res.Text != "background done" || self.String() != "background done" {
		t.Fatalf("self turn: %+v %q", res, self.String())
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.usage) != promptUsage+1 || obs.usage[len(obs.usage)-1].Input != 700 || obs.usage[len(obs.usage)-1].Model != "fake-1-build" {
		t.Fatalf("the self turn's usage: %+v", obs.usage)
	}
}

// _x.ai/task/kill stops one background task: it ends as killed.
func TestGrokKillTask(t *testing.T) {
	c, sid, obs, _ := grokWithTasks(t)
	prompt(t, c, sid, "fake:bgtask 30s sleep 60", nil)
	obs.mu.Lock()
	task := obs.started[0].ID
	obs.mu.Unlock()
	if err := c.KillTask(ctx(t), sid, task); err != nil {
		t.Fatal(err)
	}
	if end := recv(t, obs.ended, "the end"); end.ID != task || !end.Killed || end.ExitCode != nil {
		t.Fatalf("killed: %+v", end)
	}
	if err := c.KillTask(ctx(t), "no-such-session", task); err == nil {
		t.Fatal("an unknown session is an error")
	}
}

// Grok's announcement that it runs one of the client's own prompts, coming late (after the prompt answered), is not a
// prompt of Grok's own: the next prompt does not wait for it.
func TestGrokLateQueueIsNotASelfTurn(t *testing.T) {
	h := newHarness(t, "grok")
	h.env = []string{"FAKEACP_LATE_QUEUE=1"}
	c, _ := h.start(acp.Options{})
	turns := make(chan *selfTurn, 4)
	c.SetSelfTurns(func(string) runturn.SelfTurn {
		s := &selfTurn{sink: &sink{}, ended: make(chan runturn.Result, 1)}
		turns <- s
		return s
	})
	sess := newSession(t, c, h)
	prompt(t, c, sess, "hello", nil)
	time.Sleep(200 * time.Millisecond) // the late announcement comes
	start := time.Now()
	if r := prompt(t, c, sess, "again", nil); r.Text != "echo: again" || time.Since(start) > 2*time.Second {
		t.Fatalf("the next prompt runs at once: %+v after %v", r, time.Since(start))
	}
	select {
	case s := <-turns:
		t.Fatalf("no self turn: %+v", s)
	default:
	}
}
