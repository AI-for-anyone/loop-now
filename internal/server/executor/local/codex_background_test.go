package local

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

func newCodexBg(t *testing.T) (*codexHarness, *bgObs, *followUpHost) {
	t.Helper()
	obs, host := &bgObs{}, newFollowUpHost()
	h := newCodexHarness(t, func(o *Options) { o.Observer, o.BackgroundStopWait = obs, 5*time.Second })
	h.e.SetFollowUps(host)
	return h, obs, host
}

// A Codex command still running when its turn completed is listed on that run (labelled with the command, tied to its
// item); its later end closes the tool on that run with the exit code and the output's tail. Codex replies with no
// turn of its own, so no follow-up run is asked for.
func TestCodexCommandOutlivesTurn(t *testing.T) {
	h, obs, host := newCodexBg(t)
	id := h.codexAgent(nil)
	run, sink := uuid.Must(uuid.NewV7()), newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: run, Message: "fake:background 200ms sleep 20; echo finished"}, sink)
	if err != nil || res.Outcome != executor.OutcomeCompleted || res.Text != "started" {
		t.Fatalf("the reply ends with the turn: %+v %v", res, err)
	}
	start := obs.reports(lastItem(t, obs))
	if len(start) == 0 || start[0].RunID != run || start[0].Label != "sleep 20; echo finished" || start[0].Kind != executor.BackgroundCommand || start[0].ToolCallID != start[0].ID {
		t.Fatalf("listed: %+v", start)
	}
	end := waitEnded(t, obs, start[0].ID)
	if end.Status != executor.BackgroundCompleted || end.ExitCode == nil || *end.ExitCode != 0 || end.Output == nil || !strings.HasSuffix(*end.Output, "finished\n") || len(*end.Output) > executor.BackgroundOutputLimit {
		t.Fatalf("ended: %+v", end)
	}
	sink.mu.Lock()
	tools := strings.Join(sink.tools, ",")
	sink.mu.Unlock()
	if tools != "shell:started,shell:succeeded" {
		t.Fatalf("the tool ends on the run that started it: %s", tools)
	}
	select {
	case c := <-host.calls:
		t.Fatalf("Codex gives no follow-up: %+v", c)
	case <-time.After(200 * time.Millisecond):
	}
}

// Stopping a Codex command terminates it through the app-server; it ends as stopped by the person.
func TestCodexStopBackground(t *testing.T) {
	h, obs, _ := newCodexBg(t)
	id := h.codexAgent(nil)
	if res, _ := h.exec(id, "fake:background 30s sleep 60"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("%+v", res)
	}
	task := lastItem(t, obs)
	got, err := h.e.StopBackground(context.Background(), id, task, executor.StopByPerson)
	if err != nil || got.Status != executor.BackgroundStopped || got.StopReason != executor.StopByPerson || got.ExitCode != nil {
		t.Fatalf("stopped, with no exit code (Codex's -1 says nothing): %+v %v", got, err)
	}
}

// lastItem is the id of the last task reported.
func lastItem(t *testing.T, obs *bgObs) string {
	t.Helper()
	obs.bgMu.Lock()
	defer obs.bgMu.Unlock()
	if len(obs.tasks) == 0 {
		t.Fatal("no background task reported")
	}
	return obs.tasks[len(obs.tasks)-1].t.ID
}
