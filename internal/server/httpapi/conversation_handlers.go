package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
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
			s.fail(w, r, apperr.Invalid("limit", "Must be an integer between 1 and 200."))
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
			s.fail(w, r, apperr.Invalid(p.name, "Must be a valid UUID."))
			return
		}
		*p.dst = &u
		set++
	}
	if set > 1 {
		s.fail(w, r, apperr.Invalid("before", "Give at most one of before, after and aroundRun."))
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
		s.fail(w, r, apperr.Invalid("text", "Must be a string. Send an empty string for an image-only message."))
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
			s.fail(w, r, apperr.Invalid("actionId", "A valid actionId is required."))
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
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ls, err := s.openAgentStream(r.Context(), id, r.Header.Get("Last-Event-ID"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.serveStream(w, r, ls, "agentId", id.String())
}

func writeEvent(w http.ResponseWriter, id, typ string, data []byte) bool {
	_, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, typ, data)
	return err == nil
}
