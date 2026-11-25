package conversation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// bgTask records a background task the run started, the way the run recorder keeps the executor's reports.
func (e *env) bgTask(run uuid.UUID, native, status string) store.BackgroundTask {
	e.t.Helper()
	t := store.BackgroundTask{Key: uuid.Must(uuid.NewV7()), AgentID: e.agent, RunID: &run, NativeID: native, Runtime: "claude-code",
		Kind: "command", Label: "sleep 20; echo " + native, StartedAt: time.Now().UTC(), Status: "running"}
	if status != "running" {
		at := time.Now().UTC()
		code := int64(0)
		t.Status, t.EndedAt, t.ExitCode = status, &at, &code
	}
	if err := e.st.SaveBackgroundTask(context.Background(), t); err != nil {
		e.t.Fatal(err)
	}
	return t
}

func execTask(t store.BackgroundTask) executor.BackgroundTask {
	out := executor.BackgroundTask{Key: t.Key, ID: t.NativeID, Runtime: t.Runtime, Kind: t.Kind, Label: t.Label, RunID: *t.RunID, StartedAt: t.StartedAt,
		Status: t.Status, EndedAt: t.EndedAt, ExitCode: t.ExitCode}
	return out
}

// When the CLI replies by itself after background work ended, that reply is a run of origin background: its user
// message says which task ended, it names the tasks, and while it runs the Agent is busy, so a message sent then queues
// as during any reply.
func TestBackgroundRunMakesAgentBusy(t *testing.T) {
	e := newEnv(t)
	e.mustSend("start it", "")
	e.waitStarted("start it")
	e.waitIdle()
	first := e.history()[0].Run.ID
	task := e.bgTask(first, "b1", "completed")

	e.svc.FollowUp(e.agent, []executor.BackgroundTask{execTask(task)})
	e.waitStarted("follow-up")
	st, err := e.svc.Status(context.Background(), e.agent)
	if err != nil || !st.Busy || st.CurrentRun == nil {
		t.Fatalf("the Agent is busy while the CLI replies by itself: %+v %v", st, err)
	}
	if _, err := e.send("are you there?", ""); !apperr.Is(err, apperr.CodeAgentBusy) {
		t.Fatalf("a message without a choice is refused as busy: %v", err)
	}
	queued := e.mustSend("next", "queue")
	if queued.Run.Status != "queued" {
		t.Fatalf("a message sent then queues: %+v", queued.Run)
	}
	e.ex.signal(e.agent, "release")
	e.waitStarted("next")
	e.waitIdle()
	h := e.history()
	if len(h) != 6 {
		t.Fatalf("history: %+v", h)
	}
	user := h[2]
	var reply apitypes.Message
	for _, m := range h {
		if m.Role == "assistant" && m.Run.ID == user.Run.ID {
			reply = m
		}
	}
	if user.Origin != executor.OriginBackground || reply.Origin != executor.OriginBackground || reply.Text != "It finished." || reply.Run.Status != "completed" {
		t.Fatalf("the follow-up is a run of its own: %+v / %+v", user, reply)
	}
	if !strings.Contains(user.Text, "sleep 20; echo b1") || !strings.Contains(user.Text, "completed") {
		t.Fatalf("its message says which task ended and how: %q", user.Text)
	}
	if got := reply.Run.TriggerBackgroundTasks; len(got) != 1 || got[0].ID != "b1" || got[0].Status != "completed" {
		t.Fatalf("the run names the task it followed: %+v", reply.Run.TriggerBackgroundTasks)
	}
	if got := h[1].Run.BackgroundTasks; len(got) != 1 || got[0].ID != "b1" || got[0].RunID != first {
		t.Fatalf("the first run lists the task it started: %+v", got)
	}
}

