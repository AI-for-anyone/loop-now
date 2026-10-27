package httpapi_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

// Messages between the console and agents are shown inline: task attribution
// on every message, the node chat, the per-agent list and the pending summary.
func TestInboxInlineAttributionNodeChatConversationAndPending(t *testing.T) {
	e, manager, d := taskEnv(t)
	ctx := context.Background()
	e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
	node := d.Nodes[0]
	project := e.dc.Project.ID
	instance := e.agentCaller(node.RuntimeID)

	early := sendInbox(instance, "@console", "还没有轮次")
	if early.Task == nil || early.Task.TaskID != d.Task.ID || early.Task.RuntimeID != node.RuntimeID || early.Task.NodeRunID != nil {
		t.Fatalf("send before any turn: %+v", early.Task)
	}
	if _, err := e.st.Pool.Exec(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,text,started_at,ended_at)
		VALUES($1,$2,$3,$4,$5,$6,'completed','task_node','',clock_timestamp(),clock_timestamp())`,
		uuid.New(), d.Task.ID, node.RuntimeID, node.ID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	plain := sendInbox(instance, "@console", "进展")
	if plain.Task == nil || plain.Task.NodeRunID == nil || *plain.Task.NodeRunID != node.ID {
		t.Fatalf("send after a node turn: %+v", plain.Task)
	}
	q := decode[apitypes.InboxMessage](t, instance.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "继续吗", "options": []string{"继续", "停止"}, "nodeRunId": node.ID}))
	if q.Task == nil || q.Task.NodeRunID == nil || *q.Task.NodeRunID != node.ID {
		t.Fatalf("question attribution: %+v", q.Task)
	}
	normal := sendInbox(manager, "@console", "常态消息")
	if normal.Task != nil {
		t.Fatal("normal agent message attributed to a task")
	}
	base, err := e.st.GetAgent(ctx, node.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	addr, _ := base.Address()
	toTask := sendInbox(e.agentCaller(e.platformAgent("旁观者").ID), addr+"#"+d.Task.ID.String(), "给任务实例")
	if toTask.Task == nil || toTask.Task.RuntimeID != node.RuntimeID || toTask.Task.NodeRunID == nil || *toTask.Task.NodeRunID != node.ID {
		t.Fatalf("message to a task instance: %+v", toTask.Task)
	}

	p := decode[apitypes.InboxPending](t, e.expect(200, "GET", "/inbox/pending", nil))
	if len(p.Questions) != 1 || p.Questions[0].Message.ID != q.ID || p.Questions[0].ProjectID == nil || *p.Questions[0].ProjectID != project {
		t.Fatalf("pending questions: %+v", p.Questions)
	}
	if ids := pendingIDs(p.Unread); !equalIDs(ids, normal.ID, plain.ID, early.ID) {
		t.Fatalf("pending unread must exclude open questions, newest first: %v", ids)
	}
	if len(p.ByTask) != 1 || p.ByTask[0].TaskID != d.Task.ID || p.ByTask[0].Questions != 1 || p.ByTask[0].Unread != 2 || p.ByTask[0].ProjectID == nil || *p.ByTask[0].ProjectID != project {
		t.Fatalf("byTask: %+v", p.ByTask)
	}
	if len(p.ByAgent) != 1 || p.ByAgent[0].AgentID != node.AgentID || p.ByAgent[0].Questions != 0 || p.ByAgent[0].Unread != 1 {
		t.Fatalf("byAgent: %+v", p.ByAgent)
	}

	events, err := e.api.Inbox.Co.ReadInbox(ctx, "0-0", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1].ID
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "继续，按原计划"}))
	if answer.AnswerTo == nil || answer.AnswerTo.MessageID != q.ID || answer.Task == nil || answer.Task.NodeRunID == nil || *answer.Task.NodeRunID != node.ID {
		t.Fatalf("text answer: %+v %+v", answer.AnswerTo, answer.Task)
	}
	original, err := e.st.GetInboxMessage(ctx, q.ID, 200)
	if err != nil || original.Question.State != "answered" || original.Question.Answer == nil || original.Question.Answer.OptionID != nil || original.ReadAt == nil {
		t.Fatalf("text answer must answer and read the question: %+v %v", original.Question, err)
	}
	if !inboxEventAfter(t, e, last, apitypes.EventInboxRead, q.ID) {
		t.Fatal("answering did not emit inbox.message.read")
	}
	p = decode[apitypes.InboxPending](t, e.expect(200, "GET", "/inbox/pending", nil))
	if len(p.Questions) != 0 || !equalIDs(pendingIDs(p.Unread), normal.ID, plain.ID, early.ID) {
		t.Fatalf("answered question still pending: %+v", p)
	}

	// The node chat lists the runtime's console messages attributed to this
	// node, oldest first; the early send has no node and stays out.
	nodePath := "/tasks/" + d.Task.ID.String() + "/nodes/" + node.ID.String() + "/inbox-messages"
	page := decode[taskmodel.TaskNodeInboxPage](t, e.expect(200, "GET", nodePath, nil))
	var got []uuid.UUID
	for _, it := range page.Items {
		var m apitypes.InboxMessage
		if err := json.Unmarshal(it.Message, &m); err != nil {
			t.Fatal(err)
		}
		got = append(got, m.ID)
		if it.TaskID != d.Task.ID || it.NodeRunID != node.ID || it.RuntimeID != node.RuntimeID || it.AgentID != node.AgentID || len(it.RunLinks) != 0 || it.NotExecuted != nil {
			t.Fatalf("node record: %+v", it)
		}
		if m.ID == q.ID && (m.Question == nil || m.Question.State != "answered") {
			t.Fatal("node chat question lost its state")
		}
	}
	if !equalIDs(got, plain.ID, q.ID, answer.ID) {
		t.Fatalf("node chat messages: %v", got)
	}
	first := decode[taskmodel.TaskNodeInboxPage](t, e.expect(200, "GET", nodePath+"?limit=2", nil))
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal("node chat first page")
	}
	rest := decode[taskmodel.TaskNodeInboxPage](t, e.expect(200, "GET", nodePath+"?limit=2&cursor="+*first.NextCursor, nil))
	if len(rest.Items) != 1 || rest.NextCursor != nil {
		t.Fatal("node chat second page")
	}

	// The agent's own conversation with the console: both directions, no task
	// instance messages, newest first, paged.
	toAgent := sendInbox(e.console(), node.AgentID.String(), "控制台发给常态Agent")
	convPath := "/agents/" + node.AgentID.String() + "/console-messages"
	conv := decode[apitypes.InboxConversationPage](t, e.expect(200, "GET", convPath+"?limit=1", nil))
	if len(conv.Messages) != 1 || conv.Messages[0].ID != toAgent.ID || conv.Messages[0].Text != "控制台发给常态Agent" || conv.NextCursor == nil {
		t.Fatalf("conversation first page: %+v", conv)
	}
	conv = decode[apitypes.InboxConversationPage](t, e.expect(200, "GET", convPath+"?limit=1&cursor="+*conv.NextCursor, nil))
	if len(conv.Messages) != 1 || conv.Messages[0].ID != normal.ID || conv.NextCursor != nil {
		t.Fatalf("conversation second page: %+v", conv)
	}
	e.console().expectError(404, "not_found", "GET", "/agents/"+uuid.NewString()+"/console-messages", nil)
	_, secret := e.identity("inline-external")
	e.external(secret).expectError(403, "forbidden", "GET", "/inbox/pending", nil)
}

// The identity page talks to an external identity with the generic send and
// reply endpoints; nothing depends on the console inbox page.
func TestConsoleIdentityConversationWithoutInboxPage(t *testing.T) {
	e := newEnv(t)
	x, secret := e.identity("page-less")
	ext := e.external(secret)
	sent := sendInbox(e.console(), "@page-less", "你好")
	if sent.To.Kind != "external" || sent.To.ID == nil || *sent.To.ID != x.ID || sent.WhenBusy != nil || sent.Task != nil {
		t.Fatalf("console send to identity: %+v", sent)
	}
	got := decode[apitypes.InboxMessage](t, ext.expect(200, "POST", "/inbox/messages/"+sent.ID.String()+"/read", map[string]any{}))
	if got.ReadAt == nil {
		t.Fatal("identity read")
	}
	back := decode[apitypes.InboxMessage](t, ext.expect(201, "POST", "/inbox/messages/"+sent.ID.String()+"/reply", map[string]any{"text": "收到"}))
	again := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+back.ID.String()+"/reply", map[string]any{"text": "好的"}))
	if back.To.Kind != "console" || again.To.Kind != "external" || again.ThreadID != sent.ID {
		t.Fatal("reply routing between console and identity")
	}
	p := decode[apitypes.InboxPending](t, e.expect(200, "GET", "/inbox/pending", nil))
	if !equalIDs(pendingIDs(p.Unread), back.ID) || p.Unread[0].ProjectID != nil || len(p.ByAgent)+len(p.ByTask) != 0 {
		t.Fatalf("identity message pending: %+v", p)
	}
	page := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/external-identities/"+x.ID.String()+"/inbox", nil))
	thread := decode[apitypes.InboxThread](t, e.expect(200, "GET", "/inbox/threads/"+sent.ID.String(), nil))
	if len(page.Messages) != 3 || len(thread.Messages) != 3 {
		t.Fatalf("identity history: %d %d", len(page.Messages), len(thread.Messages))
	}
}

func pendingIDs(items []apitypes.InboxPendingItem) []uuid.UUID {
	out := []uuid.UUID{}
	for _, it := range items {
		out = append(out, it.Message.ID)
	}
	return out
}

func equalIDs(got []uuid.UUID, want ...uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// inboxEventAfter reports whether the inbox stream has an event of typ for
// message id after the given stream position.
func inboxEventAfter(t *testing.T, e *env, after, typ string, id uuid.UUID) bool {
	t.Helper()
	events, err := e.api.Inbox.Co.ReadInbox(context.Background(), after, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		var body struct {
			ID uuid.UUID `json:"id"`
		}
		if ev.Type == typ && json.Unmarshal(ev.Data, &body) == nil && body.ID == id {
			return true
		}
	}
	return false
}
