package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/db"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

var _ executor.FollowUpHost = (*Service)(nil)

// FollowUp implements executor.FollowUpHost: the runtime's CLI began a reply by itself after background tasks ended.
// The reply becomes a run of origin background whose user message says which tasks ended; it starts at once when the
// Agent is idle, and otherwise goes first in the queue (the executor keeps the reply until the run is dispatched).
func (s *Service) FollowUp(agentID uuid.UUID, triggers []executor.BackgroundTask) {
	ctx := s.ctx
	if ctx.Err() != nil {
		return
	}
	keys := make([]uuid.UUID, 0, len(triggers))
	for _, t := range triggers {
		keys = append(keys, t.Key)
	}
	text := FollowUpText(triggers)
	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	in := store.NewRun{AgentID: agentID, RunID: runID, Text: text, Origin: executor.OriginBackground, InstanceID: s.o.InstanceID, RequestID: reqid.From(ctx)}
	acquired, err := s.co.Acquire(ctx, agentID.String(), owner)
	if err != nil {
		s.log.Error("failed to take the busy lock for the runtime's own reply", "agentId", agentID, "err", err)
		return
	}
	if acquired {
		in.Start = true
		msg, run, err := s.st.InsertMessageAndRun(ctx, in)
		switch {
		case err == nil:
			s.linkFollowUp(ctx, agentID, run.ID, keys)
			s.emitMessage(ctx, agentID, msg, run)
			s.emitRun(ctx, agentID, run.ID)
			s.startDriver(agentID, run, owner)
			return
		case errors.Is(err, store.ErrActiveRunExists), errors.Is(err, store.ErrQueuedRunsExist):
			_, _ = s.co.Release(ctx, agentID.String(), owner)
			s.restoreLock(ctx, agentID)
		default:
			_, _ = s.co.Release(ctx, agentID.String(), owner)
			s.log.Error("failed to record the runtime's own reply", "agentId", agentID, "err", err)
			return
		}
		in.Start = false
	}
	msg, run, err := s.st.InsertMessageAndRun(ctx, in)
	if err != nil {
		s.log.Error("failed to queue the runtime's own reply", "agentId", agentID, "err", err)
		return
	}
	s.linkFollowUp(ctx, agentID, run.ID, keys)
	s.emitMessage(ctx, agentID, msg, run)
	s.emitRun(ctx, agentID, run.ID)
	s.emitQueue(ctx, agentID)
	s.kick(ctx, agentID) // the run in progress may have ended meanwhile
}

func (s *Service) linkFollowUp(ctx context.Context, agentID, runID uuid.UUID, keys []uuid.UUID) {
	if err := s.st.LinkFollowUp(ctx, runID, keys); err != nil {
		s.log.Warn("failed to link the runtime's own reply to the background tasks it followed", "agentId", agentID, "runId", runID, "err", err)
	}
}

// FollowUpText is the user message of a run of origin background: which background tasks ended, and how.
func FollowUpText(triggers []executor.BackgroundTask) string {
	if len(triggers) == 0 {
		return "Platform notice: background work ended, and the runtime replied by itself."
	}
	parts := make([]string, 0, len(triggers))
	for _, t := range triggers {
		how := t.Status
		if t.ExitCode != nil {
			how += fmt.Sprintf(", exit code %d", *t.ExitCode)
		}
		if t.StopReason != "" {
			how += ", " + strings.ReplaceAll(t.StopReason, "_", " ")
		}
		parts = append(parts, fmt.Sprintf("%q (%s)", t.Label, how))
	}
	noun := "Background task"
	if len(parts) > 1 {
		noun = "Background tasks"
	}
	return "Platform notice: " + noun + " " + strings.Join(parts, ", ") + " ended, and the runtime replied by itself."
}

// BackgroundChanged is called after a background task of the Agent started, ended or was asked to stop: the Agent's
// stream gets the running list and the run that started the task (runID) its new state, and the overview the Agent's
// status.
func (s *Service) BackgroundChanged(agentID, runID uuid.UUID) {
	ctx := s.ctx
	if ctx.Err() != nil {
		return
	}
	running, err := s.st.RunningBackgroundTasks(ctx, agentID)
	if err != nil {
		s.log.Warn("failed to read the running background tasks", "agentId", agentID, "err", err)
		return
	}
	s.emit(ctx, agentID, apitypes.EventBackgroundTasksUpdated, apitypes.SseBackgroundTasksUpdated{Tasks: apitypes.FromBackgroundTasks(running)})
	s.emitRun(ctx, agentID, runID)
}

