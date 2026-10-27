package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
)

func (s *Server) getRunTimeline(w http.ResponseWriter, r *http.Request) {
	agentID, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	runID, err := pathUUID(r, "runId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tl, err := s.Records.Timeline(r.Context(), agentID, runID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	from, err := queryTime(r, "from")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	to, err := queryTime(r, "to")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := s.scopeFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Records.Usage(r.Context(), f, from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	var bounds [2]time.Time
	for i, name := range []string{"from", "to"} {
		t, err := queryTime(r, name)
		if err == nil && t == nil {
			err = apperr.Invalid(name, "必须提供")
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		bounds[i] = *t
	}
	f, err := s.scopeFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	report, err := s.Records.Stats(r.Context(), f, bounds[0], bounds[1], r.URL.Query().Get("bucket"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) listAnomalies(w http.ResponseWriter, r *http.Request) {
	from, err := queryTime(r, "from")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			s.fail(w, r, apperr.Invalid("limit", "必须是 1 到 100 之间的整数"))
			return
		}
		limit = n
	}
	f, err := s.scopeFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page, err := s.Records.Anomalies(r.Context(), f, from, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// queryTime 解析 RFC 3339 的查询参数；不传时返回 nil。
func queryTime(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return nil, apperr.Invalid(name, "必须是 RFC 3339 时间")
	}
	return &t, nil
}

// streamOverview 推送概览事件流，快照与续传规则同 Agent 事件流。
func (s *Server) streamOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f, err := s.scopeFilter(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, resume := r.Header.Get("Last-Event-ID"), false
	if cursor != "" {
		first, err := s.Coord.OverviewFirstID(ctx)
		if err == nil && first != "" && coord.CompareIDs(cursor, first) >= 0 {
			last, err := s.Coord.OverviewLastID(ctx)
			resume = err == nil && coord.CompareIDs(cursor, last) <= 0
		}
	}
	var snap apitypes.SseOverviewSnapshot
	if !resume {
		var err error
		snap, cursor, err = s.Conv.OverviewSnapshot(ctx, f)
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if !resume {
		data, _ := json.Marshal(snap)
		if !writeEvent(w, cursor, apitypes.EventOverviewSnapshot, data) {
			return
		}
	}
	_ = http.NewResponseController(w).Flush()
	s.pumpEventsFiltered(w, r, cursor, s.readOverview, overviewKeep(f), "stream", "overview")
}
