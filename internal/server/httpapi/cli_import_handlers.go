package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliimport"
	"net/http"
)

// CLIImportController is the console-only seam. A nil service is explicitly
// unavailable, never a successful scan or a callback pretending to import.
type CLIImportController interface {
	Scan(context.Context, uuid.UUID) (cliimport.Scan, error)
	Start(context.Context, uuid.UUID, cliimport.StartInput) (cliimport.Operation, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (cliimport.Operation, error)
	Cancel(context.Context, uuid.UUID, uuid.UUID) (cliimport.Operation, error)
}

func (s *Server) cliImportReady(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if s.CLIImport != nil {
		return true
	}
	s.fail(w, r, &apperr.Error{Status: 503, Code: apperr.CodeInternal, Message: "本机导入组件尚未装配"})
	return false
}
func importUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, e := pathUUID(r, name)
	if e != nil {
		return id, e
	}
	if id == uuid.Nil {
		return id, apperr.Invalid(name, "UUID不能为零")
	}
	return id, nil
}
func readImportJSON(w http.ResponseWriter, r *http.Request, out any) error {
	raw, e := readBody(w, r, 2<<20)
	if e != nil {
		return e
	}
	defer clear(raw)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return apperr.Invalid("body", "导入请求格式或字段无效")
	}
	return nil
}
func readImportEmpty(w http.ResponseWriter, r *http.Request) error {
	var body map[string]json.RawMessage
	if e := readImportJSON(w, r, &body); e != nil {
		return e
	}
	if body == nil || len(body) != 0 {
		return apperr.Invalid("body", "此操作只接受空对象")
	}
	return nil
}
func (s *Server) scanProjectCLICredentialImports(w http.ResponseWriter, r *http.Request) {
	p, e := importUUID(r, "projectId")
	if e == nil {
		e = readImportEmpty(w, r)
	}
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliImportReady(w, r) {
		return
	}
	out, e := s.CLIImport.Scan(r.Context(), p)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 201, out)
}
func (s *Server) startProjectCLICredentialImport(w http.ResponseWriter, r *http.Request) {
	p, e := importUUID(r, "projectId")
	var in cliimport.StartInput
	if e == nil {
		e = readImportJSON(w, r, &in)
	}
	if e == nil {
		e = in.Validate()
	}
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliImportReady(w, r) {
		return
	}
	out, e := s.CLIImport.Start(r.Context(), p, in)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 202, cliimport.State{Operation: out})
}
func (s *Server) getProjectCLICredentialImport(w http.ResponseWriter, r *http.Request) {
	p, e := importUUID(r, "projectId")
	id := uuid.Nil
	if e == nil {
		id, e = importUUID(r, "operationId")
	}
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliImportReady(w, r) {
		return
	}
	out, e := s.CLIImport.Get(r.Context(), p, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, cliimport.State{Operation: out})
}
func (s *Server) cancelProjectCLICredentialImport(w http.ResponseWriter, r *http.Request) {
	p, e := importUUID(r, "projectId")
	id := uuid.Nil
	if e == nil {
		id, e = importUUID(r, "operationId")
	}
	if e == nil {
		e = readImportEmpty(w, r)
	}
	if e != nil {
		s.fail(w, r, e)
		return
	}
	if !s.cliImportReady(w, r) {
		return
	}
	out, e := s.CLIImport.Cancel(r.Context(), p, id)
	if e != nil {
		s.fail(w, r, e)
		return
	}
	writeJSON(w, 200, cliimport.State{Operation: out})
}
