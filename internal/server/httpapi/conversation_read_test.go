package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

type conversationPage struct {
	Messages []struct {
		ID     uuid.UUID `json:"id"`
		Role   string    `json:"role"`
		Origin string    `json:"origin"`
		Text   string    `json:"text"`
		RunID  uuid.UUID `json:"runId"`
		Status string    `json:"runStatus"`
		Images int       `json:"images"`
		Tools  []struct {
			Name   string `json:"name"`
			Calls  int    `json:"calls"`
			Failed int    `json:"failed"`
		} `json:"tools"`
	} `json:"messages"`
	HasMore      bool `json:"hasMore"`
	HasMoreAfter bool `json:"hasMoreAfter"`
	Total        int  `json:"total"`
}

// An Agent reads its own conversation through the Agent API: only its own, paged, and validated against the contract;
// a task instance's token has no console conversation.
func TestAgentReadsItsOwnConversation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.conv.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	makeAgent := func(name string) apitypes.Agent {
		return decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": name, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	}
	own, other := makeAgent("reader"), makeAgent("neighbour")
	turn := func(agent uuid.UUID, text, reply string) {
		_, r, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: agent, RunID: uuid.New(), Text: text, Start: true, InstanceID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.AddToolEvent(ctx, r.ID, store.ToolEvent{ToolCallID: "c1", ToolName: "bash", State: "failed"}); err != nil {
			t.Fatal(err)
		}
		if _, _, ok, err := e.st.FinishRun(ctx, r.ID, store.RunEnd{Status: "completed", Text: reply}); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	turn(own.ID, "first", "one")
	turn(own.ID, "second", "two")
	turn(other.ID, "NOT_MINE", "NOT_MINE")

	reader := e.agentCaller(own.ID)
	var page conversationPage
	if err := json.Unmarshal(reader.expect(200, "GET", "/conversation?limit=3", nil), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 || page.Messages[0].Text != "one" || page.Messages[2].Text != "two" || !page.HasMore || page.HasMoreAfter || page.Total != 4 {
		t.Fatalf("wrong newest page: %+v", page)
	}
	if m := page.Messages[0]; m.Role != "assistant" || m.Origin != "console" || m.Status != "completed" || len(m.Tools) != 1 || m.Tools[0].Name != "bash" || m.Tools[0].Failed != 1 {
		t.Fatalf("wrong reply entry: %+v", m)
	}
	var older conversationPage
	if err := json.Unmarshal(reader.expect(200, "GET", "/conversation?before="+page.Messages[0].ID.String(), nil), &older); err != nil {
		t.Fatal(err)
	}
	if len(older.Messages) != 1 || older.Messages[0].Text != "first" || older.HasMore {
		t.Fatalf("wrong older page: %+v", older)
	}
	reader.expectError(404, "not_found", "GET", "/conversation?before="+uuid.NewString(), nil)
	reader.expectError(422, "validation_failed", "GET", "/conversation?before="+page.Messages[0].ID.String()+"&after="+page.Messages[0].ID.String(), nil)
	reader.expectError(422, "validation_failed", "GET", "/conversation?before=nope", nil)

	td, _, d := taskEnv(t)
	td.agentCaller(d.Nodes[0].RuntimeID).expectError(404, "not_found", "GET", "/conversation", nil)
}
