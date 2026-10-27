package tasks

import (
	"context"
	"encoding/json"
	"time"

	"loop-now/internal/server/coord"
)

// StreamEvent携带持久化序号，Redis stream自己的ID仅用于中继游标。
type StreamEvent struct {
	ID   int64           `json:"id"`
	Data json.RawMessage `json:"data"`
}

// mirrorEvents按全局提交顺序中继，项目流不会因跨任务的提交后回调调度顺序跳过事件。
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
