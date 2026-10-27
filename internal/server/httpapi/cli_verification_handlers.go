package httpapi

import (
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliverify"
	"net/http"
)

func (s *Server) cliVerificationReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.CLIVerification != nil {
		return true
	}
	s.fail(w, r, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "CLI验证组件尚未装配"})
	return false
}

func (s *Server) startProjectCLIVerification(w http.ResponseWriter, r *http.Request) {
	project, account, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cliverify.Start
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if e = in.Validate(); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliVerificationReady(w, r) {
		return
	}
	out, e := s.CLIVerification.Start(r.Context(), project, account, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 202, out)
}
func (s *Server) getProjectCLIVerificationState(w http.ResponseWriter, r *http.Request) {
	project, account, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliVerificationReady(w, r) {
		return
	}
	out, e := s.CLIVerification.State(r.Context(), project, account)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getProjectCLIVerificationOperation(w http.ResponseWriter, r *http.Request) {
	project, account, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	id, e := pathUUID(r, "operationId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliVerificationReady(w, r) {
		return
	}
	out, e := s.CLIVerification.Get(r.Context(), project, account, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) cancelProjectCLIVerificationOperation(w http.ResponseWriter, r *http.Request) {
	project, account, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	id, e := pathUUID(r, "operationId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliVerificationReady(w, r) {
		return
	}
	out, e := s.CLIVerification.Cancel(r.Context(), project, account, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
