package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"net/http"
	"strconv"
)

func (s *Server) sendTaskNodeInboxMessage(w http.ResponseWriter, r *http.Request) {
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
	var in taskmodel.TaskNodeMessageInput
	if e = readJSON(w, r, s.inboxBodyLimit(), &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if len(in.ImageIDs) > s.O.Limits.MaxMessageImages {
		s.fail(w, r, apperr.TooLarge("Too many images."))
		return
	}
	if len(in.ImageIDs) > 0 {
		if e = s.imageModel(r.Context(), store.ImageOwner{AgentID: in.ExpectedAgentID, TaskID: task, NodeRunID: node, RuntimeID: in.ExpectedRuntimeID}); e != nil {
			s.fail(w, r, e)
			return
		}
	}
	out, e := s.Tasks.SendNodeInboxMessage(r.Context(), principal(r), task, node, in, int(s.O.Limits.MaxInboxMessage))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}

// nodeQueueAction handles a node queue action (promote to interrupt, cancel, stop): the task, node and subject come
// from the path, the body is TaskNodeQueueActionInput.
func (s *Server) nodeQueueAction(param string, do func(ctx context.Context, p auth.Principal, task, node, subject uuid.UUID, in taskmodel.TaskNodeQueueActionInput) (json.RawMessage, error)) http.HandlerFunc {
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
		subject, e := pathUUID(r, param)
		if e != nil {
			s.fail(w, r, e)
			return
		}
		var in taskmodel.TaskNodeQueueActionInput
		if e = readJSON(w, r, 4096, &in); e != nil {
			s.fail(w, r, e)
			return
		}
		out, e := do(r.Context(), principal(r), task, node, subject, in)
		if e != nil {
			s.fail(w, r, e)
			return
		}
		writeJSON(w, 200, out)
	}
}
func (s *Server) getTaskNodeQueue(w http.ResponseWriter, r *http.Request) {
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
	out, e := s.Tasks.NodeQueue(r.Context(), principal(r), task, node)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) listTaskNodeInboxMessages(w http.ResponseWriter, r *http.Request) {
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
	limit, e := taskInboxLimit(r, 50, 100)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.NodeInboxMessages(r.Context(), principal(r), task, node, r.URL.Query().Get("cursor"), limit)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getTaskNodeInboxAction(w http.ResponseWriter, r *http.Request) {
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
	action, e := pathUUID(r, "actionId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Tasks.NodeInboxAction(r.Context(), principal(r), task, node, action)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getTaskNodeRunTimeline(w http.ResponseWriter, r *http.Request) {
	if principal(r).Kind != auth.KindConsole {
		s.fail(w, r, apperr.Forbidden("Only the console can read task run records."))
		return
	}
	if s.Records == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "The task run record component is not set up on this server."})
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
	run, e := pathUUID(r, "runId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	limit, e := taskInboxLimit(r, 100, 200)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	out, e := s.Records.TaskNodeTimeline(r.Context(), task, node, run, r.URL.Query().Get("cursor"), limit)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func taskInboxLimit(r *http.Request, def, max int) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def, nil
	}
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 || n > max {
		return 0, apperr.Invalid("limit", "Must be an integer from 1 to the maximum page size.")
	}
	return n, nil
}
func parseExplicitInboxRun(raw json.RawMessage) (uuid.UUID, error) {
	if raw == nil {
		return uuid.Nil, nil
	}
	var id uuid.UUID
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &id) != nil || id == uuid.Nil {
		return uuid.Nil, apperr.Invalid("runId", "Must be a non-zero run UUID.")
	}
	return id, nil
}
func (s *Server) optionalInboxRun(w http.ResponseWriter, r *http.Request) (uuid.UUID, error) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return uuid.Nil, nil
	}
	var in apitypes.InboxReadInput
	if e := readJSON(w, r, 1024, &in); e != nil {
		return uuid.Nil, e
	}
	if in.RunID == nil {
		return uuid.Nil, nil
	}
	return *in.RunID, nil
}
