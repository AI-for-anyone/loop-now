package httpapi

import (
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"net/http"
	"strings"
)

func (s *Server) cliAuthReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.CLIAuth != nil {
		return true
	}
	s.fail(w, r, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "CLI认证组件尚未装配"})
	return false
}
func (s *Server) startProjectCLIAuthOperation(w http.ResponseWriter, r *http.Request) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.CLIAuthStart
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliAuthReady(w, r) {
		return
	}
	out, e := s.CLIAuth.Start(r.Context(), project, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 202, out)
}
func (s *Server) listProjectCLIAuthOperations(w http.ResponseWriter, r *http.Request) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var account *uuid.UUID
	cli := "gcloud"
	for key, values := range r.URL.Query() {
		if (key != "accountId" && key != "cli") || len(values) != 1 {
			s.fail(w, r, apperr.Invalid("query", "查询参数无效"))
			return
		}
		if key == "cli" {
			if values[0] != "gh" && values[0] != "gcloud" && values[0] != "lark-cli" {
				s.fail(w, r, apperr.Invalid("cli", "CLI登录筛选无效"))
				return
			}
			cli = values[0]
			continue
		}
		id, e := uuid.Parse(values[0])
		if e != nil || id == uuid.Nil {
			s.fail(w, r, apperr.Invalid("accountId", "UUID无效"))
			return
		}
		account = &id
	}
	if !s.cliAuthReady(w, r) {
		return
	}
	out, e := s.CLIAuth.List(r.Context(), project, account, cli)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getProjectCLIAuthOperation(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectOperationIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliAuthReady(w, r) {
		return
	}
	out, e := s.CLIAuth.Get(r.Context(), project, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) cancelProjectCLIAuthOperation(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectOperationIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	raw, e := readBody(w, r, 1024)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	defer clear(raw)
	if strings.TrimSpace(string(raw)) != "" {
		s.fail(w, r, apperr.Invalid("body", "取消登录不接受请求体"))
		return
	}
	if !s.cliAuthReady(w, r) {
		return
	}
	out, e := s.CLIAuth.Cancel(r.Context(), project, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
