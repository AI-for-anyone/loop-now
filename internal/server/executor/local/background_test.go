package local

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// bgObs records the background task reports, besides the other run records.
type bgObs struct {
	recObs
	bgMu  sync.Mutex
	tasks []obsTask
}

type obsTask struct {
	agent uuid.UUID
	t     executor.BackgroundTask
}

func (o *bgObs) BackgroundTask(agentID uuid.UUID, t executor.BackgroundTask) {
	o.bgMu.Lock()
	defer o.bgMu.Unlock()
	o.tasks = append(o.tasks, obsTask{agentID, t})
}

func (o *bgObs) reports(id string) []executor.BackgroundTask {
	o.bgMu.Lock()
	defer o.bgMu.Unlock()
	var out []executor.BackgroundTask
	for _, r := range o.tasks {
		if r.t.ID == id {
			out = append(out, r.t)
		}
	}
	return out
}

// newBgHarness runs fakepi with an observer that keeps background tasks, and returns the live process of an Agent that
// has had one turn.
func newBgHarness(t *testing.T) (*harness, *bgObs, uuid.UUID, *proc) {
	t.Helper()
	obs := &bgObs{}
	h := newHarness(t, func(o *Options) { o.Observer = obs }, "")
	id := h.agent(nil)
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("turn: %+v", res)
	}
	h.e.mu.Lock()
	p := h.e.procs[id]
	h.e.mu.Unlock()
	if p == nil {
		t.Fatal("no live process")
	}
	return h, obs, id, p
}

// A background task belongs to the run that was replying when it started: its end is recorded on that run even when
// another run is replying by then, and two tasks from two runs each keep their own run, whichever ends first.
func TestBackgroundStartEndRecordsOnStartingRun(t *testing.T) {
	h, obs, id, p := newBgHarness(t)
	runA, runB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	p.curRun.Store(&runA)
	h.e.bgStarted(p, executor.BackgroundTask{ID: "a", Kind: executor.BackgroundCommand, Label: "sleep 20; echo finished", ToolCallID: "call-a"})
	p.curRun.Store(&runB)
	h.e.bgStarted(p, executor.BackgroundTask{ID: "b", Kind: executor.BackgroundSubagent, Label: strings.Repeat("é", executor.BackgroundLabelLimit+10)})
	if running := h.e.bgRunning(p); len(running) != 2 || running[0].ID != "a" || running[1].ID != "b" {
		t.Fatalf("running, oldest first: %+v", running)
	}
	code := int64(0)
	out := strings.Repeat("x", executor.BackgroundOutputLimit) + "end\n"
	if !h.e.bgEnded(p, "b", executor.BackgroundEnd{Status: executor.BackgroundCompleted}) {
		t.Fatal("b ends")
	}
	p.curRun.Store(nil)
	if !h.e.bgEnded(p, "a", executor.BackgroundEnd{Status: executor.BackgroundFailed, ExitCode: &code, Output: &out}) {
		t.Fatal("a ends")
	}
	a, b := obs.reports("a"), obs.reports("b")
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("one start and one end each: %+v %+v", a, b)
	}
	for _, r := range a {
		if r.RunID != runA || r.Key != a[0].Key || r.Runtime != p.drv.runtime() {
			t.Fatalf("a belongs to run A: %+v", r)
		}
	}
	for _, r := range b {
		if r.RunID != runB || r.Key != b[0].Key || r.Key == a[0].Key {
			t.Fatalf("b belongs to run B: %+v", r)
		}
	}
	if a[0].Status != executor.BackgroundRunning || a[0].ToolCallID != "call-a" || a[0].StartedAt.IsZero() || a[0].EndedAt != nil {
		t.Fatalf("start: %+v", a[0])
	}
	end := a[1]
	if end.Status != executor.BackgroundFailed || end.EndedAt == nil || end.ExitCode == nil || *end.ExitCode != 0 || end.Output == nil ||
		len(*end.Output) != executor.BackgroundOutputLimit || !strings.HasSuffix(*end.Output, "end\n") {
		t.Fatalf("end with the output's tail: %+v", end)
	}
	if n := len([]rune(b[0].Label)); n != executor.BackgroundLabelLimit {
		t.Fatalf("label clipped to %d characters, got %d", executor.BackgroundLabelLimit, n)
	}
	if running := h.e.bgRunning(p); len(running) != 0 {
		t.Fatalf("nothing runs: %+v", running)
	}
	_ = id
}

