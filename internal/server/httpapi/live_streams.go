package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// sseOut is one event of a live stream: its id in the stream, its name and its data.
type sseOut struct {
	id, event string
	data      []byte
}

// liveStream is one of the five event streams opened for a reader: what goes first and how to follow it. Its own
// endpoint serves it alone (serveStream); GET /events carries several over one connection.
type liveStream struct {
	// first is the snapshot sent before the events; nil when the stream resumes after the reader's cursor.
	first *sseOut
	// position is where the stream goes on from: the snapshot's id, or the cursor it resumes after.
	position string
	// follow passes the events after position to emit, in order and a batch at a time (an empty batch now and then
	// when nothing happens), until ctx ends or emit returns false. An error means the stream cannot be read any more.
	follow func(ctx context.Context, after string, emit func([]sseOut) bool) error
}

// coordFollow follows a stream kept by the coordinator: blocking reads of at most a second, so an idle stream still
// reports (empty batches) and stops promptly. When keep is not nil only the events it keeps are passed on; the others
// still advance the read position.
func (s *Server) coordFollow(read func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error), keep func(coord.Event) bool) func(context.Context, string, func([]sseOut) bool) error {
	return func(ctx context.Context, after string, emit func([]sseOut) bool) error {
		block := min(s.O.SSEKeepalive, time.Second)
		for ctx.Err() == nil {
			events, err := read(ctx, after, block)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			batch := make([]sseOut, 0, len(events))
			for _, ev := range events {
				after = ev.ID
				if keep == nil || keep(ev) {
					batch = append(batch, sseOut{id: ev.ID, event: ev.Type, data: ev.Data})
				}
			}
			if !emit(batch) {
				return nil
			}
		}
		return nil
	}
}

// coordResume reports whether cursor is still within a coordinator stream's retained range (first..last); a stream
// that cannot resume starts with its snapshot.
func coordResume(ctx context.Context, cursor string, first, last func(context.Context) (string, error)) bool {
	if cursor == "" {
		return false
	}
	f, err := first(ctx)
	if err != nil || f == "" || coord.CompareIDs(cursor, f) < 0 {
		return false
	}
	l, err := last(ctx)
	return err == nil && coord.CompareIDs(cursor, l) <= 0
}

// openAgentStream opens an Agent's event stream (GET /agents/{agentId}/events).
func (s *Server) openAgentStream(ctx context.Context, id uuid.UUID, cursor string) (liveStream, error) {
	if _, err := s.Store.GetAgent(ctx, id); err != nil {
		return liveStream{}, err
	}
	agent := id.String()
	resume := coordResume(ctx, cursor,
		func(ctx context.Context) (string, error) { return s.Coord.FirstEventID(ctx, agent) },
		func(ctx context.Context) (string, error) {
			_, last, err := s.Coord.Snapshot(ctx, agent, "")
			return last, err
		})
	ls := liveStream{position: cursor, follow: s.coordFollow(func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
		return s.Coord.ReadEvents(ctx, agent, after, 100, block)
	}, nil)}
	if !resume {
		snap, at, err := s.Conv.Snapshot(ctx, id)
		if err != nil {
			return liveStream{}, err
		}
		data, _ := json.Marshal(snap)
		ls.first, ls.position = &sseOut{id: at, event: apitypes.EventSnapshot, data: data}, at
	}
	return ls, nil
}

// openOverviewStream opens the overview stream in the scope f (GET /overview/events).
func (s *Server) openOverviewStream(ctx context.Context, f store.ScopeFilter, cursor string) (liveStream, error) {
	resume := coordResume(ctx, cursor, s.Coord.OverviewFirstID, s.Coord.OverviewLastID)
	ls := liveStream{position: cursor, follow: s.coordFollow(s.readOverview, overviewKeep(f))}
	if !resume {
		snap, at, err := s.Conv.OverviewSnapshot(ctx, f)
		if err != nil {
			return liveStream{}, err
		}
		data, _ := json.Marshal(snap)
		ls.first, ls.position = &sseOut{id: at, event: apitypes.EventOverviewSnapshot, data: data}, at
	}
	return ls, nil
}

// openInboxStream opens the Inbox stream (GET /inbox/events).
func (s *Server) openInboxStream(ctx context.Context, cursor string) (liveStream, error) {
	resume := coordResume(ctx, cursor, s.Coord.InboxFirstID, s.Coord.InboxLastID)
	ls := liveStream{position: cursor, follow: s.coordFollow(func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
		return s.Coord.ReadInbox(ctx, after, 100, block)
	}, nil)}
	if !resume {
		snap, at, err := s.Inbox.Snapshot(ctx)
		if err != nil {
			return liveStream{}, err
		}
		data, _ := json.Marshal(snap)
		ls.first, ls.position = &sseOut{id: at, event: apitypes.EventInboxSnapshot, data: data}, at
	}
	return ls, nil
}

