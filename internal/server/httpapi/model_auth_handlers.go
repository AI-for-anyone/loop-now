package httpapi

import (
	"errors"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelauth"
	"net/http"
)

func (s *Server) modelAuthReady(w http.ResponseWriter, r *http.Request) bool {
	if s.ModelAuth != nil {
		return true
	}
	writeError(w, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "模型账号认证组件尚未装配"})
	return false
}
func (s *Server) getModelAuthState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.State(r.Context(), id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s *Server) startModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var input modelauth.LoginInput
	if e = readJSON(w, r, 4096, &input); e != nil {
		s.fail(w, r, e)
		return
	}
	if input.Provider != "openai-codex" || input.ExpectedConfigVersion < 1 {
		s.fail(w, r, apperr.Invalid("provider", "须选择openai-codex并提供已保存的配置版本"))
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.Start(r.Context(), id, input)
	if e != nil {
		if errors.Is(e, modelauth.ErrHelperUnavailable) {
			e = &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "模型账号认证组件暂不可用"}
		}
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s *Server) getModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	id, e := pathUUID(r, "operationId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.Get(r.Context(), agent, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
func (s *Server) cancelModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	agent, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	id, e := pathUUID(r, "operationId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var input struct{}
	if e = readJSON(w, r, 1024, &input); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.Cancel(r.Context(), agent, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
