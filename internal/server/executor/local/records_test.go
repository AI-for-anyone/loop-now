package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// recObs records the run records the executor reports.
type recObs struct {
	mu      sync.Mutex
	events  []obsEvent
	usage   []obsUsage
	changed map[uuid.UUID]int
}

type obsEvent struct {
	agent uuid.UUID
	run   *uuid.UUID
	ev    executor.Event
}

type obsUsage struct {
	agent uuid.UUID
	run   *uuid.UUID
	u     executor.Usage
}

func (o *recObs) Event(agentID uuid.UUID, runID *uuid.UUID, e executor.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, obsEvent{agentID, copyID(runID), e})
}

func (o *recObs) Usage(agentID uuid.UUID, runID *uuid.UUID, u executor.Usage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.usage = append(o.usage, obsUsage{agentID, copyID(runID), u})
}

func (o *recObs) ProcessChanged(agentID uuid.UUID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.changed == nil {
		o.changed = map[uuid.UUID]int{}
	}
	o.changed[agentID]++
}

func copyID(id *uuid.UUID) *uuid.UUID {
	if id == nil {
		return nil
	}
	v := *id
	return &v
}

// eventsOf returns an Agent's events (in the order reported), optionally filtered by type.
func (o *recObs) eventsOf(agentID uuid.UUID, types ...string) []obsEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []obsEvent
	for _, e := range o.events {
		if e.agent != agentID {
			continue
		}
		if len(types) > 0 && !contains(types, e.ev.Type) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (o *recObs) usageOf(agentID uuid.UUID) []obsUsage {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []obsUsage
	for _, u := range o.usage {
		if u.agent == agentID {
			out = append(out, u)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// waitFor polls until cond holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *harness) execRun(id uuid.UUID, msg string) (uuid.UUID, executor.Result) {
	h.t.Helper()
	runID := uuid.Must(uuid.NewV7())
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: msg}, newSink())
	if err != nil {
		h.t.Fatalf("Execute: %v", err)
	}
	return runID, res
}

func types(evs []obsEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.ev.Type
	}
	return out
}

func TestRecordsStartReuseAndConfigChange(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)

	run1, res := h.execRun(id, "hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("the first execution failed: %+v", res)
	}
	evs := h.obs.eventsOf(id)
	if got := types(evs); strings.Join(got, ",") != store.EventWorkspaceSync+","+store.EventPiStarted {
		t.Fatalf("the events of the first start should be sync, start: %v", got)
	}
	for _, e := range evs {
		if e.run == nil || *e.run != run1 {
			t.Fatalf("the event should be linked to this execution: %+v", e)
		}
	}
	if sync := evs[0].ev.Data; sync["result"] != "succeeded" {
		t.Fatalf("the sync should succeed: %v", sync)
	}
	started := evs[1].ev.Data
	if started["reason"] != "no_process" || started["pid"] != h.pid(id) || started["configVersion"] != int64(1) {
		t.Fatalf("wrong start event: %v", started)
	}
	if _, ok := started["startupMs"].(int64); !ok {
		t.Fatalf("the start event should carry the startup time: %v", started)
	}

	run2, _ := h.execRun(id, "again")
	reused := h.obs.eventsOf(id, store.EventPiReused)
	if len(reused) != 1 || *reused[0].run != run2 || reused[0].ev.Data["pid"] != h.pid(id) {
		t.Fatalf("the second execution should reuse the process: %+v", reused)
	}

	oldPid := h.pid(id)
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	run3, _ := h.execRun(id, "after change")
	stopped := h.obs.eventsOf(id, store.EventPiStopped)
	if len(stopped) != 1 || stopped[0].ev.Data["reason"] != "config_changed" || *stopped[0].run != run3 || stopped[0].ev.Data["pid"] != oldPid {
		t.Fatalf("a config change should record stopping the old process: %+v", stopped)
	}
	starts := h.obs.eventsOf(id, store.EventPiStarted)
	if len(starts) != 2 || starts[1].ev.Data["reason"] != "config_changed" || *starts[1].run != run3 {
		t.Fatalf("a restart should be recorded after a config change: %+v", starts)
	}
	h.obs.mu.Lock()
	changed := h.obs.changed[id]
	h.obs.mu.Unlock()
	if changed == 0 {
		t.Fatal("process state changes should notify the observer")
	}
}