// openTaskStream opens a task's event stream (GET /tasks/{taskId}/events), or a project's task stream (GET
// /projects/{projectId}/tasks/events) when projectID is set. Task events are read from the database in order; the
// coordinator's relay only wakes the reader up.
func (s *Server) openTaskStream(ctx context.Context, p auth.Principal, taskID, projectID *uuid.UUID, cursor string) (liveStream, error) {
	if s.Tasks == nil {
		return liveStream{}, apperr.NotFound("Task service")
	}
	var snapshot any
	var position int64
	var err error
	if projectID != nil {
		if _, err = s.Store.GetProject(ctx, *projectID); err != nil {
			return liveStream{}, err
		}
		snapshot = map[string]any{"projectId": *projectID}
		position, err = s.Store.TaskEventPosition(ctx, nil, projectID)
	} else {
		if _, err = s.Tasks.Get(ctx, p, *taskID); err != nil {
			return liveStream{}, err
		}
		var detail taskmodel.Detail
		detail, position, err = s.Store.TaskSnapshot(ctx, *taskID)
		if err == nil {
			detail, err = s.Tasks.QuestionViews(ctx, p, detail)
		}
		snapshot = struct {
			Task taskmodel.Detail `json:"task"`
		}{Task: detail}
	}
	if err != nil {
		return liveStream{}, err
	}
	streamKey := ""
	if projectID != nil {
		streamKey = coord.ProjectTaskStream(projectID.String())
	} else {
		streamKey = coord.TaskStream(taskID.String())
	}
	relay := "0-0"
	if s.Coord != nil {
		if _, last, err := s.Coord.Snapshot(ctx, streamKey, ""); err == nil && last != "" {
			relay = last
		}
	}
	at := position
	resume := false
	if cursor != "" {
		if parsed, err := strconv.ParseInt(cursor, 10, 64); err == nil && parsed >= 0 && parsed <= position {
			at, resume = parsed, true
		}
	}
	ls := liveStream{position: strconv.FormatInt(at, 10)}
	if !resume {
		data, _ := json.Marshal(snapshot)
		ls.first = &sseOut{id: ls.position, event: "snapshot", data: data}
	}
	ls.follow = func(ctx context.Context, after string, emit func([]sseOut) bool) error {
		cursor, err := strconv.ParseInt(after, 10, 64)
		if err != nil {
			return fmt.Errorf("task stream position %q: %w", after, err)
		}
		interval := s.O.TaskEventPoll
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		poll := time.NewTicker(interval)
		defer poll.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-poll.C:
			}
			// The relay cursor is only used to consume wake-up notifications. The durable cursor must be advanced only
			// by ordered catch-up from the database; a new relay event must not skip past the reconnection backlog or
			// past history beyond a single batch of 100 events.
			if s.Coord != nil {
				if relayed, err := s.Coord.ReadEvents(ctx, streamKey, relay, 100, 0); err == nil && len(relayed) > 0 {
					relay = relayed[len(relayed)-1].ID
				}
			}
			events, err := s.Store.TaskEvents(ctx, taskID, projectID, cursor, 100)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			batch := make([]sseOut, 0, len(events))
			for _, event := range events {
				cursor = event.ID
				if projectID != nil && event.Type != "task.updated" {
					continue
				}
				data, err := s.taskEventView(ctx, p, event.Type, event.Data)
				if err != nil {
					return err
				}
				batch = append(batch, sseOut{id: strconv.FormatInt(cursor, 10), event: event.Type, data: data})
			}
			if !emit(batch) {
				return nil
			}
		}
	}
	return ls, nil
}

// serveStream serves one live stream on its own endpoint: the snapshot first (unless it resumes), then its events,
// with a keep-alive comment when nothing was written for the keep-alive interval.
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request, ls liveStream, logAttrs ...any) {
	ctx := r.Context()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if ls.first != nil && !writeEvent(w, ls.first.id, ls.first.event, ls.first.data) {
		return
	}
	if rc.Flush() != nil {
		return
	}
	keepalive := s.O.SSEKeepalive
	if keepalive <= 0 {
		keepalive = 15 * time.Second
	}
	lastWrite := time.Now()
	err := ls.follow(ctx, ls.position, func(batch []sseOut) bool {
		for _, ev := range batch {
			if !writeEvent(w, ev.id, ev.event, ev.data) {
				return false
			}
		}
		if len(batch) == 0 {
			if time.Since(lastWrite) < keepalive {
				return true
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return false
			}
		}
		if rc.Flush() != nil {
			return false
		}
		lastWrite = time.Now()
		return true
	})
	if err != nil && ctx.Err() == nil {
		s.Log.Warn("failed to read the event stream", append(logAttrs, "err", err)...)
	}
}
