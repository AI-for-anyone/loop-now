package runrecord

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

var _ executor.BackgroundObserver = (*Recorder)(nil)

// BackgroundTask implements executor.BackgroundObserver: it keeps the task's state on the run that started it, then
// calls BackgroundChanged.
func (r *Recorder) BackgroundTask(agentID uuid.UUID, t executor.BackgroundTask) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	stored := StoredBackgroundTask(agentID, t)
	run := t.RunID
	stored.RunID = &run
	if err := r.Store.SaveBackgroundTask(ctx, stored); err != nil {
		r.Log.Warn("failed to keep the background task", "agentId", agentID, "runId", t.RunID, "taskId", t.ID, "err", err)
		return
	}
	if r.BackgroundChanged != nil {
		r.BackgroundChanged(agentID, t.RunID)
	}
}

// StoredBackgroundTask is the record of a task the executor reported for agentID (the executor's Agent); the caller says
// whether t.RunID is a run or a task turn.
func StoredBackgroundTask(agentID uuid.UUID, t executor.BackgroundTask) store.BackgroundTask {
	out := store.BackgroundTask{Key: t.Key, AgentID: agentID, NativeID: t.ID, Runtime: t.Runtime, Kind: t.Kind, Label: t.Label, StartedAt: t.StartedAt,
		Status: t.Status, EndedAt: t.EndedAt, ExitCode: t.ExitCode, StopRequestedAt: t.StopRequestedAt, Output: t.Output}
	if t.ToolCallID != "" {
		call := t.ToolCallID
		out.ToolCallID = &call
	}
	if t.StopReason != "" {
		reason := t.StopReason
		out.StopReason = &reason
	}
	return out
}

// backgroundEntries are the timeline entries of the background tasks a run started: each one's start and, once it
// ended, its end, at their own times. Each entry has a stable id (for paging task records).
func backgroundEntries(tasks []store.BackgroundTask) []taskNodeTimelineItem {
	var out []taskNodeTimelineItem
	for _, t := range tasks {
		out = append(out, taskNodeTimelineItem{id: t.Key, at: t.StartedAt, value: apitypes.TimelineBackgroundTaskStartedEntry{
			TimelineBase: base(apitypes.TimelineBackgroundStarted, t.StartedAt), TaskID: t.NativeID, Kind: t.Kind, Label: t.Label, ToolCallID: t.ToolCallID}})
		if t.EndedAt != nil {
			out = append(out, taskNodeTimelineItem{id: uuid.NewSHA1(t.Key, []byte("ended")), at: *t.EndedAt, value: apitypes.TimelineBackgroundTaskEndedEntry{
				TimelineBase: base(apitypes.TimelineBackgroundEnded, *t.EndedAt), TaskID: t.NativeID, Status: t.Status, ExitCode: t.ExitCode,
				StopReason: t.StopReason, Output: t.Output}})
		}
	}
	return out
}
