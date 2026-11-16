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
	s.fail(w, r, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "The project credential component is not set up on this server."})
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
		return apperr.Invalid("body", "The request must be a single JSON object with only the documented fields.")
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
			return "", "", apperr.Invalid("query", "Unsupported query parameter. Use purpose, provider or cli, and accountId where this endpoint supports it.")
		}
		if len(values) != 1 || values[0] == "" {
			return "", "", apperr.Invalid("query", "Each query parameter must appear once, with a non-empty value.")
		}
	}
	purpose, provider, cli := q.Get("purpose"), q.Get("provider"), q.Get("cli")
	if purpose == "" {
		if impact || provider != "" || cli != "" {
			return "", "", apperr.Invalid("purpose", "Required here. Must be model or cli.")
		}
		return "", "", nil
	}
	if purpose == "model" && cli == "" && (!impact || provider != "") {
		return purpose, provider, nil
	}
	if purpose == "cli" && provider == "" && (!impact || cli != "") {
		return purpose, cli, nil
	}
	return "", "", apperr.Invalid("purpose", "Does not match the filter. Use purpose=model with provider, or purpose=cli with cli.")
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
		s.fail(w, r, apperr.Invalid("expectedRevision", "Must be given exactly once."))
		return
	}
	revision, e := strconv.ParseInt(values[0], 10, 64)
	if e != nil || revision < 1 {
		s.fail(w, r, apperr.Invalid("expectedRevision", "Must be a positive integer."))
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
			s.fail(w, r, apperr.Invalid("accountId", "Must be a valid non-zero UUID."))
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
	// provider is optional; without it all logins in progress are listed (Codex and plugin providers), each with provider.
	provider := r.URL.Query().Get("provider")
	var account *uuid.UUID
	if raw := r.URL.Query().Get("accountId"); raw != "" {
		id, e := uuid.Parse(raw)
		if e != nil || id == uuid.Nil {
			s.fail(w, r, apperr.Invalid("accountId", "Must be a valid non-zero UUID."))
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
			e = &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "The login helper is not available right now. Try again later."}
		}
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 202, out)
}

// answerProjectModelAuthLogin answers the question a plugin provider login is waiting on (plugins/SPEC.md §13). The
// answer is only passed in memory to the login helper process.
func (s *Server) answerProjectModelAuthLogin(w http.ResponseWriter, r *http.Request) {
	project, id, e := projectOperationIDs(r)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	var in cm.LoginInput
	if e = s.readProjectCredentialJSON(w, r, &in); e != nil {
		s.fail(w, r, e)
		return
	}
	defer func() { in.Value = "" }()
	if in.PromptID == "" {
		s.fail(w, r, apperr.Invalid("promptId", "Give the ID of the login prompt you are answering."))
		return
	}
	if !s.projectCredentialReady(w, r) {
		return
	}
	out, e := s.ProjectCredentials.Input(r.Context(), project, id, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, out)
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
		s.fail(w, r, apperr.Invalid("body", "Cancelling a login does not take a request body."))
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