// A task ends once: a stop and the task's own end can race; the first end is recorded and the later one is ignored. A
// task the runtime reports twice starts once.
func TestBackgroundEndIsOnce(t *testing.T) {
	h, obs, _, p := newBgHarness(t)
	run := uuid.Must(uuid.NewV7())
	p.curRun.Store(&run)
	h.e.bgStarted(p, executor.BackgroundTask{ID: "a", Kind: executor.BackgroundCommand, Label: "sleep 1"})
	h.e.bgStarted(p, executor.BackgroundTask{ID: "a", Kind: executor.BackgroundCommand, Label: "sleep 1"})
	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i, end := range []executor.BackgroundEnd{{Status: executor.BackgroundStopped, StopReason: executor.StopByPerson}, {Status: executor.BackgroundCompleted}} {
		wg.Go(func() { results[i] = h.e.bgEnded(p, "a", end) })
	}
	wg.Wait()
	if results[0] == results[1] {
		t.Fatalf("exactly one end counts: %v", results)
	}
	reports := obs.reports("a")
	if len(reports) != 2 || reports[0].Status != executor.BackgroundRunning || reports[1].Status == executor.BackgroundRunning {
		t.Fatalf("one start, one end: %+v", reports)
	}
	if h.e.bgEnded(p, "a", executor.BackgroundEnd{Status: executor.BackgroundFailed}) || h.e.bgEnded(p, "nope", executor.BackgroundEnd{Status: executor.BackgroundFailed}) {
		t.Fatal("a task that is not running does not end again")
	}
	if len(obs.reports("a")) != 2 || len(obs.reports("nope")) != 0 {
		t.Fatal("nothing more is reported")
	}
}

// A stop asked for is reported with the task still running, once; the ended tasks wait for the CLI's own reply about
// them, and the platform's next message takes them over (the CLI then tells about them in that reply).
func TestBackgroundStopRequestAndEndedTasks(t *testing.T) {
	h, obs, _, p := newBgHarness(t)
	run := uuid.Must(uuid.NewV7())
	p.curRun.Store(&run)
	h.e.bgStarted(p, executor.BackgroundTask{ID: "a", Kind: executor.BackgroundCommand, Label: "sleep 9"})
	h.e.bgStarted(p, executor.BackgroundTask{ID: "b", Kind: executor.BackgroundCommand, Label: "sleep 9"})
	at := time.Now()
	if !h.e.bgStopRequested(p, "a", at) || h.e.bgStopRequested(p, "a", at.Add(time.Second)) || h.e.bgStopRequested(p, "nope", at) {
		t.Fatal("a stop is asked for once, of a running task")
	}
	reports := obs.reports("a")
	if len(reports) != 2 || reports[1].Status != executor.BackgroundRunning || reports[1].StopRequestedAt == nil || !reports[1].StopRequestedAt.Equal(at) {
		t.Fatalf("stop asked for: %+v", reports)
	}
	h.e.bgEnded(p, "a", executor.BackgroundEnd{Status: executor.BackgroundStopped, StopReason: executor.StopByPerson})
	h.e.bgEnded(p, "b", executor.BackgroundEnd{Status: executor.BackgroundCompleted})
	if got := h.e.bgTakeEnded(p); len(got) != 2 || !slices.ContainsFunc(got, func(t executor.BackgroundTask) bool { return t.ID == "a" && t.StopRequestedAt != nil }) {
		t.Fatalf("ended tasks, as they ended: %+v", got)
	}
	if got := h.e.bgTakeEnded(p); len(got) != 0 {
		t.Fatalf("taken once: %+v", got)
	}
}

// Stopping a task asks the runtime to stop it (a runtime with no means of its own keeps it running, the stop asked for);
// the call waits a little for the end and returns the task as it is then. A task that does not run is not found.
func TestStopBackgroundWaitsForTheEnd(t *testing.T) {
	h, obs, id, p := newBgHarness(t)
	h.e.o.BackgroundStopWait = 300 * time.Millisecond
	run := uuid.Must(uuid.NewV7())
	p.curRun.Store(&run)
	h.e.bgStarted(p, executor.BackgroundTask{ID: "a", Kind: executor.BackgroundCommand, Label: "sleep 9"})
	h.e.bgStarted(p, executor.BackgroundTask{ID: "b", Kind: executor.BackgroundCommand, Label: "sleep 9"})
	if _, err := h.e.StopBackground(context.Background(), id, "nope", executor.StopByPerson); !errors.Is(err, executor.ErrBackgroundNotFound) {
		t.Fatalf("an unknown task: %v", err)
	}
	if _, err := h.e.StopBackground(context.Background(), uuid.New(), "a", executor.StopByPerson); !errors.Is(err, executor.ErrBackgroundNotFound) {
		t.Fatalf("an Agent with no process: %v", err)
	}
	begin := time.Now()
	got, err := h.e.StopBackground(context.Background(), id, "a", executor.StopByPerson)
	if err != nil || got.Status != executor.BackgroundRunning || got.StopRequestedAt == nil || time.Since(begin) < 250*time.Millisecond {
		t.Fatalf("still running after the wait, stop asked for: %+v %v", got, err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.e.bgEnded(p, "b", executor.BackgroundEnd{Status: executor.BackgroundStopped, StopReason: executor.StopByPerson})
	}()
	all, err := h.e.StopAllBackground(context.Background(), id, executor.StopByPerson)
	if err != nil || len(all) != 2 || all[0].ID != "a" || all[0].Status != executor.BackgroundRunning || all[1].ID != "b" || all[1].Status != executor.BackgroundStopped {
		t.Fatalf("stop all returns each as it is: %+v %v", all, err)
	}
	if n := len(obs.reports("a")); n != 2 {
		t.Fatalf("the stop of a is asked for once: %d reports", n)
	}
	if all, err := h.e.StopAllBackground(context.Background(), uuid.New(), executor.StopByPerson); err != nil || len(all) != 0 {
		t.Fatalf("nothing to stop: %+v %v", all, err)
	}
}
