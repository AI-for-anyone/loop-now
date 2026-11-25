package runrecord

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// A background task shows in the timeline of the run that started it: when it started (with the tool call that started
// it) and, once it ended, how; an end that came after the run ended is still there, at its own time.
func TestBackgroundTaskTimelineEntries(t *testing.T) {
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	end := at.Add(time.Minute)
	run, call, code, out, reason := uuid.New(), "call-1", int64(1), "boom\n", "person"
	tasks := []store.BackgroundTask{
		{Key: uuid.New(), RunID: &run, NativeID: "b1", Kind: "command", Label: "make test", ToolCallID: &call, StartedAt: at, Status: "failed", EndedAt: &end, ExitCode: &code, Output: &out},
		{Key: uuid.New(), RunID: &run, NativeID: "b2", Kind: "subagent", Label: "review", StartedAt: at, Status: "running"},
		{Key: uuid.New(), RunID: &run, NativeID: "b3", Kind: "command", Label: "sleep 9", StartedAt: at, Status: "stopped", EndedAt: &end, StopReason: &reason},
	}
	entries := backgroundEntries(tasks)
	if len(entries) != 5 {
		t.Fatalf("three starts and two ends: %+v", entries)
	}
	started := entries[0].value.(apitypes.TimelineBackgroundTaskStartedEntry)
	if started.Type != "background_task_started" || !started.At.Equal(at) || started.TaskID != "b1" || started.Kind != "command" ||
		started.Label != "make test" || started.ToolCallID == nil || *started.ToolCallID != "call-1" {
		t.Fatalf("start: %+v", started)
	}
	ended := entries[1].value.(apitypes.TimelineBackgroundTaskEndedEntry)
	if ended.Type != "background_task_ended" || !ended.At.Equal(end) || ended.TaskID != "b1" || ended.Status != "failed" ||
		ended.ExitCode == nil || *ended.ExitCode != 1 || ended.Output == nil || *ended.Output != out || ended.StopReason != nil {
		t.Fatalf("end: %+v", ended)
	}
	if entries[0].id == entries[1].id || entries[1].id != backgroundEntries(tasks)[1].id {
		t.Fatal("each entry has its own stable id")
	}
	if stopped := entries[4].value.(apitypes.TimelineBackgroundTaskEndedEntry); stopped.StopReason == nil || *stopped.StopReason != "person" {
		t.Fatalf("stopped: %+v", stopped)
	}
}

// The recorder keeps what the executor reports about a background task on the run that started it, then says so.
func TestRecorderKeepsBackgroundTasks(t *testing.T) {
	st := sqlitetest.NewStore(t)
	ctx := context.Background()
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	_, run, err := st.InsertMessageAndRun(ctx, store.NewRun{AgentID: a.ID, RunID: uuid.Must(uuid.NewV7()), Text: "go", Start: true, InstanceID: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	type change struct{ agent, run uuid.UUID }
	var changes []change
	r := &Recorder{Store: st, Log: slog.New(slog.DiscardHandler), BackgroundChanged: func(agent, run uuid.UUID) { changes = append(changes, change{agent, run}) }}
	task := executor.BackgroundTask{Key: uuid.Must(uuid.NewV7()), ID: "b1", Runtime: "claude-code", Kind: "command", Label: "sleep 20", RunID: run.ID,
		ToolCallID: "call-1", StartedAt: time.Now().UTC(), Status: "running"}
	r.BackgroundTask(a.ID, task)
	now := time.Now().UTC()
	code := int64(0)
	task.Status, task.EndedAt, task.ExitCode = "completed", &now, &code
	r.BackgroundTask(a.ID, task)
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || len(got.BackgroundTasks) != 1 || got.BackgroundTasks[0].Status != "completed" || *got.BackgroundTasks[0].ToolCallID != "call-1" {
		t.Fatalf("kept on the run: %+v %v", got.BackgroundTasks, err)
	}
	if len(changes) != 2 || changes[0] != (change{a.ID, run.ID}) {
		t.Fatalf("each change is announced: %+v", changes)
	}
}