// executeFollowUp records the reply the runtime's CLI gives (or gave) by itself as the run of origin background: nothing
// is sent to the runtime. An interrupt stops that reply as it would any other.
func (s *Service) executeFollowUp(ctx context.Context, agentID uuid.UUID, run store.Run) executor.Result {
	s.recordDispatch(ctx, agentID, run.ID)
	pollCtx, stopPoll := context.WithCancel(ctx)
	defer stopPoll()
	go s.pollAbort(pollCtx, agentID, run.ID)
	res, err := s.ex.Execute(ctx, executor.Request{AgentID: agentID, RunID: run.ID, Origin: run.Origin, MessageID: run.UserMessageID},
		&runSink{s: s, agentID: agentID, runID: run.ID})
	if err != nil {
		if ctx.Err() != nil {
			return res
		}
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: err.Error()}
	}
	return res
}

// errFresh tells a replay lookup that the action has not been done yet.
var errFresh = errors.New("the action has not been done yet")

// StopBackgroundTask stops one of the Agent's running background tasks for a person (stop reason person) and answers the
// task as it is after the stop was asked for. actionId rules are those of stopping a reply: the same actionId answers
// the same. A task that ended is a conflict carrying it; one that is not the Agent's is not found.
func (s *Service) StopBackgroundTask(ctx context.Context, agentID uuid.UUID, id string, actionID uuid.UUID) (json.RawMessage, error) {
	return s.backgroundAction(ctx, agentID, actionID, apitypes.BackgroundStopOne, id, func(stopper executor.BackgroundStopper) (any, error) {
		t, err := stopper.StopBackground(ctx, agentID, id, executor.StopByPerson)
		if errors.Is(err, executor.ErrBackgroundNotFound) {
			last, lerr := s.st.LatestBackgroundTask(ctx, agentID, id)
			switch {
			case store.IsNotFound(lerr):
				return nil, apperr.NotFoundCode(apperr.CodeBackgroundTaskNotFound, "This Agent has no background task "+id+".")
			case lerr != nil:
				return nil, lerr
			case last.Status == executor.BackgroundRunning:
				// Kept as running but not on the runtime's process any more (it is gone): nothing to stop.
				return nil, apperr.NotFoundCode(apperr.CodeBackgroundTaskNotFound, "Background task "+id+" no longer runs on this Agent's runtime.")
			}
			e := apperr.Conflict(apperr.CodeBackgroundTaskEnded, "Background task "+id+" has already ended.")
			e.Details = apitypes.BackgroundTaskEndedDetails{Task: apitypes.FromBackgroundTask(last)}
			return nil, e
		}
		if err != nil {
			return nil, err
		}
		return s.storedTask(ctx, t)
	})
}

// StopAllBackgroundTasks stops every background task running on the Agent's runtime process for a person, and answers
// each as it is after the stop was asked for.
func (s *Service) StopAllBackgroundTasks(ctx context.Context, agentID, actionID uuid.UUID) (json.RawMessage, error) {
	return s.backgroundAction(ctx, agentID, actionID, apitypes.BackgroundStopAll, "", func(stopper executor.BackgroundStopper) (any, error) {
		tasks, err := stopper.StopAllBackground(ctx, agentID, executor.StopByPerson)
		if err != nil {
			return nil, err
		}
		out := apitypes.BackgroundTaskList{Tasks: []apitypes.BackgroundTask{}}
		for _, t := range tasks {
			v, err := s.storedTask(ctx, t)
			if err != nil {
				return nil, err
			}
			out.Tasks = append(out.Tasks, v)
		}
		return out, nil
	})
}

// storedTask is the task as its record keeps it (the executor reports every change before it returns).
func (s *Service) storedTask(ctx context.Context, t executor.BackgroundTask) (apitypes.BackgroundTask, error) {
	stored, err := s.st.BackgroundTaskByKey(ctx, t.Key)
	if err != nil {
		return apitypes.BackgroundTask{}, err
	}
	return apitypes.FromBackgroundTask(stored), nil
}

// backgroundAction does a stop of background tasks once per actionId: a replay answers what the first call answered.
func (s *Service) backgroundAction(ctx context.Context, agentID, actionID uuid.UUID, op, target string, do func(executor.BackgroundStopper) (any, error)) (json.RawMessage, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"target": target})
	action := store.AgentAction{ActorKey: "console", ID: actionID, Operation: op, Body: body}
	if out, _, err := s.st.AgentQueueAction(ctx, agentID, action, func(*db.Tx) (any, error) { return nil, errFresh }); err == nil {
		return out, nil
	} else if !errors.Is(err, errFresh) {
		return nil, err
	}
	stopper, ok := s.ex.(executor.BackgroundStopper)
	if !ok {
		return nil, apperr.NotFoundCode(apperr.CodeBackgroundTaskNotFound, "This Agent's runtime runs no background tasks.")
	}
	value, err := do(stopper)
	if err != nil {
		return nil, err
	}
	out, _, err := s.st.AgentQueueAction(ctx, agentID, action, func(*db.Tx) (any, error) { return value, nil })
	return out, err
}