func TestRecordsUsageAttribution(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	runID, res := h.execRun(id, "!usage=100,20,1000,0 !respmodel=claude-x-2026 !tool=bash !toolusage=3,1 !compact=500,50 !warmafter=0,1,2000 hi")
	if res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("execution failed: %+v", res)
	}
	waitFor(t, "warm-up usage between two turns", func() bool { return len(h.obs.usageOf(id)) == 5 })
	usage := h.obs.usageOf(id)
	kinds := []string{}
	for _, u := range usage[:4] {
		kinds = append(kinds, u.u.Kind)
		if u.run == nil || *u.run != runID {
			t.Fatalf("usage within a turn should be linked to this execution: %+v", u)
		}
	}
	if strings.Join(kinds, ",") != "assistant,tool,compaction,assistant" {
		t.Fatalf("wrong usage order: %v", kinds)
	}
	if a := usage[0].u; a.Provider != "anthropic" || a.Model != "claude-x" || a.ResponseModel != "claude-x-2026" || a.Input != 100 || a.CacheRead != 1000 || a.StopReason != "toolUse" {
		t.Fatalf("wrong assistant usage: %+v", a)
	}
	if tool := usage[1].u; tool.Provider != "" || tool.Model != "" || tool.Input != 3 {
		t.Fatalf("tool usage should be recorded as an unknown model: %+v", tool)
	}
	if c := usage[2].u; c.Provider != "anthropic" || c.Model != "claude-x" || c.Input != 500 {
		t.Fatalf("compaction usage should count toward the model the process started with: %+v", c)
	}
	if w := usage[4]; w.run != nil || w.u.Kind != "cache_warm" || w.u.CacheRead != 2000 {
		t.Fatalf("warm-up between two turns should belong to no execution: %+v", w)
	}
	comp := h.obs.eventsOf(id, store.EventContextCompaction)
	if len(comp) != 1 || comp[0].ev.Data["result"] != "succeeded" || *comp[0].run != runID {
		t.Fatalf("one compaction should be recorded: %+v", comp)
	}
}

func TestRecordsRetry(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	runID, _ := h.execRun(id, "!retry hi")
	evs := h.obs.eventsOf(id, store.EventModelRetryStarted, store.EventModelRetryEnded)
	if len(evs) != 2 || evs[0].ev.Type != store.EventModelRetryStarted || evs[1].ev.Data["success"] != true || *evs[1].run != runID {
		t.Fatalf("the start and end of one retry should be recorded: %+v", evs)
	}
}

func TestRecordsIdleTimeout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.IdleTimeout = 200 * time.Millisecond }, "")
	id := h.agent(nil)
	h.execRun(id, "hi")
	waitFor(t, "stop on idle timeout", func() bool { return len(h.obs.eventsOf(id, store.EventPiStopped)) == 1 })
	st := h.obs.eventsOf(id, store.EventPiStopped)[0]
	if st.run != nil || st.ev.Data["reason"] != "idle_timeout" {
		t.Fatalf("wrong stop event for the idle timeout: %+v", st)
	}
	if len(h.obs.eventsOf(id, store.EventPiExited)) != 0 {
		t.Fatal("a deliberate stop should not be recorded as an unexpected exit")
	}
	if p := h.e.Process(id); p.State != executor.ProcessStopped || p.StartedAt != nil || p.PID != 0 {
		t.Fatalf("wrong process state after the stop: %+v", p)
	}
}

