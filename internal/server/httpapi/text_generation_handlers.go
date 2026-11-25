package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/textgen"
)

// textGeneration is the writer service; a server without one has no writer.
func (s *Server) textGeneration(w http.ResponseWriter, r *http.Request) (*textgen.Service, bool) {
	if s.TextGeneration == nil {
		s.fail(w, r, &apperr.Error{Status: http.StatusConflict, Code: apperr.CodeTextGenerationUnavailable, Message: "Writing with AI is not available on this server."})
		return nil, false
	}
	return s.TextGeneration, true
}

func (s *Server) getTextGenerationSettings(w http.ResponseWriter, r *http.Request) {
	if s.TextGeneration == nil {
		writeJSON(w, http.StatusOK, textgen.Settings{})
		return
	}
	v, err := s.TextGeneration.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putTextGenerationSettings(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.textGeneration(w, r)
	if !ok {
		return
	}
	var in struct {
		Selection json.RawMessage `json:"selection"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if len(in.Selection) == 0 {
		s.fail(w, r, apperr.Invalid("selection", "Required: a runtime model, or null for Automatic."))
		return
	}
	var sel *textgen.Selection
	if !bytes.Equal(bytes.TrimSpace(in.Selection), []byte("null")) {
		var v struct {
			Provider      *string `json:"provider"`
			ModelID       *string `json:"modelId"`
			ThinkingLevel *string `json:"thinkingLevel"`
		}
		dec := json.NewDecoder(bytes.NewReader(in.Selection))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&v); err != nil {
			s.fail(w, r, apperr.Invalid("selection", "Must be an object with provider, modelId and thinkingLevel, or null."))
			return
		}
		if v.Provider == nil {
			s.fail(w, r, apperr.Invalid("selection.provider", "Required."))
			return
		}
		if v.ModelID == nil {
			s.fail(w, r, apperr.Invalid("selection.modelId", "Required."))
			return
		}
		sel = &textgen.Selection{Provider: *v.Provider, ModelID: *v.ModelID, ThinkingLevel: v.ThinkingLevel}
	}
	v, err := tg.PutSettings(r.Context(), sel)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) createPromptDraft(w http.ResponseWriter, r *http.Request) {
	tg, ok := s.textGeneration(w, r)
	if !ok {
		return
	}
	var in struct {
		Target      *string    `json:"target"`
		AgentID     *uuid.UUID `json:"agentId"`
		ProjectID   *uuid.UUID `json:"projectId"`
		AgentName   *string    `json:"agentName"`
		Request     *string    `json:"request"`
		CurrentText *string    `json:"currentText"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	for field, missing := range map[string]bool{"target": in.Target == nil, "request": in.Request == nil, "currentText": in.CurrentText == nil} {
		if missing {
			s.fail(w, r, apperr.Invalid(field, "Required."))
			return
		}
	}
	d, err := tg.Draft(r.Context(), textgen.DraftRequest{Target: *in.Target, AgentID: in.AgentID, ProjectID: in.ProjectID, AgentName: in.AgentName,
		Request: *in.Request, CurrentText: *in.CurrentText})
	if err != nil {
		if r.Context().Err() != nil {
			return // the console closed the request; the CLI is already stopped
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
