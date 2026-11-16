package httpapi

import (
	"bytes"
	"encoding/json"
	"loop-now/internal/server/apperr"
	"net/http"
)

func promptText(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return nil, apperr.Invalid("prompt", "Must be a string. An empty string is allowed.")
	}
	return &value, nil
}
func promptVersion(raw json.RawMessage, required bool) (*int64, error) {
	if len(raw) == 0 && !required {
		return nil, nil
	}
	var value int64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || value < 1 {
		return nil, apperr.Invalid("expectedPromptVersion", "Must be a positive integer.")
	}
	return &value, nil
}
func (s *Server) getProjectPrompt(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Config.Store.GetProjectPrompt(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) putProjectPrompt(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Prompt   json.RawMessage `json:"prompt"`
		Expected json.RawMessage `json:"expectedPromptVersion"`
	}
	if err = readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	text, err := promptText(in.Prompt)
	if err == nil && text == nil {
		err = apperr.Invalid("prompt", "Required. Send the prompt text; an empty string is allowed.")
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	version, err := promptVersion(in.Expected, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Config.Store.PutProjectPrompt(r.Context(), id, *text, *version)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