func TestRecordsCrashDuringRun(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.execRun(id, "first")
	runID, res := h.execRun(id, "!crash")
	if res.ErrorCode != executor.ErrPiExited || !strings.Contains(res.ErrorMessage, "simulated crash") {
		t.Fatalf("the crash error message should carry Pi's error output: %+v", res)
	}
	waitFor(t, "unexpected exit event", func() bool { return len(h.obs.eventsOf(id, store.EventPiExited)) == 1 })
	ex := h.obs.eventsOf(id, store.EventPiExited)[0]
	if ex.run == nil || *ex.run != runID || !strings.Contains(ex.ev.Data["stderrTail"].(string), "simulated crash") ||
		!strings.Contains(ex.ev.Data["exit"].(string), "exit status 3") {
		t.Fatalf("wrong event for a crash during a reply: %+v", ex)
	}
}

func TestRecordsIdleCrash(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.execRun(id, "hi")
	pid := h.pid(id)
	if pid == 0 {
		t.Fatal("there should be a live process")
	}
	_ = procgroup.Kill(pid)
	waitFor(t, "unexpected exit event while idle", func() bool { return len(h.obs.eventsOf(id, store.EventPiExited)) == 1 })
	ex := h.obs.eventsOf(id, store.EventPiExited)[0]
	if ex.run != nil {
		t.Fatalf("an exit while idle should not be linked to an execution: %+v", ex)
	}
}

func TestRecordsEvicted(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 1 }, "")
	a, b := h.agent(nil), h.agent(nil)
	h.execRun(a, "a")
	h.execRun(b, "b")
	st := h.obs.eventsOf(a, store.EventPiStopped)
	if len(st) != 1 || st[0].ev.Data["reason"] != "evicted" || st[0].ev.Data["forAgentId"] != b.String() || st[0].run != nil {
		t.Fatalf("freeing a slot should record the stop reason and the target Agent: %+v", st)
	}
}

func TestRecordsAbortTimeout(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.AbortTimeout = 500 * time.Millisecond }, "")
	id := h.agent(nil)
	runID := uuid.Must(uuid.NewV7())
	sink := newSink()
	done := make(chan struct{})
	go func() {
		_, _ = h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: runID, Message: "!sleep=10s !noabort"}, sink)
		close(done)
	}()
	<-sink.gotText
	_ = h.e.Interrupt(context.Background(), id, runID)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("should end after the interrupt timeout")
	}
	waitFor(t, "stop event for the interrupt timeout", func() bool { return len(h.obs.eventsOf(id, store.EventPiStopped)) == 1 })
	st := h.obs.eventsOf(id, store.EventPiStopped)[0]
	if st.ev.Data["reason"] != "abort_timeout" || st.run == nil || *st.run != runID {
		t.Fatalf("wrong stop event for the interrupt timeout: %+v", st)
	}
	if len(h.obs.eventsOf(id, store.EventPiExited)) != 0 {
		t.Fatal("a kill should not be recorded as an unexpected exit")
	}
}

func TestRecordsWorkspaceSyncFailure(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Sources = []store.FolderSource{
			{ID: uuid.Must(uuid.NewV7()), Path: "docs", Kind: "files"},
			{ID: uuid.Must(uuid.NewV7()), Path: "app", Kind: "git", GitURL: "file:///nonexistent/repo.git", GitBranch: "main"},
		}
	})
	runID, res := h.execRun(id, "hi")
	if res.ErrorCode != executor.ErrWorkspaceSyncFailed {
		t.Fatalf("should end with a workspace sync failure: %+v", res)
	}
	evs := h.obs.eventsOf(id, store.EventWorkspaceSync)
	if len(evs) != 1 || *evs[0].run != runID || evs[0].ev.Data["result"] != "failed" || !strings.Contains(evs[0].ev.Data["error"].(string), "Source app") {
		t.Fatalf("wrong sync failure event: %+v", evs)
	}
	sources := evs[0].ev.Data["sources"].([]map[string]any)
	if len(sources) != 2 || sources[0]["outcome"] != "files_synced" || sources[1]["outcome"] != "failed" || sources[1]["path"] != "app" {
		t.Fatalf("wrong per-source outcomes: %+v", sources)
	}
	if len(h.obs.eventsOf(id, store.EventPiStarted)) != 0 {
		t.Fatal("Pi should not start when the sync fails")
	}
}

