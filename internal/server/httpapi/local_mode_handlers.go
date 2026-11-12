package httpapi

import (
	"net/http"

	"loop-now/internal/server/localmode"
)

func (s *Server) getLocalModeSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.LocalMode.Settings(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) updateLocalModeSettings(w http.ResponseWriter, r *http.Request) {
	var in localmode.SettingsPatch
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.LocalMode.UpdateSettings(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) getProjectLocalDirectories(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.LocalMode.ProjectDirectories(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putProjectLocalDirectories(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in localmode.PutInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.LocalMode.PutProjectDirectories(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) getAgentLocalDirectories(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.LocalMode.AgentDirectories(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putAgentLocalDirectories(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in localmode.PutInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.LocalMode.PutAgentDirectories(r.Context(), id, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
