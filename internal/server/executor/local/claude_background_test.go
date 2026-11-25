package local

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// followUpHost records the follow-ups the executor asks for.
type followUpHost struct {
	mu    sync.Mutex
	calls chan followUpCall
}

type followUpCall struct {
	agent, run uuid.UUID
	triggers   []executor.BackgroundTask
}

func newFollowUpHost() *followUpHost { return &followUpHost{calls: make(chan followUpCall, 8)} }

func (f *followUpHost) FollowUp(agentID, runID uuid.UUID, triggers []executor.BackgroundTask) {
	f.calls <- followUpCall{agentID, runID, triggers}
}

func (f *followUpHost) next(t *testing.T) followUpCall {
	t.Helper()
	select {
	case c := <-f.calls:
		return c
	case <-time.After(10 * time.Second):
		t.Fatal("no follow-up asked for")
	}
	return followUpCall{}
}

// newClaudeBg runs Claude Code on fakeclaude with an observer that keeps background tasks and a follow-up host.
func newClaudeBg(t *testing.T) (*claudeHarness, *bgObs, *followUpHost) {
	t.Helper()
	obs, host := &bgObs{}, newFollowUpHost()
	h := newClaudeHarness(t, func(o *Options) { o.Observer, o.BackgroundStopWait = obs, 5*time.Second })
	h.e.SetFollowUps(host)
	return h, obs, host
}

func (h *claudeHarness) execRunWith(id uuid.UUID, msg string) (uuid.UUID, executor.Result, *recSink) {
	h.t.Helper()
	run, sink := uuid.Must(uuid.NewV7()), newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: msg}, sink)
	if err != nil {
		h.t.Fatal(err)
	}
	return run, res, sink
}

// waitReport waits until the task has been reported in a status other than running.
func waitEnded(t *testing.T, obs *bgObs, task string) executor.BackgroundTask {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := obs.reports(task); len(r) > 0 && r[len(r)-1].Status != executor.BackgroundRunning {
			return r[len(r)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not end", task)
	return executor.BackgroundTask{}
}

// A Bash command Claude Code runs in the background is listed on the run that started it, labelled with the command
// and tied to its tool call; the reply ends as usual. Its end is recorded with the end of its output.
func TestClaudeBackgroundTaskListedAndEnded(t *testing.T) {
	h, obs, _ := newClaudeBg(t)
	id := h.claudeAgent(nil)
	run, res, _ := h.execRunWith(id, "fake:background 200ms sleep 20; echo finished")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "started" {
		t.Fatalf("the reply ends as the CLI ends it: %+v", res)
	}
	start := obs.reports("b1")
	if len(start) == 0 || start[0].Status != executor.BackgroundRunning || start[0].RunID != run || start[0].Label != "sleep 20; echo finished" ||
		start[0].Kind != executor.BackgroundCommand || start[0].ToolCallID != "toolu_bg_1" || start[0].Runtime != "claude-code" {
		t.Fatalf("listed: %+v", start)
	}
	end := waitEnded(t, obs, "b1")
	if end.Status != executor.BackgroundCompleted || end.RunID != run || end.Output == nil || !strings.HasSuffix(*end.Output, "finished\n") ||
		len(*end.Output) > executor.BackgroundOutputLimit || end.ExitCode != nil {
		t.Fatalf("ended with its output's tail: %+v", end)
	}
}

// After the task ends Claude Code replies by itself: the executor asks the host for a run of origin background, which
// records the reply (text and usage) without sending anything to the runtime.
func TestClaudeFollowUpBecomesBackgroundRun(t *testing.T) {
	h, obs, host := newClaudeBg(t)
	id := h.claudeAgent(nil)
	run, _, _ := h.execRunWith(id, "fake:background 100ms true")
	call := host.next(t)
	if call.agent != id || len(call.triggers) != 1 || call.triggers[0].ID != "b1" || call.triggers[0].Status != executor.BackgroundCompleted || call.triggers[0].RunID != run {
		t.Fatalf("follow-up of b1: %+v", call)
	}
	sink := newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, sink)
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != "background done" || sink.text.String() != "background done" || sink.running != 1 {
		t.Fatalf("the follow-up run: %+v %v %q", res, err, sink.text.String())
	}
	if sink.selection.Runtime != "claude-code" || sink.selection.ModelID != "sonnet" {
		t.Fatalf("who replied: %+v", sink.selection)
	}
	obs.mu.Lock()
	var follow int
	for _, u := range obs.usage {
		if u.run != nil && *u.run == call.run {
			follow++
		}
	}
	obs.mu.Unlock()
	if follow != 1 {
		t.Fatal("the follow-up's usage is on its run")
	}
	if res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, newSink()); res.Outcome != executor.OutcomeMerged {
		t.Fatalf("a run with no reply left to record: %+v", res)
	}
}

// Stopping a task from the list sends Claude Code the control request stop_task; the task ends as stopped by the
// person.
func TestClaudeStopTaskSendsControlRequest(t *testing.T) {
	h, _, _ := newClaudeBg(t)
	id := h.claudeAgent(nil)
	h.execRunWith(id, "fake:background 30s sleep 60")
	got, err := h.e.StopBackground(context.Background(), id, "b1", executor.StopByPerson)
	if err != nil || got.Status != executor.BackgroundStopped || got.StopReason != executor.StopByPerson || got.StopRequestedAt == nil {
		t.Fatalf("stopped: %+v %v", got, err)
	}
}

// A task that ends while the person's next reply runs is recorded on the run that started it, and no follow-up run is
// asked for: Claude Code tells about it in that reply.
func TestClaudeEndDuringNextTurnCreatesNoRun(t *testing.T) {
	h, obs, host := newClaudeBg(t)
	id := h.claudeAgent(nil)
	first, _, _ := h.execRunWith(id, "fake:background 100ms true")
	if _, res, _ := h.execRunWith(id, "fake:wait 600ms"); res.Text != "waited" {
		t.Fatalf("%+v", res)
	}
	if end := waitEnded(t, obs, "b1"); end.RunID != first || end.Status != executor.BackgroundCompleted {
		t.Fatalf("recorded on the first run: %+v", end)
	}
	select {
	case c := <-host.calls:
		t.Fatalf("no follow-up run: %+v", c)
	case <-time.After(300 * time.Millisecond):
	}
}

// A reply Claude Code began by itself while a person's message was being sent goes first in that message's reply; the
// run made for it then finds nothing to record.
func TestClaudeFollowUpTakenByStartingRun(t *testing.T) {
	h, _, host := newClaudeBg(t)
	id := h.claudeAgent(nil)
	h.execRunWith(id, "fake:background 100ms true")
	call := host.next(t)
	_, res, sink := h.execRunWith(id, "hello")
	if res.Outcome != executor.OutcomeCompleted || res.Text != "background done\n\necho: hello" || sink.text.String() != "background done\n\necho: hello" {
		t.Fatalf("the follow-up goes first: %+v %q", res, sink.text.String())
	}
	if res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, newSink()); res.Outcome != executor.OutcomeMerged {
		t.Fatalf("merged: %+v", res)
	}
}
