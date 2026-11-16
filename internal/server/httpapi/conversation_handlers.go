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
	"loop-now/internal/server/coord"
	"loop-now/internal/server/store"
)

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			s.fail(w, r, apperr.Invalid("limit", "必须是 1 到 200 之间的整数"))
			return
		}
		limit = n
	}
	q := store.MessageQuery{Limit: limit}
	set := 0
	for _, p := range []struct {
		name string
		dst  **uuid.UUID
	}{{"before", &q.Before}, {"after", &q.After}, {"aroundRun", &q.AroundRun}} {
		v := r.URL.Query().Get(p.name)
		if v == "" {
			continue
		}
		u, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid(p.name, "不是合法的 UUID"))
			return
		}
		*p.dst = &u
		set++
	}
	if set > 1 {
		s.fail(w, r, apperr.Invalid("before", "before、after、aroundRun 最多只能传一个"))
		return
	}
	page, err := s.Conv.History(r.Context(), id, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Text     *string         `json:"text"`
		ImageIDs json.RawMessage `json:"imageIds"`
		WhenBusy string          `json:"whenBusy"`
	}
	if err := readJSON(w, r, s.O.Limits.MaxMessage*4+1024, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Text == nil {
		s.fail(w, r, apperr.Invalid("text", "正文须为字符串，纯图片时传空字符串"))
		return
	}
	ids, imageErr := s.imageIDs(in.ImageIDs)
	if imageErr != nil {
		s.fail(w, r, imageErr)
		return
	}
	if len(ids) > 0 {
		if imageErr = s.imageModel(r.Context(), store.ImageOwner{AgentID: id}); imageErr != nil {
			s.fail(w, r, imageErr)
			return
		}
	}
	project := s.agentProject(r, id)
	if len(ids) == 0 && s.consoleCommand(w, r, id, project, *in.Text) {
		return
	}
	text, err := s.modifyText(r, "message.before_enqueue", project, &id, nil, *in.Text, map[string]any{"from": party(principal(r)), "imageCount": len(ids)})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msg, err := s.Conv.Send(r.Context(), id, text, in.WhenBusy, ids...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, apitypes.SendMessageResult{Message: msg, Run: msg.Run})
}

// getAgentQueue returns the queue list of the regular conversation, item by item, in actual processing order.
func (s *Server) getAgentQueue(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Conv.Queue(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// queueAction handles a queue action of the regular conversation: the Agent and the subject (a run or an Inbox message)
// come from the path, the body is {actionId}.
func (s *Server) queueAction(param string, do func(ctx context.Context, agentID, subject, actionID uuid.UUID) (json.RawMessage, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathUUID(r, "agentId")
		if err != nil {
			s.fail(w, r, err)
			return
		}
		subject, err := pathUUID(r, param)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in apitypes.AgentQueueActionInput
		if err = readJSON(w, r, 4096, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		if in.ActionID == uuid.Nil {
			s.fail(w, r, apperr.Invalid("actionId", "必须提供有效动作ID"))
			return
		}
		out, err := do(r.Context(), id, subject, in.ActionID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) getAgentStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	st, err := s.Conv.Status(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) resetSession(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sess, err := s.Conv.ResetSession(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// streamEvents pushes the SSE event stream. Without Last-Event-ID, or when it is outside the retained range, a snapshot
// is pushed first; the snapshot's event ID is the latest position of the event stream at that moment; with a
// Last-Event-ID still inside the retained range, no snapshot is pushed and the stream resumes right after it.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.Store.GetAgent(ctx, id); err != nil {
		s.fail(w, r, err)
		return
	}
	agent := id.String()
	cursor, resume := r.Header.Get("Last-Event-ID"), false
	if cursor != "" {
		first, err := s.Coord.FirstEventID(ctx, agent)
		if err == nil && first != "" && coord.CompareIDs(cursor, first) >= 0 {
			_, last, err := s.Coord.Snapshot(ctx, agent, "")
			resume = err == nil && coord.CompareIDs(cursor, last) <= 0
		}
	}
	var snap apitypes.SseSnapshot
	if !resume {
		snap, cursor, err = s.Conv.Snapshot(ctx, id)
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
	rc := http.NewResponseController(w)
	if !resume {
		data, _ := json.Marshal(snap)
		if !writeEvent(w, cursor, apitypes.EventSnapshot, data) {
			return
		}
	}
	_ = rc.Flush()
	s.pumpEvents(w, r, cursor, func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
		return s.Coord.ReadEvents(ctx, agent, after, 100, block)
	}, "agentId", agent)
}

// pumpEvents keeps reading the event stream after cursor and pushing it, with periodic keepalives, until the connection
// or the server closes.
func (s *Server) pumpEvents(w http.ResponseWriter, r *http.Request, cursor string,
	read func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error), logAttrs ...any) {
	s.pumpEventsFiltered(w, r, cursor, read, nil, logAttrs...)
}

// pumpEventsFiltered is like pumpEvents; when keep is not nil only the events it keeps are written, and filtered-out
// events still advance the cursor so they are not read again and again.
func (s *Server) pumpEventsFiltered(w http.ResponseWriter, r *http.Request, cursor string,
	read func(ctx context.Context, after string, block time.Duration) ([]coord.Event, error), keep func(coord.Event) bool, logAttrs ...any) {
	ctx := r.Context()
	rc := http.NewResponseController(w)
	// Block at most 1 second per read and decide on keepalives by elapsed time, so the loop exits promptly when the
	// connection or the server closes.
	block := min(s.O.SSEKeepalive, time.Second)
	lastWrite := time.Now()
	for ctx.Err() == nil {
		events, err := read(ctx, cursor, block)
		if err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("读取事件流失败", append(logAttrs, "err", err)...)
			}
			return
		}
		wrote := false
		for _, ev := range events {
			cursor = ev.ID
			if keep != nil && !keep(ev) {
				continue
			}
			if !writeEvent(w, ev.ID, ev.Type, ev.Data) {
				return
			}
			wrote = true
		}
		if !wrote {
			if time.Since(lastWrite) < s.O.SSEKeepalive {
				continue
			}
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
		lastWrite = time.Now()
	}
}

func writeEvent(w http.ResponseWriter, id, typ string, data []byte) bool {
	_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, typ, data)
	return err == nil
}
