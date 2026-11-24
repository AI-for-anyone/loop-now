package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

// conversationPage mirrors the ConversationPage schema of the Agent API.
type conversationPage struct {
	Messages     []conversationEntry `json:"messages"`
	HasMore      bool                `json:"hasMore"`
	HasMoreAfter bool                `json:"hasMoreAfter"`
	Total        int                 `json:"total"`
}

// conversationEntry mirrors the ConversationEntry schema.
type conversationEntry struct {
	ID        uuid.UUID          `json:"id"`
	Role      string             `json:"role"`
	Origin    string             `json:"origin"`
	Text      string             `json:"text"`
	CreatedAt time.Time          `json:"createdAt"`
	RunID     uuid.UUID          `json:"runId"`
	RunStatus string             `json:"runStatus"`
	Images    int                `json:"images"`
	Tools     []conversationTool `json:"tools"`
}

// conversationTool mirrors the ConversationToolUse schema.
type conversationTool struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Failed int    `json:"failed"`
}

// maxConversationPage caps the page size of GET /conversation (PageLimit: larger values are capped).
const maxConversationPage = 100

// readConversation lets an Agent read its own current conversation (Agent API GET /conversation). A task instance's
// token has no console conversation and gets not_found.
func (s *Server) readConversation(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Kind != auth.KindAgent || p.TaskID != uuid.Nil {
		s.fail(w, r, apperr.NotFound("Conversation"))
		return
	}
	q := store.ConversationQuery{Limit: 20}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			s.fail(w, r, apperr.Invalid("limit", "Must be an integer of at least 1."))
			return
		}
		q.Limit = min(n, maxConversationPage)
	}
	for _, f := range []struct {
		name string
		dst  **uuid.UUID
	}{{"before", &q.Before}, {"after", &q.After}} {
		v := r.URL.Query().Get(f.name)
		if v == "" {
			continue
		}
		id, err := uuid.Parse(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid(f.name, "Must be a valid UUID."))
			return
		}
		*f.dst = &id
	}
	if q.Before != nil && q.After != nil {
		s.fail(w, r, apperr.Invalid("before", "Give at most one of before and after."))
		return
	}
	page, err := s.Store.ReadConversation(r.Context(), p.ID, q)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := conversationPage{Messages: make([]conversationEntry, 0, len(page.Entries)), HasMore: page.HasMore, HasMoreAfter: page.HasMoreAfter, Total: page.Total}
	for _, m := range page.Entries {
		tools := make([]conversationTool, 0, len(m.Tools))
		for _, t := range m.Tools {
			tools = append(tools, conversationTool{Name: t.Name, Calls: t.Calls, Failed: t.Failed})
		}
		out.Messages = append(out.Messages, conversationEntry{ID: m.ID, Role: m.Role, Origin: m.Origin, Text: m.Text, CreatedAt: m.CreatedAt,
			RunID: m.RunID, RunStatus: m.RunStatus, Images: m.Images, Tools: tools})
	}
	writeJSON(w, http.StatusOK, out)
}
