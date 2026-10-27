package httpapi

import (
	"net/http"

	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/sleepmodel"
)

func (s *Server) requestSleep(w http.ResponseWriter, r *http.Request) {
	var in sleepmodel.Input
	if err := readJSON(w, r, s.O.Limits.MaxMessage+1024, &in); err != nil {
		writeError(w, err)
		return
	}
	p := principal(r)
	project, agent, task := s.principalScope(r, p)
	o, err := s.hook(r, pluginhost.Modify, "sleep.before_schedule", project, agent, task, &in.RunID, map[string]any{"durationSeconds": in.Seconds, "reason": in.Reason})
	if err != nil {
		writeError(w, err)
		return
	}
	if v, ok := o.Patch["durationSeconds"].(float64); ok && v >= 1 && v == float64(int64(v)) {
		in.Seconds = int64(v)
	}
	out, err := s.Store.RequestSleep(r.Context(), p, in, int(s.O.Limits.MaxMessage))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) getSleepState(w http.ResponseWriter, r *http.Request) {
	out, err := s.Store.SleepState(r.Context(), principal(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