func TestRecordsCLIInstall(t *testing.T) {
	skipWithoutCLITemplates(t)
	// A template with install packages only for other platforms: the install attempt fails with
	// "no install package for the current platform" and is recorded.
	dir := t.TempDir()
	tmpl := `{"name":"zz","version":"1.0.0","injection":"env","fields":[],"credential":{"env":{}},"binary":"zz",
		"install":{"plan9/mips":{"url":"https://example.invalid/zz.zip","sha256":"` + strings.Repeat("0", 64) + `","format":"zip","path":"zz"}}}`
	if err := os.WriteFile(filepath.Join(dir, "zz.json"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil, dir)
	if _, ok := h.e.tmpl.Get("zz"); !ok {
		t.Skip("the template directory override works differently than expected; skipping")
	}
	id := h.agent(func(s *agentconfig.Snapshot) { s.CLIs = []string{"zz"} })
	runID, res := h.execRun(id, "hi")
	if res.ErrorCode != executor.ErrCLIInstallFailed {
		t.Fatalf("should end with a CLI install failure: %+v", res)
	}
	evs := h.obs.eventsOf(id, store.EventCLIInstall)
	if len(evs) != 1 || *evs[0].run != runID || evs[0].ev.Data["result"] != "failed" || evs[0].ev.Data["cli"] != "zz" ||
		evs[0].ev.Data["version"] != "1.0.0" || !strings.Contains(evs[0].ev.Data["error"].(string), "current platform") {
		t.Fatalf("wrong install failure event: %+v", evs)
	}

	// A CLI that is already installed produces no install event.
	preinstall(t, h.root, "gh", "2.100.0", "gh")
	h2 := h.agent(func(s *agentconfig.Snapshot) { s.CLIs = []string{"gh"} })
	h.execRun(h2, "hi")
	if evs := h.obs.eventsOf(h2, store.EventCLIInstall); len(evs) != 0 {
		t.Fatalf("no install should be recorded when already installed: %+v", evs)
	}
}

// An interrupt only applies to the turn it names: when A has already ended and B is running, a late interrupt aimed at
// A must not affect B; an interrupt aimed at B takes effect as usual.
func TestInterruptTargetsOnlyGivenRun(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	ctx := context.Background()
	runA := uuid.Must(uuid.NewV7())
	if res, err := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: runA, Message: "first turn"}, newSink()); err != nil || res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("A should complete normally: %+v %v", res, err)
	}
	start := func(runID uuid.UUID, msg string) (*recSink, chan executor.Result) {
		sink, done := newSink(), make(chan executor.Result, 1)
		go func() {
			res, _ := h.e.Execute(ctx, executor.Request{AgentID: id, RunID: runID, Message: msg}, sink)
			done <- res
		}()
		<-sink.gotText // already printing text, waiting between two chunks of text
		return sink, done
	}
	wait := func(done chan executor.Result) executor.Result {
		select {
		case res := <-done:
			return res
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the execution to end")
			return executor.Result{}
		}
	}
	runB := uuid.Must(uuid.NewV7())
	_, doneB := start(runB, "!sleep=300ms second turn")
	_ = h.e.Interrupt(ctx, id, runA) // the late interrupt aimed at A
	if res := wait(doneB); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("an interrupt aimed at A should not affect B: %+v", res)
	}
	runC := uuid.Must(uuid.NewV7())
	_, doneC := start(runC, "!sleep=10s third turn")
	_ = h.e.Interrupt(ctx, id, runC)
	if res := wait(doneC); res.Outcome != executor.OutcomeInterrupted {
		t.Fatalf("an interrupt aimed at C should take effect: %+v", res)
	}
}
