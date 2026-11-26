package httpapi

import (
	"errors"
	"net/http"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

var errNoPermissions = errors.New("permission requests are not available")

// getPermissionSettings is GET /permission-settings.
func (s *Server) getPermissionSettings(w http.ResponseWriter, r *http.Request) {
	mode, err := s.Store.PermissionDefaultMode(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.PermissionSettings{DefaultMode: mode})
}

// putPermissionSettings is PUT /permission-settings.
func (s *Server) putPermissionSettings(w http.ResponseWriter, r *http.Request) {
	var in apitypes.PermissionSettings
	if err := readJSON(w, r, 1024, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	switch in.DefaultMode {
	case store.PermissionModeSupervised, store.PermissionModeAcceptEdits, store.PermissionModeAuto, store.PermissionModeFull:
	default:
		s.fail(w, r, apperr.Invalid("defaultMode", "Must be supervised, accept_edits, auto or full."))
		return
	}
	mode, err := s.Store.SetPermissionDefaultMode(r.Context(), in.DefaultMode)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.PermissionSettings{DefaultMode: mode})
}

// listAgentPermissionRequests is GET /agents/{agentId}/permission-requests (state=pending).
func (s *Server) listAgentPermissionRequests(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if state := r.URL.Query().Get("state"); state != "" && state != "pending" {
		s.fail(w, r, apperr.Invalid("state", "Must be pending."))
		return
	}
	if _, err := s.Store.GetAgent(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	pending, err := s.Store.PendingPermissionRequests(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.PermissionRequestList{Requests: apitypes.FromPermissionRequests(pending)})
}

// answerPermissionRequest is POST /permission-requests/{permissionRequestId}/answer.
func (s *Server) answerPermissionRequest(w http.ResponseWriter, r *http.Request) {
	if s.Permissions == nil {
		s.fail(w, r, errNoPermissions)
		return
	}
	id, err := pathUUID(r, "permissionRequestId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in apitypes.PermissionAnswer
	if err := readJSON(w, r, 32<<10, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		s.fail(w, r, apperr.Invalid("decision", "Must be approve or reject."))
		return
	}
	reason := ""
	if in.Reason != nil {
		if len([]rune(*in.Reason)) > 4000 {
			s.fail(w, r, apperr.Invalid("reason", "Must be at most 4000 characters."))
			return
		}
		if in.Decision == "reject" {
			reason = *in.Reason
		}
	}
	out, err := s.Permissions.Answer(r.Context(), id, in.Decision == "approve", reason)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromPermissionRequest(out))
}

// listTaskNodePermissionRequests is GET /tasks/{taskId}/nodes/{nodeRunId}/permission-requests.
func (s *Server) listTaskNodePermissionRequests(w http.ResponseWriter, r *http.Request) {
	if !s.taskReady(w, r) {
		return
	}
	task, err := pathUUID(r, "taskId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	node, err := pathUUID(r, "nodeRunId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Tasks.NodePermissionRequests(r.Context(), principal(r), task, node)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
