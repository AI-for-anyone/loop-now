package store_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

func (e *recEnv) bgTask(run uuid.UUID, native string, started time.Time) store.BackgroundTask {
	e.t.Helper()
	call := "call-" + native
	t := store.BackgroundTask{Key: uuid.Must(uuid.NewV7()), AgentID: e.agent, RunID: &run, NativeID: native, Runtime: "claude-code",
		Kind: "command", Label: "sleep 20", ToolCallID: &call, StartedAt: started, Status: "running"}
	if err := e.s.SaveBackgroundTask(e.ctx, t); err != nil {
		e.t.Fatal(err)
	}
	return t
}

func ended(t store.BackgroundTask, status string, at time.Time) store.BackgroundTask {
	t.Status, t.EndedAt = status, &at
	return t
}

// A task's first recorded end is the one kept: a stop and the task's own end can race, and only one of them is its
// end. A stop asked for while it runs is kept with it.
func TestBackgroundTaskFirstEndWins(t *testing.T) {
	e := newRecEnv(t)
	run := e.turn("go", "completed", "ok")
	now := time.Now().UTC().Truncate(time.Millisecond)
	task := e.bgTask(run.ID, "b1", now)
	asked := task
	asked.StopRequestedAt = &now
	if err := e.s.SaveBackgroundTask(e.ctx, asked); err != nil {
		t.Fatal(err)
	}
	done := ended(task, "completed", now.Add(time.Second))
	code := int64(0)
	out := "finished\n"
	done.ExitCode, done.Output = &code, &out
	if err := e.s.SaveBackgroundTask(e.ctx, done); err != nil {
		t.Fatal(err)
	}
	stopped := ended(task, "stopped", now.Add(2*time.Second))
	reason := "person"
	stopped.StopReason = &reason
	if err := e.s.SaveBackgroundTask(e.ctx, stopped); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.GetRun(e.ctx, run.ID)
	if err != nil || len(got.BackgroundTasks) != 1 {
		t.Fatalf("the run lists the task it started: %+v %v", got.BackgroundTasks, err)
	}
	b := got.BackgroundTasks[0]
	if b.Status != "completed" || b.StopReason != nil || b.ExitCode == nil || *b.ExitCode != 0 || b.Output == nil || *b.Output != out ||
		b.StopRequestedAt == nil || !b.StopRequestedAt.Equal(now) || b.EndedAt == nil || !b.EndedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("the first end is kept: %+v", b)
	}
	if running, err := e.s.RunningBackgroundTasks(e.ctx, e.agent); err != nil || len(running) != 0 {
		t.Fatalf("nothing runs any more: %+v %v", running, err)
	}
}

// The running list is per Agent, oldest first, with the count used by the Agent lists; tasks keep to the run that
// started them, and a follow-up run lists the tasks whose end it followed.
func TestRunningBackgroundTasksAndFollowUps(t *testing.T) {
	e := newRecEnv(t)
	first := e.turn("one", "completed", "ok")
	second := e.turn("two", "completed", "ok")
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := e.bgTask(first.ID, "a", now)
	b := e.bgTask(second.ID, "b", now.Add(time.Second))
	running, err := e.s.RunningBackgroundTasks(e.ctx, e.agent)
	if err != nil || len(running) != 2 || running[0].NativeID != "a" || running[1].NativeID != "b" {
		t.Fatalf("oldest first: %+v %v", running, err)
	}
	if counts, err := e.s.RunningBackgroundCounts(e.ctx, []uuid.UUID{e.agent, uuid.New()}); err != nil || counts[e.agent] != 2 || len(counts) != 1 {
		t.Fatalf("counts: %v %v", counts, err)
	}
	if err := e.s.SaveBackgroundTask(e.ctx, ended(b, "completed", now.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	follow := e.turn("Background task ended", "completed", "it finished")
	if err := e.s.LinkFollowUp(e.ctx, follow.ID, []uuid.UUID{b.Key}); err != nil {
		t.Fatal(err)
	}
	got, err := e.s.GetRun(e.ctx, follow.ID)
	if err != nil || len(got.TriggerBackgroundTasks) != 1 || got.TriggerBackgroundTasks[0].NativeID != "b" || len(got.BackgroundTasks) != 0 {
		t.Fatalf("the follow-up names the task it followed: %+v %v", got, err)
	}
	if got, _ := e.s.GetRun(e.ctx, first.ID); len(got.BackgroundTasks) != 1 || got.BackgroundTasks[0].Key != a.Key || got.TriggerBackgroundTasks != nil {
		t.Fatalf("the first run keeps its own task: %+v", got.BackgroundTasks)
	}
	latest, err := e.s.LatestBackgroundTask(e.ctx, e.agent, "b")
	if err != nil || latest.Key != b.Key || latest.Status != "completed" {
		t.Fatalf("latest by the runtime's id: %+v %v", latest, err)
	}
	if _, err := e.s.LatestBackgroundTask(e.ctx, e.agent, "none"); !store.IsNotFound(err) {
		t.Fatalf("an unknown id is not found: %v", err)
	}
}

// At startup no runtime process of an earlier server is followed any more: tasks still recorded as running become
// unknown, ended at startup, and the list is empty.
func TestStartupMarksRunningUnknown(t *testing.T) {
	e := newRecEnv(t)
	run := e.turn("go", "completed", "ok")
	now := time.Now().UTC().Truncate(time.Millisecond)
	e.bgTask(run.ID, "a", now)
	done := e.bgTask(run.ID, "b", now)
	if err := e.s.SaveBackgroundTask(e.ctx, ended(done, "completed", now)); err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	marked, err := e.s.MarkBackgroundTasksUnknown(e.ctx, at)
	if err != nil || len(marked) != 1 || marked[0].NativeID != "a" || marked[0].Status != "unknown" || !marked[0].EndedAt.Equal(at) {
		t.Fatalf("marked: %+v %v", marked, err)
	}
	if running, _ := e.s.RunningBackgroundTasks(e.ctx, e.agent); len(running) != 0 {
		t.Fatalf("nothing is listed as running: %+v", running)
	}
	got, _ := e.s.GetRun(e.ctx, run.ID)
	if len(got.BackgroundTasks) != 2 || got.BackgroundTasks[1].Status != "completed" {
		t.Fatalf("a task that ended keeps its end: %+v", got.BackgroundTasks)
	}
}
