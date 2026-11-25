package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskmodel"
)

// stopBackgroundTask stops one of the Agent's running background tasks (POST /agents/{agentId}/background-tasks/
// {backgroundTaskId}/stop); with all set it stops every one (POST /agents/{agentId}/background-tasks/stop).
func (s *Server) stopBackgroundTask(all bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathUUID(r, "agentId")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in apitypes.AgentQueueActionInput
		if err = readJSON(w, r, 4096, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if in.ActionID == uuid.Nil {
			s.fail(w, r, apperr.Invalid("actionId", "A valid actionId is required."))
			return
		}
		if all {
			out, err := s.Conv.StopAllBackgroundTasks(r.Context(), id, in.ActionID)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		task := r.PathValue("backgroundTaskId")
		if task == "" {
			s.fail(w, r, apperr.Invalid("backgroundTaskId", "Must not be empty."))
			return
		}
		out, err := s.Conv.StopBackgroundTask(r.Context(), id, task, in.ActionID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// listTaskNodeBackgroundTasks is GET /tasks/{taskId}/nodes/{nodeRunId}/background-tasks.
func (s *Server) listTaskNodeBackgroundTasks(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	task, e := pathUUID(r, "taskId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	node, e := pathUUID(r, "nodeRunId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.NodeBackgroundTasks(r.Context(), principal(r), task, node)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// stopTaskNodeBackgroundTask stops one background task of a node (…/background-tasks/{backgroundTaskId}/stop); with all
// set, every one (…/background-tasks/stop).
func (s *Server) stopTaskNodeBackgroundTask(all bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.taskReady(w, r) {
			return
		}
		task, e := pathUUID(r, "taskId")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		node, e := pathUUID(r, "nodeRunId")
		if e != nil {
			s.fail(w, r, e)
			return
		}
		var in taskmodel.TaskNodeQueueActionInput
		if e = readJSON(w, r, 4096, &in); e != nil {
			s.fail(w, r, e)
			return
		}
		var out []byte
		if all {
			out, e = s.Tasks.StopAllNodeBackgroundTasks(r.Context(), principal(r), task, node, in)
		} else if id := r.PathValue("backgroundTaskId"); id == "" {
			e = apperr.Invalid("backgroundTaskId", "Must not be empty.")
		} else {
			out, e = s.Tasks.StopNodeBackgroundTask(r.Context(), principal(r), task, node, id, in)
		}
		if e != nil {
			s.fail(w, r, e)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
