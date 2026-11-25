package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// EventNodeBackgroundTasks replaces a node's running background tasks on the task's event stream.
const EventNodeBackgroundTasks = "task.node.background_tasks.updated"

var _ executor.BackgroundObserver = (*Observer)(nil)

// BackgroundTask implements executor.BackgroundObserver: a task of a task instance is kept on the task turn that
// started it, and the node's running list goes to the task's event stream; other tasks take the regular path.
func (o *Observer) BackgroundTask(runtime uuid.UUID, t executor.BackgroundTask) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	_, err := o.Store.TaskInstance(ctx, runtime)
	if store.IsNotFound(err) {
		if b, ok := o.Normal.(executor.BackgroundObserver); ok {
			b.BackgroundTask(runtime, t)
		}
		return
	}
	if err != nil {
		o.failed(runtime, &t.RunID, err)
		return
	}
	turn, err := o.Store.TaskTurn(ctx, t.RunID)
	if err == nil && turn.RuntimeID != runtime {
		err = errors.New("the task turn does not match the runtime instance")
	}
	if err != nil {
		o.failed(runtime, &t.RunID, err)
		return
	}
	stored := runrecord.StoredBackgroundTask(runtime, t)
	stored.TaskTurnID = &turn.ID
	if err := o.Store.SaveBackgroundTask(ctx, stored); err != nil {
		o.failed(runtime, &t.RunID, err)
		return
	}
	if err := EmitNodeBackgroundTasks(ctx, o.Store, turn); err != nil {
		o.failed(runtime, &t.RunID, err)
	}
}

// NodeBackgroundTasks is the running list of a node: the tasks its turns started that still run.
func NodeBackgroundTasks(ctx context.Context, st *store.Store, taskID, nodeRunID, runtimeID uuid.UUID) (apitypes.TaskNodeBackgroundTasks, error) {
	running, err := st.RunningNodeBackgroundTasks(ctx, nodeRunID)
	if err != nil {
		return apitypes.TaskNodeBackgroundTasks{}, err
	}
	return apitypes.TaskNodeBackgroundTasks{TaskID: taskID, NodeRunID: nodeRunID, RuntimeID: runtimeID, Tasks: apitypes.FromBackgroundTasks(running)}, nil
}

// EmitNodeBackgroundTasks puts the running list of the turn's node on the task's event stream.
func EmitNodeBackgroundTasks(ctx context.Context, st *store.Store, turn store.TaskTurn) error {
	if turn.NodeRunID == uuid.Nil {
		return nil
	}
	list, err := NodeBackgroundTasks(ctx, st, turn.TaskID, turn.NodeRunID, turn.RuntimeID)
	if err != nil {
		return err
	}
	return st.AppendTaskEvent(ctx, turn.TaskID, EventNodeBackgroundTasks, list)
}

// ReconcileBackgroundTasks runs at server startup, before any runtime process starts: the tasks still recorded as
// running belong to processes of an earlier server and are no longer followed, so they become unknown (ended at at),
// and each node that had some gets its emptied list on the task's event stream (which outlives restarts).
func ReconcileBackgroundTasks(ctx context.Context, st *store.Store, at time.Time) error {
	marked, err := st.MarkBackgroundTasksUnknown(ctx, at)
	if err != nil {
		return err
	}
	done := map[uuid.UUID]bool{}
	for _, t := range marked {
		if t.TaskTurnID == nil {
			continue
		}
		turn, err := st.TaskTurn(ctx, *t.TaskTurnID)
		if err != nil {
			return err
		}
		if done[turn.NodeRunID] {
			continue
		}
		done[turn.NodeRunID] = true
		if err := EmitNodeBackgroundTasks(ctx, st, turn); err != nil {
			return err
		}
	}
	return nil
}

// NodeBackgroundTasks is the console's list of the background tasks a node's turns started that still run.
func (s *Service) NodeBackgroundTasks(ctx context.Context, p auth.Principal, task, node uuid.UUID) (apitypes.TaskNodeBackgroundTasks, error) {
	if p.Kind != auth.KindConsole {
		return apitypes.TaskNodeBackgroundTasks{}, apperr.Forbidden("Only the console can read a node's background tasks.")
	}
	target, err := s.nodeTarget(ctx, task, node)
	if err != nil {
		return apitypes.TaskNodeBackgroundTasks{}, err
	}
	return NodeBackgroundTasks(ctx, s.Store, task, node, target.RuntimeID)
}

