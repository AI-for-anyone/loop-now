package httpapi

import (
	"net/http"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
)

func (s *Server) switchTaskLarkAccount(w http.ResponseWriter, r *http.Request) {
	if principal(r).Kind != auth.KindConsole {
		s.fail(w, r, apperr.Forbidden("任务凭证切换仅限控制台"))
		return
	}
	if s.Tasks == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "任务凭证交接服务尚未装配"})
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
		s.fail(w, r, apperr.Forbidden("任务凭证切换仅限控制台"))
		return
	}
	if s.Tasks == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "任务凭证交接服务尚未装配"})
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
