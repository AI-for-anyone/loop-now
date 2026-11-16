package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/inbox"
)

// Extension dialogs (plugins/SPEC.md §11): confirm, select and input become questions the Agent sends to the console,
// with the request's timeout as the deadline; the console's answer is handed back to the extension, and the answer
// message is marked read without notifying the Agent; expiry and the end of the turn are answered as a cancel, and the
// Agent gets no expiry notice; task turns and dialogs with more than 10 options do not ask.
func TestExtensionDialogQuestions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("Assistant")
	ask := func(ctx context.Context, d inbox.Dialog) chan inbox.DialogAnswer {
		d.AgentID, d.RunID = agent.ID, uuid.New()
		ch := make(chan inbox.DialogAnswer, 1)
		go func() {
			ans, err := e.api.Inbox.AskDialog(ctx, d)
			if err != nil {
				t.Errorf("AskDialog: %v", err)
			}
			ch <- ans
		}()
		return ch
	}
	question := func(request string) apitypes.InboxMessage {
		t.Helper()
		var id uuid.UUID
		deadline := time.Now().Add(10 * time.Second)
		for e.st.Pool.QueryRow(ctx, `SELECT question_message_id FROM extension_dialogs WHERE request_id=$1`, request).Scan(&id) != nil {
			if time.Now().After(deadline) {
				t.Fatalf("no question for %s", request)
			}
			time.Sleep(20 * time.Millisecond)
		}
		return decode[apitypes.InboxMessage](t, e.expect(200, "POST", "/inbox/messages/"+id.String()+"/read", map[string]any{}))
	}
	wait := func(ch chan inbox.DialogAnswer) inbox.DialogAnswer {
		t.Helper()
		select {
		case ans := <-ch:
			return ans
		case <-time.After(15 * time.Second):
			t.Fatal("timed out waiting for an answer")
			return inbox.DialogAnswer{}
		}
	}

	confirm := ask(ctx, inbox.Dialog{RequestID: "c", Method: "confirm", Title: "Continue deploying?", Message: "This overwrites production", Timeout: time.Minute})
	q := question("c")
	if q.From.Kind != "agent" || *q.From.ID != agent.ID || q.To.Kind != "console" || q.Text != "Continue deploying?\nThis overwrites production" || q.Question == nil ||
		len(q.Question.Options) != 2 || q.Question.Options[0].Label != "Yes" || time.Until(q.Question.ExpiresAt) > time.Minute || time.Until(q.Question.ExpiresAt) < 50*time.Second {
		t.Fatalf("confirm question: %+v %+v", q, q.Question)
	}
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "2"}))
	if ans := wait(confirm); ans.Cancelled || ans.Confirmed || ans.Outcome != "answered" || *ans.QuestionID != q.ID {
		t.Fatalf("answer \"No\": %+v", ans)
	}
	if m, err := e.st.GetInboxMessage(ctx, answer.ID, 200); err != nil || m.ReadAt == nil {
		t.Fatalf("the answer message should be marked read: %+v %v", m.ReadAt, err)
	}

	pick := ask(ctx, inbox.Dialog{RequestID: "s", Method: "select", Title: "Environment", Options: []string{"staging", "production"}})
	q = question("s")
	if len(q.Question.Options) != 2 || q.Question.Options[1].Label != "production" || time.Until(q.Question.ExpiresAt) < 29*time.Minute {
		t.Fatalf("select question (30 minutes without a timeout): %+v", q.Question)
	}
	e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "2"})
	if ans := wait(pick); ans.Cancelled || ans.Value != "production" {
		t.Fatalf("select: %+v", ans)
	}

	input := ask(ctx, inbox.Dialog{RequestID: "i", Method: "input", Title: "Version", Placeholder: "e.g. 1.2.0"})
	q = question("i")
	if q.Text != "Version\nReply with text to answer (e.g. 1.2.0)" || q.Question.Options[0].Label != "Cancel" {
		t.Fatalf("input question: %q %+v", q.Text, q.Question)
	}
	e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "1.4.0"})
	if ans := wait(input); ans.Cancelled || ans.Value != "1.4.0" {
		t.Fatalf("input: %+v", ans)
	}

	expiring := ask(ctx, inbox.Dialog{RequestID: "x", Method: "confirm", Title: "Expires soon", Timeout: time.Second})
	q = question("x")
	if ans := wait(expiring); !ans.Cancelled || ans.Outcome != "expired" {
		t.Fatalf("expiry: %+v", ans)
	}
	runCtx, endRun := context.WithCancel(ctx)
	ended := ask(runCtx, inbox.Dialog{RequestID: "r", Method: "confirm", Title: "This turn ends"})
	question("r")
	endRun()
	if ans := wait(ended); !ans.Cancelled || ans.Outcome != "expired" {
		t.Fatalf("this turn ends: %+v", ans)
	}
	var reason string
	if err := e.st.Pool.QueryRow(ctx, `SELECT q.state||'/'||q.expired_reason FROM inbox_questions q JOIN extension_dialogs d ON d.question_message_id=q.message_id WHERE d.request_id='r'`).Scan(&reason); err != nil || reason != "expired/run_ended" {
		t.Fatalf("the question should expire when this turn ends: %q %v", reason, err)
	}
	if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	var toAgent int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE to_kind='agent' AND to_agent_id=$1 AND (read_at IS NULL OR from_kind='platform')`, agent.ID).Scan(&toAgent); err != nil || toAgent != 0 {
		t.Fatalf("neither dialog answers nor expiry should remind the Agent: %d %v", toAgent, err)
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("an answer should not start a new turn: %+v", page.Messages)
	}

	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = string(rune('a' + i))
	}
	if ans := wait(ask(ctx, inbox.Dialog{RequestID: "m", Method: "select", Title: "Too many", Options: tooMany})); !ans.Cancelled || ans.QuestionID != nil {
		t.Fatalf("more than 10 options should not ask: %+v", ans)
	}
	if ans, err := e.api.Inbox.AskDialog(ctx, inbox.Dialog{AgentID: uuid.New(), RunID: uuid.New(), RequestID: "t", Method: "confirm", Title: "Task turn"}); err != nil || !ans.Cancelled || ans.QuestionID != nil {
		t.Fatalf("task turns should not ask: %+v %v", ans, err)
	}
}
