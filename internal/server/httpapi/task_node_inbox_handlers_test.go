package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loop-now/internal/server/auth"
)

func TestTaskNodeTimelineUnavailable(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/unused", nil)
	r = r.WithContext(auth.With(r.Context(), auth.Console()))
	w := httptest.NewRecorder()
	s.getTaskNodeRunTimeline(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unassembled records status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestTaskNodeOptionalRunBody(t *testing.T) {
	s := &Server{}
	for _, raw := range []string{"", "{}", "null", `{"runId":null}`, `{"runId":"00000000-0000-0000-0000-000000000000"}`} {
		t.Run(raw, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/unused", strings.NewReader(raw))
			_, err := s.optionalInboxRun(httptest.NewRecorder(), r)
			wantError := raw != "" && raw != "{}"
			if (err != nil) != wantError {
				t.Fatalf("body=%q err=%v", raw, err)
			}
		})
	}
}
