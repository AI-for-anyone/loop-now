package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

// maxMultiplexedStreams is how many streams one GET /events may carry (the contract's maxItems).
const maxMultiplexedStreams = 64

// muxStream is a stream named in GET /events: its name and how to open it.
type muxStream struct {
	name string
	open func(ctx context.Context, p auth.Principal, cursor string) (liveStream, error)
}

// parseMuxStream reads a stream name: agent:<agentId>, overview, overview:platform, overview:project:<projectId>,
// inbox, task:<taskId> or project-tasks:<projectId>. Whether the Agent, task or project exists is found out when the
// stream is opened.
func (s *Server) parseMuxStream(name string) (muxStream, bool) {
	id := func(prefix string) (uuid.UUID, bool) {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			return uuid.Nil, false
		}
		u, err := uuid.Parse(rest)
		return u, err == nil && u.String() == rest
	}
	m := muxStream{name: name}
	switch {
	case name == "inbox":
		m.open = func(ctx context.Context, _ auth.Principal, cursor string) (liveStream, error) {
			return s.openInboxStream(ctx, cursor)
		}
	case name == "overview" || name == "overview:platform":
		f := store.ScopeFilter{Platform: name == "overview:platform"}
		m.open = func(ctx context.Context, _ auth.Principal, cursor string) (liveStream, error) {
			return s.openOverviewStream(ctx, f, cursor)
		}
	case strings.HasPrefix(name, "overview:project:"):
		pid, ok := id("overview:project:")
		if !ok {
			return m, false
		}
		m.open = func(ctx context.Context, _ auth.Principal, cursor string) (liveStream, error) {
			if _, err := s.Store.GetProject(ctx, pid); err != nil {
				return liveStream{}, err
			}
			return s.openOverviewStream(ctx, store.ScopeFilter{ProjectID: &pid}, cursor)
		}
	case strings.HasPrefix(name, "agent:"):
		aid, ok := id("agent:")
		if !ok {
			return m, false
		}
		m.open = func(ctx context.Context, _ auth.Principal, cursor string) (liveStream, error) {
			return s.openAgentStream(ctx, aid, cursor)
		}
	case strings.HasPrefix(name, "task:"):
		tid, ok := id("task:")
		if !ok {
			return m, false
		}
		m.open = func(ctx context.Context, p auth.Principal, cursor string) (liveStream, error) {
			return s.openTaskStream(ctx, p, &tid, nil, cursor)
		}
	case strings.HasPrefix(name, "project-tasks:"):
		pid, ok := id("project-tasks:")
		if !ok {
			return m, false
		}
		m.open = func(ctx context.Context, p auth.Principal, cursor string) (liveStream, error) {
			return s.openTaskStream(ctx, p, nil, &pid, cursor)
		}
	default:
		return m, false
	}
	return m, true
}

// parseCombinedCursor reads a combined cursor: <stream>=<id> pairs separated by ";". Pairs that cannot be read are
// left out (their streams then start with a snapshot).
func parseCombinedCursor(raw string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(raw, ";") {
		name, id, ok := strings.Cut(part, "=")
		if ok && name != "" && id != "" && !strings.ContainsAny(id, "=\r\n") {
			out[name] = id
		}
	}
	return out
}

// combinedCursor writes the positions of the streams that have started, in the order they were asked for.
func combinedCursor(order []string, positions map[string]string) string {
	var b strings.Builder
	for _, name := range order {
		if id, ok := positions[name]; ok {
			if b.Len() > 0 {
				b.WriteByte(';')
			}
			b.WriteString(name + "=" + id)
		}
	}
	return b.String()
}

