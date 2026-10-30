package httpapi

import (
	"net/http"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelconfig"
)

func (s *Server) modelSettings() (*modelconfig.Service, *apperr.Error) {
	if s.ModelSettings == nil {
		return nil, &apperr.Error{Status: http.StatusServiceUnavailable, Code: apperr.CodeInternal, Message: "模型配置组件尚未装配"}
	}
	return s.ModelSettings, nil
}

func (s *Server) getModelSettings(w http.ResponseWriter, r *http.Request) {
	service, unavailable := s.modelSettings()
	if unavailable != nil {
		writeError(w, unavailable)
		return
	}
	settings, err := service.Get(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putModelSettings(w http.ResponseWriter, r *http.Request) {
	service, unavailable := s.modelSettings()
	if unavailable != nil {
		writeError(w, unavailable)
		return
	}
	var in struct {
		ModelsJSON       string `json:"modelsJson"`
		ExpectedRevision int64  `json:"expectedRevision"`
	}
	if err := readJSON(w, r, 1<<20+4096, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	settings, err := service.Put(r.Context(), in.ModelsJSON, in.ExpectedRevision)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}
