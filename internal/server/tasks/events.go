package tasks

import (
	"context"
	"encoding/json"
	"time"

	"loop-now/internal/server/coord"
)

// StreamEvent carries the persisted sequence number; the event stream's own ID is only used as the relay cursor.
type StreamEvent struct {
	ID   int64           `json:"id"`
	Data json.RawMessage `json:"data"`
}

// mirrorEvents relays in global commit order, so the project stream does not skip events because of the scheduling
// order of post-commit callbacks across tasks.
func (s *Service) mirrorEvents() {
	if s.Coord == nil || s.ctx.Err() != nil {
		return
	}
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	timeout := s.Options.RecordTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	for ctx.Err() == nil {
		events, e := s.Store.TaskEvents(ctx, nil, nil, s.eventPosition, 100)
		if e != nil {
			return
		}
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			data, e := json.Marshal(StreamEvent{ID: event.ID, Data: event.Data})
			if e != nil {
				return
			}
			if _, e = s.Coord.AppendEvent(ctx, coord.TaskStream(event.TaskID.String()), event.Type, data); e != nil {
				return
			}
			if event.Type == "task.updated" {
				if _, e = s.Coord.AppendEvent(ctx, coord.ProjectTaskStream(event.ProjectID.String()), event.Type, data); e != nil {
					return
				}
			}
			s.eventPosition = event.ID
		}
	}
}