// multiplexedEvent mirrors the MultiplexedEvent schema.
type multiplexedEvent struct {
	Stream string          `json:"stream"`
	ID     string          `json:"id"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

// muxItem is what a stream's reader hands the writer: that it started (at position, with its snapshot in batch), a
// batch of events, or the error that ended it.
type muxItem struct {
	name     string
	started  bool
	position string
	batch    []sseOut
	err      error
}

// streamMultiplexed serves GET /events: several streams over one connection. Each stream is opened and followed by a
// reader of its own, exactly as its own endpoint would; one writer wraps their events, keeps every stream's position
// and gives each event the combined cursor as its id.
func (s *Server) streamMultiplexed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var streams []muxStream
	seen := map[string]bool{}
	for _, name := range q["stream"] {
		if seen[name] {
			continue
		}
		m, ok := s.parseMuxStream(name)
		if !ok {
			s.fail(w, r, apperr.Invalid("stream", "Unknown stream: "+name))
			return
		}
		seen[name] = true
		streams = append(streams, m)
	}
	switch {
	case len(streams) == 0:
		s.fail(w, r, apperr.Invalid("stream", "Name at least one stream."))
		return
	case len(streams) > maxMultiplexedStreams:
		s.fail(w, r, apperr.Invalid("stream", fmt.Sprintf("At most %d streams.", maxMultiplexedStreams)))
		return
	}
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = q.Get("cursor")
	}
	s.serveMultiplexed(w, r, streams, parseCombinedCursor(raw))
}

// serveMultiplexed carries the streams over the connection, each from its position in cursors (by name; none means
// its snapshot first).
func (s *Server) serveMultiplexed(w http.ResponseWriter, r *http.Request, streams []muxStream, cursors map[string]string) {
	order := make([]string, len(streams))
	for i, m := range streams {
		order[i] = m.name
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if rc.Flush() != nil {
		return
	}

	p := principal(r)
	items := make(chan muxItem)
	var readers sync.WaitGroup
	defer readers.Wait()
	defer cancel() // runs first: the readers stop, then the handler waits for them
	for _, m := range streams {
		readers.Go(func() {
			send := func(it muxItem) bool {
				select {
				case items <- it:
					return true
				case <-ctx.Done():
					return false
				}
			}
			ls, err := m.open(ctx, p, cursors[m.name])
			if err != nil {
				send(muxItem{name: m.name, err: err})
				return
			}
			start := muxItem{name: m.name, started: true, position: ls.position}
			if ls.first != nil {
				start.batch = []sseOut{*ls.first}
			}
			if !send(start) {
				return
			}
			err = ls.follow(ctx, ls.position, func(batch []sseOut) bool {
				if len(batch) == 0 {
					return ctx.Err() == nil
				}
				return send(muxItem{name: m.name, batch: batch})
			})
			if err != nil && ctx.Err() == nil {
				s.Log.Warn("failed to read an event stream", "stream", m.name, "err", err)
				send(muxItem{name: m.name, err: err})
			}
		})
	}

	keepalive := s.O.SSEKeepalive
	if keepalive <= 0 {
		keepalive = 15 * time.Second
	}
	tick := time.NewTicker(min(keepalive, time.Second))
	defer tick.Stop()
	// Every stream asked for with a position keeps it from the first event on, before its reader has even opened it: a
	// reconnect in the meantime then resumes it from there instead of giving it a fresh snapshot. A stream that starts
	// with a snapshot replaces it; one that cannot be opened loses it.
	positions := map[string]string{}
	for _, name := range order {
		if c, ok := cursors[name]; ok {
			positions[name] = c
		}
	}
	lastWrite := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if time.Since(lastWrite) < keepalive {
				continue
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			lastWrite = time.Now()
		case it := <-items:
			if it.err != nil {
				delete(positions, it.name)
				code, msg := apperr.CodeInternal, "The stream could not be read."
				var ae *apperr.Error
				if errors.As(it.err, &ae) {
					code, msg = ae.Code, ae.Message
				}
				data, _ := json.Marshal(map[string]any{"stream": it.name, "error": map[string]string{"code": code, "message": msg}})
				if !writeEvent(w, combinedCursor(order, positions), "stream.error", data) {
					return
				}
			} else {
				if it.started {
					positions[it.name] = it.position
				}
				for _, ev := range it.batch {
					positions[it.name] = ev.id
					data, _ := json.Marshal(multiplexedEvent{Stream: it.name, ID: ev.id, Event: ev.event, Data: ev.data})
					if !writeEvent(w, combinedCursor(order, positions), "event", data) {
						return
					}
				}
				if len(it.batch) == 0 {
					continue
				}
			}
			if rc.Flush() != nil {
				return
			}
			lastWrite = time.Now()
		}
	}
}
