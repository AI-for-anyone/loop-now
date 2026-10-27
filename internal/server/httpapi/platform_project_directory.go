package httpapi

import (
	"net/http"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

func (s *Server) listPlatformProjects(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Kind != auth.KindAgent {
		s.fail(w, r, apperr.Forbidden("仅平台 Agent 可以读取项目目录"))
		return
	}
	// Re-read current persisted ownership; neither a token claim nor a supplied
	// scope/project/Agent parameter can authorize global discovery.
	a, err := s.Store.GetAddressableAgent(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, unauthenticatedIfNotFound(err))
		return
	}
	if a.ProjectID != nil {
		s.fail(w, r, apperr.Forbidden("仅平台 Agent 可以读取项目目录"))
		return
	}
	items, err := s.Store.ListPlatformProjectDirectory(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Projects []store.PlatformProjectDirectoryItem `json:"projects"`
	}{Projects: items})
}