// StopNodeBackgroundTask stops one background task a node's turns started, for a person; the node's turn goes on.
func (s *Service) StopNodeBackgroundTask(ctx context.Context, p auth.Principal, task, node uuid.UUID, id string, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error) {
	return s.nodeBackgroundAction(ctx, p, task, node, id, in, func(target store.TaskConsoleLineage, running []store.BackgroundTask) (any, error) {
		for _, t := range running {
			if t.NativeID == id {
				return s.stopNodeTask(ctx, target, t)
			}
		}
		ended, err := s.Store.TaskTurnBackgroundTasksOfNode(ctx, node, id)
		if err != nil {
			return nil, err
		}
		if len(ended) == 0 {
			return nil, apperr.NotFoundCode(apperr.CodeBackgroundTaskNotFound, "This node has no background task "+id+".")
		}
		e := apperr.Conflict(apperr.CodeBackgroundTaskEnded, "Background task "+id+" has already ended.")
		e.Details = apitypes.BackgroundTaskEndedDetails{Task: apitypes.FromBackgroundTask(ended[len(ended)-1])}
		return nil, e
	})
}

// StopAllNodeBackgroundTasks stops every background task a node's turns started that still runs, for a person.
func (s *Service) StopAllNodeBackgroundTasks(ctx context.Context, p auth.Principal, task, node uuid.UUID, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error) {
	return s.nodeBackgroundAction(ctx, p, task, node, "", in, func(target store.TaskConsoleLineage, running []store.BackgroundTask) (any, error) {
		out := apitypes.BackgroundTaskList{Tasks: []apitypes.BackgroundTask{}}
		for _, t := range running {
			v, err := s.stopNodeTask(ctx, target, t)
			if err != nil {
				return nil, err
			}
			out.Tasks = append(out.Tasks, v)
		}
		return out, nil
	})
}

// stopNodeTask asks the executor to stop a task on the node's instance and answers it as its record keeps it then; a
// task that ended meanwhile answers its end.
func (s *Service) stopNodeTask(ctx context.Context, target store.TaskConsoleLineage, t store.BackgroundTask) (apitypes.BackgroundTask, error) {
	if stopper, ok := s.Executor.(executor.BackgroundStopper); ok {
		if _, err := stopper.StopBackground(ctx, target.RuntimeID, t.NativeID, executor.StopByPerson); err != nil && !errors.Is(err, executor.ErrBackgroundNotFound) {
			return apitypes.BackgroundTask{}, err
		}
	}
	stored, err := s.Store.BackgroundTaskByKey(ctx, t.Key)
	if err != nil {
		return apitypes.BackgroundTask{}, err
	}
	return apitypes.FromBackgroundTask(stored), nil
}

// nodeTarget is the node's Agent and task instance, as the console sees it.
func (s *Service) nodeTarget(ctx context.Context, task, node uuid.UUID) (store.TaskConsoleLineage, error) {
	var target store.TaskConsoleLineage
	_, err := s.Store.MutateTask(ctx, task, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		var e error
		target, e = consoleNodeTarget(m, node)
		return nil, e
	})
	return target, err
}

// errFresh tells a replay lookup that the action has not been done yet.
var errFresh = errors.New("the action has not been done yet")

// nodeBackgroundAction does a stop of a node's background tasks once per actionId, for the node's Agent and instance as
// the person expects them (another is a task_conflict); a replay answers what the first call answered.
func (s *Service) nodeBackgroundAction(ctx context.Context, p auth.Principal, task, node uuid.UUID, id string, in taskmodel.TaskNodeQueueActionInput,
	do func(target store.TaskConsoleLineage, running []store.BackgroundTask) (any, error)) (json.RawMessage, error) {
	if p.Kind != auth.KindConsole {
		return nil, apperr.Forbidden("Only the console can stop a node's background tasks.")
	}
	if task == uuid.Nil || node == uuid.Nil || in.ActionID == uuid.Nil || in.ExpectedAgentID == uuid.Nil || in.ExpectedRuntimeID == uuid.Nil {
		return nil, apperr.Invalid("body", "actionId, expectedAgentId and expectedRuntimeId must be non-zero UUIDs.")
	}
	op := "stopAllTaskNodeBackgroundTasks"
	if id != "" {
		op = "stopTaskNodeBackgroundTask"
	}
	action := bodyAction(p, in.ActionID, op, struct {
		NodeID uuid.UUID
		Task   string
		Input  taskmodel.TaskNodeQueueActionInput
	}{node, id, in})
	expected := store.TaskConsoleLineage{TaskID: task, NodeRunID: node, AgentID: in.ExpectedAgentID, RuntimeID: in.ExpectedRuntimeID}
	action.Authorize = func(m *store.TaskMutation) error {
		target, e := consoleNodeTarget(m, node)
		if e != nil {
			return e
		}
		if target != expected {
			return store.TaskConsoleConflict(m, expected, "target_mismatch")
		}
		return nil
	}
	out, err := s.Store.MutateTask(ctx, task, action, func(*store.TaskMutation) (any, error) { return nil, errFresh })
	if err == nil {
		return out, nil
	}
	if !errors.Is(err, errFresh) {
		return nil, err
	}
	running, err := s.Store.RunningNodeBackgroundTasks(ctx, node)
	if err != nil {
		return nil, err
	}
	value, err := do(expected, running)
	if err != nil {
		return nil, err
	}
	return s.Store.MutateTask(ctx, task, action, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		return value, nil
	})
}
