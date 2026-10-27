package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) projectCredentialReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.ProjectCredentials != nil {
		return true
	}
	s.fail(w, r, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "项目凭证组件尚未装配"})
	return false
}

// Never include decoder errors: unknown JSON field names can themselves contain
// secrets. New credential inputs expose only fixed safe validation text.
func (s *Server) readProjectCredentialJSON(w http.ResponseWriter, r *http.Request, out any) error {
	raw, e := readBody(w, r, s.O.Limits.MaxCredential+4096)
	if e != nil {
		return e
	}
	defer clear(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return apperr.Invalid("body", "请求格式或字段无效")
	}
	return nil
}
func projectAccountIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		return uuid.Nil, uuid.Nil, e
	}
	account, e := pathUUID(r, "accountId")
	return project, account, e
}
func credentialQuery(r *http.Request, impact bool) (string, string, error) {
	q := r.URL.Query()
	for key, values := range q {
		if key != "purpose" && key != "provider" && key != "cli" && !(impact && key == "accountId") {
			return "", "", apperr.Invalid("query", "查询参数无效")
		}
		if len(values) != 1 || values[0] == "" {
			return "", "", apperr.Invalid("query", "查询字段须为非空单值")
		}
	}
	purpose, provider, cli := q.Get("purpose"), q.Get("provider"), q.Get("cli")
	if purpose == "" {
		if impact || provider != "" || cli != "" {
			return "", "", apperr.Invalid("purpose", "须提供对应用途")
		}
		return "", "", nil
	}
	if purpose == "model" && cli == "" && (!impact || provider != "") {
		return purpose, provider, nil
	}
	if purpose == "cli" && provider == "" && (!impact || cli != "") {
		return purpose, cli, nil
	}
	return "", "", apperr.Invalid("purpose", "用途与provider/cli过滤不匹配")
}
func (s *Server) listProjectCredentialAccounts(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	purpose, slot, e := credentialQuery(r, false)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.List(r.Context(), id, purpose, slot)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) createProjectCredentialAccount(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.Create
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	defer func() { in.Credential.Key = ""; clear(in.Credential.Fields) }()
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Create(r.Context(), id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) getProjectCredentialAccount(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Get(r.Context(), project, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) renameProjectCredentialAccount(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.Rename
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Rename(r.Context(), project, id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) replaceProjectCredentialAccount(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.Replace
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	defer func() { in.Credential.Key = ""; clear(in.Credential.Fields) }()
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Replace(r.Context(), project, id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) deleteProjectCredentialAccount(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectAccountIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	values := r.URL.Query()["expectedRevision"]
	if len(values) != 1 {
		s.fail(w, r, apperr.Invalid("expectedRevision", "须提供单个预期版本"))
		return
	}
	revision, e := strconv.ParseInt(values[0], 10, 64)
	if e != nil || revision < 1 {
		s.fail(w, r, apperr.Invalid("expectedRevision", "须为正整数"))
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Delete(r.Context(), project, id, revision)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getProjectCredentialAccountImpact(w http.ResponseWriter, r *http.Request) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	purpose, slot, e := credentialQuery(r, true)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var account *uuid.UUID
	if raw := r.URL.Query().Get("accountId"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil || id == uuid.Nil {
			s.fail(w, r, apperr.Invalid("accountId", "须为有效UUID"))
			return
		}
		account = &id
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Impact(r.Context(), project, purpose, slot, account)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) getAgentCredentialSelections(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Selections(r.Context(), id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) putAgentCredentialSelection(w http.ResponseWriter, r *http.Request) {
	id, e := pathUUID(r, "agentId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.SelectionPut
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Select(r.Context(), id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) listProjectModelAuthOperations(w http.ResponseWriter, r *http.Request) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	provider := r.URL.Query().Get("provider")
	if provider != "" && provider != "openai-codex" {
		s.fail(w, r, apperr.Invalid("provider", "仅支持openai-codex"))
		return
	}
	var account *uuid.UUID
	if raw := r.URL.Query().Get("accountId"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil || id == uuid.Nil {
			s.fail(w, r, apperr.Invalid("accountId", "UUID无效"))
			return
		}
		account = &id
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.ListLogins(r.Context(), project, provider, account)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) startProjectModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.LoginStart
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Start(r.Context(), project, in)
	if e != nil {
		if errors.Is(e, modelauth.ErrHelperUnavailable) {
			e = &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "认证辅助组件暂不可用"}
		}
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 202, out)
}
func projectOperationIDs(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	project, e := pathUUID(r, "projectId")
	if e != nil {
		return uuid.Nil, uuid.Nil, e
	}
	id, e := pathUUID(r, "operationId")
	return project, id, e
}
func (s *Server) getProjectModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectOperationIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.GetLogin(r.Context(), project, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) cancelProjectModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectOperationIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	body, e := readBody(w, r, 1024)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if strings.TrimSpace(string(body)) != "" {
		clear(body)
		s.fail(w, r, apperr.Invalid("body", "取消登录不接受请求体"))
		return
	}
	clear(body)
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.CancelLogin(r.Context(), project, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
}
