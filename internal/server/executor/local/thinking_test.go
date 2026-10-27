package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

func TestThinkingLevelLaunchReuseAndDefaultReset(t *testing.T) {
	h := newHarness(t, nil, "")
	high, medium := "high", "medium"
	id := h.agent(func(s *agentconfig.Snapshot) { s.ThinkingLevel = &high })
	if result, _ := h.exec(id, "one"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	state, e := p.client.GetState(context.Background())
	if e != nil || state.ThinkingLevel != "high" {
		t.Fatal("launch level", state, e)
	}
	// A live session can carry a prior explicit value. The next round's
	// resolved null/default must be applied even if the process is reused.
	h.src.mu.Lock()
	h.src.snaps[id].ThinkingLevel = &medium
	h.src.mu.Unlock()
	if result, _ := h.exec(id, "two"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	h.e.mu.Lock()
	same := h.e.procs[id]
	h.e.mu.Unlock()
	state, e = p.client.GetState(context.Background())
	if e != nil || same != p || state.ThinkingLevel != "medium" {
		t.Fatal("default reset retained old explicit level", state, e)
	}
	// The CLI launch also carries the effective level explicitly on resume.
	args := piArgs(h.e.dirs(id), agentconfig.Snapshot{ThinkingLevel: &medium}, nil, "existing-session")
	if argValue(args, "--thinking") != "medium" || argValue(args, "--session") != "existing-session" {
		t.Fatal("resume omitted explicit default", args)
	}
}

func TestInterruptDuringThinkingPreparationNeverPrompts(t *testing.T) {
	barrier := filepath.Join(t.TempDir(), "thinking")
	h := newHarness(t, func(o *Options) { o.Passthrough = append(o.Passthrough, "FAKEPI_THINKING_BARRIER") }, "")
	h.env["FAKEPI_THINKING_BARRIER"] = barrier
	high := "high"
	id := h.agent(func(s *agentconfig.Snapshot) { s.ThinkingLevel = &high })
	run := uuid.New()
	sink := newSink()
	done := make(chan executor.Result, 1)
	fail := make(chan error, 1)
	go func() {
		out, e := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "must-not-reach-Pi"}, sink)
		done <- out
		fail <- e
	}()
	waitFor(t, "thinking preparation barrier", func() bool { _, e := os.Stat(barrier + ".ready"); return e == nil })
	h.e.mu.Lock()
	r := h.e.runs[id]
	h.e.mu.Unlock()
	r.mu.Lock()
	prompting := r.prompting
	r.mu.Unlock()
	if prompting {
		t.Fatal("thinking preparation marked prompting")
	}
	if e := h.e.Interrupt(context.Background(), id, run); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(barrier+".release", []byte("release"), 0600); e != nil {
		t.Fatal(e)
	}
	select {
	case out := <-done:
		if e := <-fail; e != nil || out.Outcome != executor.OutcomeInterrupted || out.ErrorCode != "" {
			t.Fatal("preparation cancel reported model error", out, e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel stuck")
	}
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	stats, e := p.client.Call(context.Background(), "get_session_stats", nil)
	if e != nil {
		t.Fatal(e)
	}
	// A real fixture process reports counts, not a successful mock Prompt.
	var counts struct {
		UserMessages  int `json:"userMessages"`
		TotalMessages int `json:"totalMessages"`
	}
	if json.Unmarshal(stats, &counts) != nil || counts.UserMessages != 0 || counts.TotalMessages != 0 {
		t.Fatal("cancelled preparation still sent Prompt", string(stats))
	}
	sink.mu.Lock()
	running := sink.running
	sink.mu.Unlock()
	if running != 0 {
		t.Fatal("cancelled preparation entered running")
	}
}