// A follow-up the CLI begins while a reply runs goes first in the queue, ahead of messages queued before it, and is not
// one of the person's queued messages.
func TestBackgroundFollowUpQueuesFirst(t *testing.T) {
	e := newEnv(t)
	run := e.mustSend("block current", "")
	e.waitStarted("block current")
	e.mustSend("queued before", "queue")
	task := e.bgTask(run.Run.ID, "b1", "completed")
	e.svc.FollowUp(e.agent, []executor.BackgroundTask{execTask(task)})
	q, err := e.svc.Queue(context.Background(), e.agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range q.Items {
		if strings.Contains(it.Text, "b1") {
			t.Fatalf("the follow-up is not a queued message: %+v", q.Items)
		}
	}
	e.ex.signal(e.agent, "release")
	e.waitStarted("follow-up")
	e.ex.signal(e.agent, "release")
	e.waitStarted("queued before")
	e.waitIdle()
}

// A follow-up whose reply another run took (it was starting when the CLI began it) ends as merged_into_reply, having
// done nothing.
func TestBackgroundFollowUpMerged(t *testing.T) {
	e := newEnv(t)
	e.ex.followUpMerged = true
	e.svc.FollowUp(e.agent, nil)
	e.waitStarted("follow-up merged")
	e.waitIdle()
	h := e.history()
	if len(h) != 2 || h[1].Run.Status != "interrupted" || h[1].Run.EndReason == nil || *h[1].Run.EndReason != "merged_into_reply" {
		t.Fatalf("merged: %+v", h)
	}
}

// When a background task starts, ends or is asked to stop, the Agent's stream gets the full running list (only tasks
// still running) and the run that started it is updated; the status lists the running tasks too.
func TestBackgroundSnapshotEvent(t *testing.T) {
	e := newEnv(t)
	e.mustSend("start them", "")
	e.waitStarted("start them")
	e.waitIdle()
	run := e.history()[0].Run.ID
	e.bgTask(run, "a", "running")
	e.bgTask(run, "b", "completed")
	e.svc.BackgroundChanged(e.agent, run)
	var tasks *apitypes.SseBackgroundTasksUpdated
	var updated *apitypes.Run
	for _, ev := range e.newestEvents() {
		switch {
		case ev.Type == apitypes.EventBackgroundTasksUpdated && tasks == nil:
			tasks = &apitypes.SseBackgroundTasksUpdated{}
			if err := json.Unmarshal(ev.Data, tasks); err != nil {
				t.Fatal(err)
			}
		case ev.Type == apitypes.EventRunUpdated && updated == nil:
			var u apitypes.SseRunUpdated
			if err := json.Unmarshal(ev.Data, &u); err != nil {
				t.Fatal(err)
			}
			updated = &u.Run
		}
	}
	if tasks == nil || len(tasks.Tasks) != 1 || tasks.Tasks[0].ID != "a" || tasks.Tasks[0].Status != "running" {
		t.Fatalf("the running list: %+v", tasks)
	}
	if updated == nil || updated.ID != run || updated.Status != "completed" || len(updated.BackgroundTasks) != 2 {
		t.Fatalf("the run that started them is updated: %+v", updated)
	}
	st, err := e.svc.Status(context.Background(), e.agent)
	if err != nil || len(st.BackgroundTasks) != 1 || st.BackgroundTasks[0].ID != "a" || st.Busy {
		t.Fatalf("an idle Agent lists its running tasks: %+v %v", st, err)
	}
}

// A person stops a background task from the list: the executor stops it and the answer is the task as the record keeps
// it; the same actionId gives the same answer. A task that has ended is a conflict carrying how it ended; one the Agent
// never had is not found. Stop all answers each task that was running.
func TestStopBackgroundTask(t *testing.T) {
	e := newEnv(t)
	e.mustSend("start them", "")
	e.waitStarted("start them")
	e.waitIdle()
	run := e.history()[0].Run.ID
	a := e.bgTask(run, "a", "running")
	e.bgTask(run, "b", "completed")
	c := e.bgTask(run, "c", "running")
	byID := map[string]store.BackgroundTask{"a": a, "c": c}
	var reasons []string
	e.ex.stopBackground = func(agent uuid.UUID, id, reason string) (executor.BackgroundTask, error) {
		task, ok := byID[id]
		if !ok || agent != e.agent {
			return executor.BackgroundTask{}, executor.ErrBackgroundNotFound
		}
		reasons = append(reasons, reason)
		now := time.Now().UTC()
		stopped := task
		stopped.Status, stopped.EndedAt, stopped.StopReason, stopped.StopRequestedAt = "stopped", &now, &reason, &now
		if err := e.st.SaveBackgroundTask(context.Background(), stopped); err != nil {
			t.Fatal(err)
		}
		delete(byID, id)
		return executor.BackgroundTask{Key: task.Key, ID: id, Status: "stopped"}, nil
	}
	action := uuid.New()
	out, err := e.svc.StopBackgroundTask(context.Background(), e.agent, "a", action)
	if err != nil {
		t.Fatal(err)
	}
	var got apitypes.BackgroundTask
	if err := json.Unmarshal(out, &got); err != nil || got.ID != "a" || got.Status != "stopped" || got.StopReason == nil || *got.StopReason != "person" || got.RunID != run {
		t.Fatalf("the stopped task: %s %v", out, err)
	}
	again, err := e.svc.StopBackgroundTask(context.Background(), e.agent, "a", action)
	if err != nil || string(again) != string(out) {
		t.Fatalf("the same actionId answers the same: %s %v", again, err)
	}
	_, err = e.svc.StopBackgroundTask(context.Background(), e.agent, "b", uuid.New())
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeBackgroundTaskEnded || ae.Status != 409 {
		t.Fatalf("an ended task is a conflict: %v", err)
	}
	if d, ok := ae.Details.(apitypes.BackgroundTaskEndedDetails); !ok || d.Task.ID != "b" || d.Task.Status != "completed" {
		t.Fatalf("with how it ended: %+v", ae.Details)
	}
	if _, err := e.svc.StopBackgroundTask(context.Background(), e.agent, "zzz", uuid.New()); !apperr.Is(err, apperr.CodeBackgroundTaskNotFound) {
		t.Fatalf("an unknown task is not found: %v", err)
	}
	all, err := e.svc.StopAllBackgroundTasks(context.Background(), e.agent, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	var list apitypes.BackgroundTaskList
	if err := json.Unmarshal(all, &list); err != nil || len(list.Tasks) != 1 || list.Tasks[0].ID != "c" || list.Tasks[0].Status != "stopped" {
		t.Fatalf("stop all: %s %v", all, err)
	}
	if len(reasons) != 2 || reasons[0] != executor.StopByPerson {
		t.Fatalf("stopped by the person: %v", reasons)
	}
	if _, err := e.svc.StopBackgroundTask(context.Background(), uuid.New(), "a", uuid.New()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("an unknown Agent: %v", err)
	}
}
