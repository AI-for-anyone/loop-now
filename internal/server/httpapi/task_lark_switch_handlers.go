package httpapi

import (
	"net/http"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

func (s *Server) switchTaskLarkAccount(w http.ResponseWriter, r *http.Request) {
	if principal(r).Kind != auth.KindConsole {
		s.fail(w, r, apperr.Forbidden("Only the console can switch task credentials."))
		return
	}
	if s.Tasks == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "The task credential handover service is not set up on this server."})
		return
	}
	task, err := pathUUID(r, "taskId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in taskmodel.TaskLarkSwitchInput
	if err = s.taskBody(w, r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Tasks.SwitchLarkAccount(r.Context(), principal(r), task, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) getTaskLarkAccountSwitch(w http.ResponseWriter, r *http.Request) {
	if principal(r).Kind != auth.KindConsole {
		s.fail(w, r, apperr.Forbidden("Only the console can switch task credentials."))
		return
	}
	if s.Tasks == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "The task credential handover service is not set up on this server."})
		return
	}
	task, err := pathUUID(r, "taskId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	action, err := pathUUID(r, "actionId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Tasks.LarkSwitchAction(r.Context(), principal(r), task, action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
