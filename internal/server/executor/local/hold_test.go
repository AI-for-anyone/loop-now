package local

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// asTask makes the Agent's snapshot a task instance's.
func asTask(s *agentconfig.Snapshot) {
	s.TaskID, s.ConfigurationAgentID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
}

// A task instance's turn ends only once the background work it started has ended and the CLI's own reply about it has
// come (it goes into the same turn), on every runtime: no follow-up run is asked for. Codex replies with nothing, and
// its turn does not wait for a reply (the reply wait is minutes).
func TestTaskInstanceHoldsAllRuntimes(t *testing.T) {
	cases := []struct {
		name, msg, want string
		agent           func(t *testing.T) (*harness, uuid.UUID, *followUpHost)
	}{
		{"claude", "fake:background 200ms true", "started\n\nbackground done", func(t *testing.T) (*harness, uuid.UUID, *followUpHost) {
			h, _, host := newClaudeBg(t)
			return h.harness, h.claudeAgent(asTask), host
		}},
		{"grok", "fake:bgtask 200ms true", "started\n\nbackground done", func(t *testing.T) (*harness, uuid.UUID, *followUpHost) {
			h, _, host := newGrokBg(t)
			return h.harness, h.acpAgent(asTask), host
		}},
		{"antigravity", "fake:background 300ms", "backgrounded\n\nreported 1", func(t *testing.T) (*harness, uuid.UUID, *followUpHost) {
			obs, host := &bgObs{}, newFollowUpHost()
			h := newACPHarness(t, "agy", func(o *Options) { o.Observer = obs })
			h.e.SetFollowUps(host)
			return h.harness, h.acpAgent(asTask), host
		}},
		{"codex", "fake:background 200ms true", "started", func(t *testing.T) (*harness, uuid.UUID, *followUpHost) {
			h, _, host := newCodexBg(t)
			return h.harness, h.codexAgent(asTask), host
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, id, host := c.agent(t)
			start := time.Now()
			res, sink := h.exec(id, c.msg)
			if took := time.Since(start); res.Outcome != executor.OutcomeCompleted || res.Text != c.want || took < 150*time.Millisecond || took > 30*time.Second {
				t.Fatalf("the turn holds until the work ended: %+v after %v", res, time.Since(start))
			}
			if got := sink.text.String(); got != c.want {
				t.Fatalf("the CLI's own reply goes into the turn: %q", got)
			}
			select {
			case call := <-host.calls:
				t.Fatalf("no follow-up run for a task instance: %+v", call)
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
}

// When nothing comes from the runtime for the background wait, the held turn stops its background tasks (task_limit),
// records why and ends.
func TestTaskInstanceLimitStopsTasks(t *testing.T) {
	obs := &bgObs{}
	h := newClaudeHarness(t, func(o *Options) {
		o.Observer, o.ACPBackgroundWait, o.BackgroundStopWait = obs, 500*time.Millisecond, 2*time.Second
	})
	id := h.claudeAgent(asTask)
	start := time.Now()
	res, _ := h.exec(id, "fake:background 30s sleep 60")
	if res.Outcome != executor.OutcomeCompleted || !strings.HasPrefix(res.Text, "started") || time.Since(start) < 500*time.Millisecond || time.Since(start) > 10*time.Second {
		t.Fatalf("the turn ends at the limit: %+v after %v", res, time.Since(start))
	}
	end := waitEnded(t, obs, "b1")
	if end.Status != executor.BackgroundStopped || end.StopReason != executor.StopTaskLimit {
		t.Fatalf("stopped by the limit: %+v", end)
	}
	if evs := obs.eventsOf(id, store.EventBackgroundWaitTimeout); len(evs) != 1 || evs[0].ev.Data["limitMs"] != int64(500) {
		t.Fatalf("the run record says why: %+v", evs)
	}
}

// Stopping a held turn stops the background tasks it waits for (reply_stopped).
func TestTaskInstanceStopEndsHeldTasks(t *testing.T) {
	obs := &bgObs{}
	h := newClaudeHarness(t, func(o *Options) { o.Observer, o.BackgroundStopWait = obs, 2*time.Second })
	id := h.claudeAgent(asTask)
	run := uuid.Must(uuid.NewV7())
	done := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:background 30s sleep 60"}, newSink())
		done <- res
	}()
	deadline := time.Now().Add(10 * time.Second)
	for len(obs.reports("b1")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.e.Interrupt(context.Background(), id, run); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Fatalf("interrupted: %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the held turn did not end")
	}
	if end := waitEnded(t, obs, "b1"); end.Status != executor.BackgroundStopped || end.StopReason != executor.StopReplyStopped {
		t.Fatalf("stopped with the reply: %+v", end)
	}
}

// An Antigravity Agent's reply ends with its prompt: the command still running is a background task, and agy's own
// reply after it ends becomes a follow-up run.
func TestAntigravityNoHoldForAgents(t *testing.T) {
	obs, host := &bgObs{}, newFollowUpHost()
	h := newACPHarness(t, "agy", func(o *Options) { o.Observer = obs })
	h.e.SetFollowUps(host)
	id := h.acpAgent(nil)
	run := uuid.Must(uuid.NewV7())
	start := time.Now()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:background 300ms"}, newSink())
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != "backgrounded" || time.Since(start) > 250*time.Millisecond {
		t.Fatalf("no hold: %+v %v after %v", res, err, time.Since(start))
	}
	task := lastItem(t, obs)
	if r := obs.reports(task)[0]; r.RunID != run || r.Label != "sleep 300ms" || r.Runtime != "antigravity" {
		t.Fatalf("listed: %+v", r)
	}
	if end := waitEnded(t, obs, task); end.Status != executor.BackgroundCompleted || end.Output == nil || *end.Output != "finished" {
		t.Fatalf("ended: %+v", end)
	}
	call := host.next(t)
	sink := newSink()
	follow, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, sink)
	if err != nil || follow.Outcome != executor.OutcomeCompleted || follow.Text != "reported 1" || sink.text.String() != "reported 1" {
		t.Fatalf("agy's own reply is a follow-up run, without its leading blank lines: %+v %v %q", follow, err, sink.text.String())
	}
}

// Stopping an Antigravity task cancels the session's background commands (agy has no means to stop one): each ends as
// stopped.
func TestAntigravityStopBackground(t *testing.T) {
	obs := &bgObs{}
	h := newACPHarness(t, "agy", func(o *Options) { o.Observer, o.BackgroundStopWait = obs, 2*time.Second })
	id := h.acpAgent(nil)
	h.exec(id, "fake:background 30s 40s")
	running := h.e.bgRunning(h.e.liveProc(id))
	if len(running) != 2 {
		t.Fatalf("two tasks: %+v", running)
	}
	got, err := h.e.StopBackground(context.Background(), id, running[0].ID, executor.StopByPerson)
	if err != nil || got.Status != executor.BackgroundStopped || got.StopReason != executor.StopByPerson {
		t.Fatalf("stopped: %+v %v", got, err)
	}
	if other := waitEnded(t, obs, running[1].ID); other.Status != executor.BackgroundStopped || other.StopReason != executor.StopByPerson {
		t.Fatalf("the other one ends with it: %+v", other)
	}
}

// agy starts commands in its own replies too (no prompt open): one still running when that reply ends is a background
// task of the held turn like the prompt's, and the turn waits for it and for agy's next own reply.
// (The second command outlasts acpSettle, so it is still running when its reply ends.)
func TestTaskInstanceHoldsForOwnReplyCommands(t *testing.T) {
	obs, host := &bgObs{}, newFollowUpHost()
	h := newACPHarness(t, "agy", func(o *Options) { o.Observer = obs })
	h.e.SetFollowUps(host)
	id := h.acpAgent(asTask)
	run := uuid.Must(uuid.NewV7())
	sink := newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:background 200ms+4s"}, sink)
	want := "backgrounded\n\nreported 1\n\nreported 1"
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != want || sink.text.String() != want {
		t.Fatalf("the turn waits for the command of the own reply and the reply after it: %+v %v %q", res, err, sink.text.String())
	}
	obs.bgMu.Lock()
	tasks := map[string]executor.BackgroundTask{}
	for _, r := range obs.tasks {
		tasks[r.t.ID] = r.t
	}
	obs.bgMu.Unlock()
	if len(tasks) != 2 {
		t.Fatalf("two background tasks: %+v", tasks)
	}
	for _, task := range tasks {
		if task.RunID != run || task.Status != executor.BackgroundCompleted {
			t.Fatalf("each belongs to the held turn and ended there: %+v", task)
		}
	}
	select {
	case call := <-host.calls:
		t.Fatalf("no follow-up run for a task instance: %+v", call)
	case <-time.After(300 * time.Millisecond):
	}
}

// For an Antigravity Agent, a command agy starts in its own reply is listed on that reply's run, and its end brings a
// follow-up run of its own.
func TestAntigravityOwnReplyCommandIsListed(t *testing.T) {
	obs, host := &bgObs{}, newFollowUpHost()
	h := newACPHarness(t, "agy", func(o *Options) { o.Observer = obs })
	h.e.SetFollowUps(host)
	id := h.acpAgent(nil)
	if res, _ := h.exec(id, "fake:background 200ms+4s"); res.Text != "backgrounded" {
		t.Fatalf("no hold: %+v", res)
	}
	first := lastItem(t, obs)
	call := host.next(t)
	follow, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, newSink())
	if err != nil || follow.Text != "reported 1" {
		t.Fatalf("the first own reply: %+v %v", follow, err)
	}
	second := lastItem(t, obs)
	if second == first {
		t.Fatal("the command of the own reply is not listed")
	}
	if r := obs.reports(second)[0]; r.RunID != call.run || r.Label != "sleep 4s" || r.Status != executor.BackgroundRunning {
		t.Fatalf("listed on the own reply's run: %+v", r)
	}
	if end := waitEnded(t, obs, second); end.Status != executor.BackgroundCompleted {
		t.Fatalf("ended: %+v", end)
	}
	next := host.next(t)
	if len(next.triggers) != 1 || next.triggers[0].ID != second {
		t.Fatalf("a follow-up run for its end: %+v", next)
	}
	if res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: next.run, Origin: executor.OriginBackground}, newSink()); err != nil || res.Text != "reported 1" {
		t.Fatalf("the next own reply: %+v %v", res, err)
	}
}

// A background task that starts and ends inside the CLI's own reply (Claude Code's watcher that fails at once) brings
// another own reply after that one: the held turn waits for it and takes it, instead of ending with nothing running.
func TestTaskInstanceHoldsAfterTaskEndedInFollowUp(t *testing.T) {
	obs, host := &bgObs{}, newFollowUpHost()
	h := newClaudeHarness(t, func(o *Options) { o.Observer = obs })
	h.e.SetFollowUps(host)
	id := h.claudeAgent(asTask)
	run := uuid.Must(uuid.NewV7())
	sink := newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:background 200ms then:50ms"}, sink)
	want := "started\n\nbackground done\n\nbackground done"
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != want || sink.text.String() != want {
		t.Fatalf("the turn takes the reply about the task that ended inside the follow-up: %+v %v %q", res, err, sink.text.String())
	}
	for _, task := range []string{"b1", "b2"} {
		if end := waitEnded(t, obs, task); end.RunID != run || end.Status != executor.BackgroundCompleted {
			t.Fatalf("%s belongs to the held turn and ended: %+v", task, end)
		}
	}
	select {
	case call := <-host.calls:
		t.Fatalf("no follow-up run for a task instance: %+v", call)
	case <-time.After(300 * time.Millisecond):
	}
}
