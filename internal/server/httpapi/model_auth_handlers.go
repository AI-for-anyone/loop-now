package httpapi

import (
	"errors"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"net/http"
)

func (s *Server) modelAuthReady(w http.ResponseWriter, r *http.Request) bool {
	if s.ModelAuth != nil {
		return true
	}
	writeError(w, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "The model login component is not set up on this server."})
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
	if input.Provider == "" || len(input.Provider) > 64 {
		s.fail(w, r, apperr.Invalid("provider", "Must be the Agent's current model provider, at most 64 bytes."))
		return
	}
	if input.ExpectedConfigVersion < 1 {
		s.fail(w, r, apperr.Invalid("expectedConfigVersion", "Must be the Agent's saved config version (at least 1)."))
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.Start(r.Context(), id, input)
	if e != nil {
		if errors.Is(e, modelauth.ErrHelperUnavailable) {
			e = &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "The model login component is not available right now. Try again later."}
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

// answerModelAuthLogin answers the question a platform Agent's plugin-provider login is waiting on. The answer is
// handed over in memory only: never stored or logged.
func (s *Server) answerModelAuthLogin(w http.ResponseWriter, r *http.Request) {
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
	var in cm.LoginInput
	// As with project logins, the raw body is wiped after decoding (the answer may be a pasted authorization code).
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	defer func() { in.Value = "" }()
	if in.PromptID == "" {
		s.fail(w, r, apperr.Invalid("promptId", "Give the ID of the login prompt you are answering."))
		return
	}
	if !s.modelAuthReady(w, r) {
		return
	}
	view, e := s.ModelAuth.Input(r.Context(), agent, id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
