package httpapi

import (
	"net/http"
	"strconv"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/runtimes"
)

// listRuntimes reports whether each Agent runtime is available on this machine; refresh=true bypasses the cache.
func (s *Server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	refresh := false
	if v := r.URL.Query().Get("refresh"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid("refresh", "Must be true or false."))
			return
		}
		refresh = b
	}
	list := []runtimes.Status{}
	if s.Runtimes != nil {
		list = s.Runtimes.List(r.Context(), refresh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": list})
}
