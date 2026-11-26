package local

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// startTask registers a running background task on the Agent's live process, as started by a run of its.
func startTask(t *testing.T, h *harness, id uuid.UUID, task string) *proc {
	t.Helper()
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	if p == nil {
		t.Fatal("no live process")
	}
	run := uuid.Must(uuid.NewV7())
	h.e.bgStarted(p, executor.BackgroundTask{ID: task, RunID: run, Kind: executor.BackgroundCommand, Label: "sleep 60"})
	return p
}

// lastReport is the last report of a task; it fails when there is none.
func lastReport(t *testing.T, obs *bgObs, task string) executor.BackgroundTask {
	t.Helper()
	r := obs.reports(task)
	if len(r) == 0 {
		t.Fatalf("no report of %s", task)
	}
	return r[len(r)-1]
}

func wantStopped(t *testing.T, obs *bgObs, task, reason string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r := lastReport(t, obs, task); r.Status != executor.BackgroundRunning {
			if r.Status != executor.BackgroundStopped || r.StopReason != reason || r.EndedAt == nil {
				t.Fatalf("%s: want stopped (%s), got %+v", task, reason, r)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s still running", task)
}

// A runtime process with background tasks running is not idle: the idle timeout leaves it alone (they would end with
// it), and it ends once they have ended and the timeout has passed again.
func TestIdleTimeoutSkipsProcessWithBackgroundTasks(t *testing.T) {
	obs := &bgObs{}
	h := newHarness(t, func(o *Options) { o.Observer, o.IdleTimeout = obs, 150*time.Millisecond }, "")
	id := h.agent(nil)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	p := startTask(t, h, id, "a")
	time.Sleep(500 * time.Millisecond)
	if h.pid(id) != p.rt.Pid() || lastReport(t, obs, "a").Status != executor.BackgroundRunning {
		t.Fatal("the process with a running background task was stopped as idle")
	}
	h.e.bgEnded(p, "a", executor.BackgroundEnd{Status: executor.BackgroundCompleted})
	deadline := time.Now().Add(3 * time.Second)
	for h.pid(id) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.pid(id) != 0 {
		t.Fatal("once its tasks ended, the idle process stops")
	}
}

// For a free slot, a process with running background tasks is never the one evicted.
func TestEvictionSkipsProcessWithBackgroundTasks(t *testing.T) {
	obs := &bgObs{}
	h := newHarness(t, func(o *Options) { o.Observer, o.MaxProcesses = obs, 1 }, "")
	a, b := h.agent(nil), h.agent(nil)
	h.exec(a, "hi")
	startTask(t, h, a, "a")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err := h.e.Execute(ctx, executor.Request{AgentID: b, RunID: uuid.Must(uuid.NewV7()), Message: "hi"}, newSink())
	if err == nil && h.pid(b) != 0 {
		t.Fatal("b got a slot by evicting a")
	}
	if h.pid(a) == 0 || lastReport(t, obs, "a").Status != executor.BackgroundRunning {
		t.Fatal("a and its task go on")
	}
}

// A change of settings restarts the process with the next message, stopping its background tasks (settings_changed).
func TestRestartForConfigChangeStopsTasks(t *testing.T) {
	obs := &bgObs{}
	h := newHarness(t, func(o *Options) { o.Observer = obs }, "")
	id := h.agent(nil)
	h.exec(id, "hi")
	startTask(t, h, id, "a")
	if restart, err := h.e.RestartOnNextMessage(context.Background(), id); err != nil || restart {
		t.Fatalf("same settings: %v %v", restart, err)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.ModelID = "claude-y" })
	if restart, err := h.e.RestartOnNextMessage(context.Background(), id); err != nil || !restart {
		t.Fatalf("new settings restart the process: %v %v", restart, err)
	}
	if restart, err := h.e.RestartOnNextMessage(context.Background(), uuid.New()); err != nil || restart {
		t.Fatalf("no process, nothing to restart: %v %v", restart, err)
	}
	h.exec(id, "again")
	wantStopped(t, obs, "a", executor.StopSettingsChanged)
}

// Starting a task from the Agent stops its background tasks (task_started) and goes on instead of failing as busy.
func TestFreezeForForkStopsTasks(t *testing.T) {
	obs := &bgObs{}
	h := newHarness(t, func(o *Options) { o.Observer = obs }, "")
	id := h.agent(nil)
	h.exec(id, "hi")
	startTask(t, h, id, "a")
	if err := h.e.FreezeForFork(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	wantStopped(t, obs, "a", executor.StopTaskStarted)
}

// Each way of ending the process records its background tasks as stopped for its own reason; an unexpected exit leaves
// them unknown.
func TestResetSwitchDeleteShutdownStopTasks(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		do           func(h *harness, id uuid.UUID, p *proc)
	}{
		{"new session", executor.StopSessionReset, func(h *harness, id uuid.UUID, _ *proc) { h.e.StopIdle(context.Background(), id, stopSessionReset) }},
		{"resume", executor.StopSessionReset, func(h *harness, id uuid.UUID, _ *proc) { h.e.StopIdle(context.Background(), id, stopSessionResumed) }},
		{"reload", executor.StopSettingsChanged, func(h *harness, id uuid.UUID, _ *proc) { h.e.StopIdle(context.Background(), id, stopConsoleReload) }},
		{"delete", executor.StopAgentDeleted, func(h *harness, id uuid.UUID, _ *proc) { h.e.Release(context.Background(), id) }},
		{"shutdown", executor.StopShutdown, func(h *harness, _ uuid.UUID, _ *proc) { h.e.Shutdown(context.Background()) }},
		{"switch", executor.StopRuntimeSwitched, func(h *harness, _ uuid.UUID, p *proc) {
			p.sessionStale.Store(true)
			h.e.stop(p, stopCause{reason: stopConfigChanged})
		}},
		{"reply stopped", executor.StopReplyStopped, func(h *harness, _ uuid.UUID, p *proc) { h.e.retire(p) }},
		{"exit", "", func(h *harness, _ uuid.UUID, p *proc) { _ = p.rt.CloseStdin() }},
	} {
		t.Run(c.name, func(t *testing.T) {
			obs := &bgObs{}
			h := newHarness(t, func(o *Options) { o.Observer = obs }, "")
			id := h.agent(nil)
			h.exec(id, "hi")
			p := startTask(t, h, id, "a")
			c.do(h, id, p)
			if c.reason == "" {
				deadline := time.Now().Add(5 * time.Second)
				for lastReport(t, obs, "a").Status == executor.BackgroundRunning && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if r := lastReport(t, obs, "a"); r.Status != executor.BackgroundUnknown || r.StopReason != "" {
					t.Fatalf("an unexpected exit leaves it unknown: %+v", r)
				}
				return
			}
			wantStopped(t, obs, "a", c.reason)
		})
	}
}

// exitHoldObs holds the wrap-up of a process that exited by itself at the record of its exit, until release is closed.
type exitHoldObs struct {
	recObs
	held    chan struct{}
	release chan struct{}
}

func (o *exitHoldObs) Event(agentID uuid.UUID, runID *uuid.UUID, e executor.Event) {
	o.recObs.Event(agentID, runID, e)
	if e.Type == store.EventPiExited {
		close(o.held)
		<-o.release
	}
}

// Shutdown returns only after the wrap-up of a process that exited by itself has ended, since that wrap-up still writes
// in the Agent's folder; within its context, as for the processes it stops itself.
func TestShutdownWaitsForWrapUpOfExitedProcess(t *testing.T) {
	obs := &exitHoldObs{held: make(chan struct{}), release: make(chan struct{})}
	h := newHarness(t, func(o *Options) { o.Observer = obs }, "")
	id := h.agent(nil)
	h.exec(id, "hi")
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	_ = p.rt.CloseStdin() // the process exits by itself; watch takes it out of the table and wraps it up
	select {
	case <-obs.held:
	case <-time.After(10 * time.Second):
		close(obs.release)
		t.Fatal("the exit was not recorded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	err := h.e.Shutdown(ctx)
	cancel()
	close(obs.release)
	if err == nil {
		t.Fatal("Shutdown returned while the exited process was still being wrapped up")
	}
	if err := h.e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.stopDone:
	default:
		t.Fatal("Shutdown returned before the wrap-up ended")
	}
}
