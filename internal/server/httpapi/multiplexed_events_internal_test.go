package httpapi

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/auth"
)

// A stream that fails after it started ends with stream.error, and from then on the combined cursor has no position
// for it: a reconnect with that cursor gives it a fresh snapshot instead of resuming it from a position it never
// confirmed. The other stream goes on, its position kept.
func TestMultiplexedStreamFailingMidway(t *testing.T) {
	s := &Server{Log: slog.New(slog.DiscardHandler)}
	s.O.SSEKeepalive = time.Second
	errorSeen := make(chan struct{})
	failing := muxStream{name: "a", open: func(context.Context, auth.Principal, string) (liveStream, error) {
		return liveStream{first: &sseOut{id: "1", event: "snapshot", data: []byte(`{}`)}, position: "1",
			follow: func(ctx context.Context, after string, emit func([]sseOut) bool) error {
				if !emit([]sseOut{{id: "2", event: "x", data: []byte(`{}`)}}) || !emit([]sseOut{{id: "3", event: "x", data: []byte(`{}`)}}) {
					return nil
				}
				return errors.New("the stream cannot be read")
			}}, nil
	}}
	going := muxStream{name: "b", open: func(context.Context, auth.Principal, string) (liveStream, error) {
		return liveStream{position: "5", follow: func(ctx context.Context, after string, emit func([]sseOut) bool) error {
			select {
			case <-errorSeen: // after the other stream's stream.error was read
			case <-ctx.Done():
				return nil
			}
			emit([]sseOut{{id: "6", event: "y", data: []byte(`{}`)}})
			<-ctx.Done()
			return nil
		}}, nil
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.serveMultiplexed(w, r, []muxStream{failing, going}, map[string]string{"b": "5"})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	type event struct{ id, name, data string }
	events := make(chan event, 16)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(resp.Body)
		var ev event
		for sc.Scan() {
			switch line := sc.Text(); {
			case line == "":
				if ev.name != "" {
					events <- ev
				}
				ev = event{}
			case strings.HasPrefix(line, "id: "):
				ev.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	read := func() event {
		t.Helper()
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the stream closed")
			}
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
		return event{}
	}
	for _, want := range []string{"a=1;b=5", "a=2;b=5", "a=3;b=5"} {
		if ev := read(); ev.name != "event" || ev.id != want {
			t.Fatalf("stream a's events: %+v, want id %s", ev, want)
		}
	}
	failed := read()
	if failed.name != "stream.error" || !strings.Contains(failed.data, `"stream":"a"`) || failed.id != "b=5" {
		t.Fatalf("stream a ends with stream.error and loses its position: %+v", failed)
	}
	close(errorSeen)
	if ev := read(); ev.name != "event" || ev.id != "b=6" || !strings.Contains(ev.data, `"stream":"b"`) {
		t.Fatalf("stream b goes on, and the cursor has no position for a: %+v", ev)
	}
	cancel()
	_, _ = io.Copy(io.Discard, resp.Body)
}
