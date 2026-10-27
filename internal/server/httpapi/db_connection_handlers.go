package httpapi

import (
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"net/http"
	"strconv"
)

func (s *Server) getAgentDBConnections(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.Store.GetDBConnections(r.Context(), id, r.PathValue("cli"))
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) putAgentDBConnections(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.DBConnectionsPut
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.Store.PutDBConnections(r.Context(), id, r.PathValue("cli"), in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) deleteAgentDBConnections(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	q := r.URL.Query()
	values := q["expectedConfigVersion"]
	if len(q) != 1 || len(values) != 1 {
		s.fail(w, r, apperr.Invalid("expectedConfigVersion", "须提供单个配置版本"))
		return
	}
	expected, e := strconv.ParseInt(values[0], 10, 64)
	if e != nil || expected < 1 {
		s.fail(w, r, apperr.Invalid("expectedConfigVersion", "配置版本须为正整数"))
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.Store.DeleteDBConnections(r.Context(), id, r.PathValue("cli"), expected)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
