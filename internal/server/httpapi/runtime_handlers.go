package httpapi

import (
	"net/http"
	"strconv"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/runtimes"
)

// listRuntimes 报告各 Agent 运行时在本机是否可用；refresh=true 时不用缓存。
func (s *Server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	refresh := false
	if v := r.URL.Query().Get("refresh"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid("refresh", "只能是 true 或 false"))
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
