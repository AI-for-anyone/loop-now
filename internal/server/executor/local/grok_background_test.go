package local

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

func newGrokBg(t *testing.T) (*acpHarness, *bgObs, *followUpHost) {
	t.Helper()
	obs, host := &bgObs{}, newFollowUpHost()
	h := newACPHarness(t, "grok", func(o *Options) { o.Observer, o.BackgroundStopWait = obs, 5*time.Second })
	h.e.SetFollowUps(host)
	return h, obs, host
}

// A Grok command moved to the background is listed on the run that started it, with its command and tool call; its
// end has the exit code and the output's tail; the prompt Grok then runs by itself becomes a follow-up run that
// records its reply.
func TestGrokBackgroundAndFollowUp(t *testing.T) {
	h, obs, host := newGrokBg(t)
	id := h.acpAgent(nil)
	run, sink := uuid.Must(uuid.NewV7()), newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:bgtask 200ms sleep 20; echo finished"}, sink)
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != "started" {
		t.Fatalf("the reply ends with the prompt: %+v %v", res, err)
	}
	task := lastItem(t, obs)
	start := obs.reports(task)[0]
	if start.RunID != run || start.Label != "sleep 20; echo finished" || start.Kind != executor.BackgroundCommand || start.ToolCallID == "" || start.Runtime != "grok" {
		t.Fatalf("listed: %+v", start)
	}
	end := waitEnded(t, obs, task)
	if end.Status != executor.BackgroundCompleted || end.ExitCode == nil || *end.ExitCode != 0 || end.Output == nil || !strings.HasSuffix(*end.Output, "finished\n") || len(*end.Output) > executor.BackgroundOutputLimit {
		t.Fatalf("ended: %+v", end)
	}
	call := host.next(t)
	if len(call.triggers) != 1 || call.triggers[0].ID != task || call.triggers[0].Status != executor.BackgroundCompleted || call.triggers[0].EndedAt == nil {
		t.Fatalf("the follow-up names the task it follows, as it ended: %+v", call)
	}
	follow := newSink()
	res, err = h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, follow)
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != "background done" || follow.text.String() != "background done" {
		t.Fatalf("the follow-up run: %+v %v %q", res, err, follow.text.String())
	}
}

// The follow-up's usage is the one its turn_completed reports, recorded on the follow-up run (Grok's prompts of the
// client keep counting from their result only).
func TestGrokFollowUpUsageCounted(t *testing.T) {
	h, obs, host := newGrokBg(t)
	id := h.acpAgent(nil)
	run := uuid.Must(uuid.NewV7())
	if _, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:bgtask 100ms true"}, newSink()); err != nil {
		t.Fatal(err)
	}
	call := host.next(t)
	if _, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: call.run, Origin: executor.OriginBackground}, newSink()); err != nil {
		t.Fatal(err)
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	var first, follow []executor.Usage
	for _, u := range obs.usage {
		switch {
		case u.run != nil && *u.run == run:
			first = append(first, u.u)
		case u.run != nil && *u.run == call.run:
			follow = append(follow, u.u)
		}
	}
	if len(first) != 1 || len(follow) != 1 || follow[0].Input != 700-500 || follow[0].CacheRead != 500 || follow[0].Output != 9 || follow[0].ResponseModel != "fake-1-build" {
		t.Fatalf("usage: first %+v, follow-up %+v", first, follow)
	}
}

// Stopping a Grok task kills it with _x.ai/task/kill, without ending the process; it ends as stopped by the person.
func TestGrokStopBackground(t *testing.T) {
	h, obs, _ := newGrokBg(t)
	id := h.acpAgent(nil)
	h.exec(id, "fake:bgtask 30s sleep 60")
	pid := h.pid(id)
	got, err := h.e.StopBackground(context.Background(), id, lastItem(t, obs), executor.StopByPerson)
	if err != nil || got.Status != executor.BackgroundStopped || got.StopReason != executor.StopByPerson || h.pid(id) != pid {
		t.Fatalf("stopped, the process goes on: %+v %v", got, err)
	}
}
