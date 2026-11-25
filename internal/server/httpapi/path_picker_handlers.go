package httpapi

import (
	"net/http"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/pathpicker"
)

// createPathPick shows this computer's own dialog to choose a folder or file and answers with the chosen path, or
// null when cancelled. It waits for the person; when the console closes the request, the dialog closes with it.
func (s *Server) createPathPick(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        *string `json:"kind"`
		DefaultPath *string `json:"defaultPath"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Kind == nil {
		s.fail(w, r, apperr.Invalid("kind", "Required: directory or file."))
		return
	}
	if s.PathPicker == nil {
		s.fail(w, r, apperr.Conflict(apperr.CodePathPickerUnavailable, "This server can't show a dialog to choose a path; type the path instead."))
		return
	}
	req := pathpicker.Request{Kind: pathpicker.Kind(*in.Kind)}
	if in.DefaultPath != nil {
		req.DefaultPath = *in.DefaultPath
	}
	path, err := s.PathPicker.Pick(r.Context(), req)
	if err != nil {
		if r.Context().Err() != nil {
			return // the console closed the request; the dialog is already closed
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.PathPick{Path: path})
}
