package local

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/executor"
)

func TestUnlimitedProcessesAdmitsConcurrentRuntimesWithoutEviction(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 0 }, "")
	idle := h.agent(nil)
	if result, _ := h.exec(idle, "idle"); result.Outcome != executor.OutcomeCompleted {
		t.Fatalf("idle setup: %+v", result)
	}
	idlePID := h.pid(idle)
	const count = 6
	ids := make([]uuid.UUID, count)
	runIDs := make([]uuid.UUID, count)
	sinks := make([]*recSink, count)
	type outcome struct {
		result executor.Result
		err    error
	}
	done := make([]chan outcome, count)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := range ids {
		ids[i] = h.agent(nil)
		runIDs[i] = uuid.New()
		sinks[i] = newSink()
		done[i] = make(chan outcome, 1)
	}
	for i, id := range ids {
		go func(i int, id uuid.UUID) {
			result, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: runIDs[i], Message: "!sleep=30s !text=first_second_third"}, sinks[i])
			done[i] <- outcome{result, err}
		}(i, id)
	}
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for i, sink := range sinks {
		select {
		case <-sink.gotText:
		case result := <-done[i]:
			t.Fatalf("runtime %d finished before concurrent admission: %+v", i, result)
		case <-deadline.C:
			t.Fatalf("runtime %d failed to enter concurrently", i)
		}
	}
	h.e.mu.Lock()
	total, busy, waiting := len(h.e.procs), 0, len(h.e.waiters)
	for _, p := range h.e.procs {
		if p.busy {
			busy++
		}
	}
	h.e.mu.Unlock()
	if total != count+1 || busy != count || waiting != 0 {
		t.Fatalf("processes=%d busy=%d waiting=%d", total, busy, waiting)
	}
	if h.pid(idle) != idlePID {
		t.Fatal("unlimited admission evicted an idle process")
	}
	for i, id := range ids {
		if h.pid(id) == 0 {
			t.Fatalf("busy runtime %d was evicted", i)
		}
		sinks[i].mu.Lock()
		waiting := slices.Contains(sinks[i].phases, executor.PhaseWaitingSlot)
		sinks[i].mu.Unlock()
		if waiting {
			t.Fatalf("runtime %d reported waiting_slot", i)
		}
	}
	// Unlimited global admission must not bypass same-runtime serialization.
	if _, err := h.e.Execute(ctx, executor.Request{AgentID: ids[0], RunID: uuid.New(), Message: "duplicate"}, newSink()); err == nil {
		t.Fatal("same runtime admitted a concurrent turn")
	}
	for i, id := range ids {
		if err := h.e.Interrupt(context.Background(), id, runIDs[i]); err != nil {
			t.Fatal(err)
		}
	}
	for i := range ids {
		select {
		case outcome := <-done[i]:
			if outcome.err != nil || outcome.result.Outcome != executor.OutcomeInterrupted {
				t.Fatalf("interrupt %d: %+v", i, outcome)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("interrupt %d timed out", i)
		}
	}
}
