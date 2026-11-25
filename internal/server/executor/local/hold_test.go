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
// come (it goes into the same turn), on every runtime: no follow-up run is asked for. Codex replies with nothing.
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
			if res.Outcome != executor.OutcomeCompleted || res.Text != c.want || time.Since(start) < 150*time.Millisecond {
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
	follow, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, newSink())
	if err != nil || follow.Outcome != executor.OutcomeCompleted || strings.TrimSpace(follow.Text) != "reported 1" {
		t.Fatalf("agy's own reply is a follow-up run: %+v %v", follow, err)
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
