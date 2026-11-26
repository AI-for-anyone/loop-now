package httpapi

import (
	"errors"
	"net/http"

	"loop-now/internal/server/apitypes"
)

var errNoImport = errors.New("conversation import is not available")

// startConversationScan is POST /conversation-scans.
func (s *Server) startConversationScan(w http.ResponseWriter, r *http.Request) {
	if s.ConvImport == nil {
		s.fail(w, r, errNoImport)
		return
	}
	var in apitypes.ConversationScanInput
	if err := readJSON(w, r, 4096, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.ConvImport.StartScan(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

// getConversationScan is GET /conversation-scans/{operationId}.
func (s *Server) getConversationScan(w http.ResponseWriter, r *http.Request) {
	if s.ConvImport == nil {
		s.fail(w, r, errNoImport)
		return
	}
	id, err := pathUUID(r, "operationId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.ConvImport.Scan(id.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// startConversationImport is POST /conversation-imports.
func (s *Server) startConversationImport(w http.ResponseWriter, r *http.Request) {
	if s.ConvImport == nil {
		s.fail(w, r, errNoImport)
		return
	}
	var in apitypes.ConversationImportInput
	if err := readJSON(w, r, 1<<20, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.ConvImport.StartImport(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}

// getConversationImport is GET /conversation-imports/{operationId}.
func (s *Server) getConversationImport(w http.ResponseWriter, r *http.Request) {
	if s.ConvImport == nil {
		s.fail(w, r, errNoImport)
		return
	}
	id, err := pathUUID(r, "operationId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.ConvImport.Import(id.String())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
