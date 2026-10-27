package httpapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/inbox"
)

// 扩展对话（plugins/SPEC.md §11）：confirm、select、input 成为 Agent 发给控制台的提问，期限为请求的超时；
// 控制台的回答交还给扩展，回答消息记为已读、不提醒 Agent；到期与这一轮结束按取消回应，不给 Agent 发到期通知；
// 任务轮次与选项超过 10 个的对话不提问。
func TestExtensionDialogQuestions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	agent, path := e.newAgent("助手")
	ask := func(ctx context.Context, d inbox.Dialog) chan inbox.DialogAnswer {
		d.AgentID, d.RunID = agent.ID, uuid.New()
		ch := make(chan inbox.DialogAnswer, 1)
		go func() {
			ans, err := e.api.Inbox.AskDialog(ctx, d)
			if err != nil {
				t.Errorf("AskDialog：%v", err)
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
				t.Fatalf("没有提问 %s", request)
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
			t.Fatal("等待回答超时")
			return inbox.DialogAnswer{}
		}
	}

	confirm := ask(ctx, inbox.Dialog{RequestID: "c", Method: "confirm", Title: "继续部署？", Message: "会覆盖线上", Timeout: time.Minute})
	q := question("c")
	if q.From.Kind != "agent" || *q.From.ID != agent.ID || q.To.Kind != "console" || q.Text != "继续部署？\n会覆盖线上" || q.Question == nil ||
		len(q.Question.Options) != 2 || q.Question.Options[0].Label != "是" || time.Until(q.Question.ExpiresAt) > time.Minute || time.Until(q.Question.ExpiresAt) < 50*time.Second {
		t.Fatalf("确认提问：%+v %+v", q, q.Question)
	}
	answer := decode[apitypes.InboxMessage](t, e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "2"}))
	if ans := wait(confirm); ans.Cancelled || ans.Confirmed || ans.Outcome != "answered" || *ans.QuestionID != q.ID {
		t.Fatalf("回答“否”：%+v", ans)
	}
	if m, err := e.st.GetInboxMessage(ctx, answer.ID, 200); err != nil || m.ReadAt == nil {
		t.Fatalf("回答消息应记为已读：%+v %v", m.ReadAt, err)
	}

	pick := ask(ctx, inbox.Dialog{RequestID: "s", Method: "select", Title: "环境", Options: []string{"staging", "production"}})
	q = question("s")
	if len(q.Question.Options) != 2 || q.Question.Options[1].Label != "production" || time.Until(q.Question.ExpiresAt) < 29*time.Minute {
		t.Fatalf("选择提问（没有超时时 30 分钟）：%+v", q.Question)
	}
	e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "2"})
	if ans := wait(pick); ans.Cancelled || ans.Value != "production" {
		t.Fatalf("选择：%+v", ans)
	}

	input := ask(ctx, inbox.Dialog{RequestID: "i", Method: "input", Title: "版本号", Placeholder: "例如 1.2.0"})
	q = question("i")
	if q.Text != "版本号\n直接回复文字作答（例如 1.2.0）" || q.Question.Options[0].Label != "取消" {
		t.Fatalf("输入提问：%q %+v", q.Text, q.Question)
	}
	e.expect(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"text": "1.4.0"})
	if ans := wait(input); ans.Cancelled || ans.Value != "1.4.0" {
		t.Fatalf("输入：%+v", ans)
	}

	expiring := ask(ctx, inbox.Dialog{RequestID: "x", Method: "confirm", Title: "很快到期", Timeout: time.Second})
	q = question("x")
	if ans := wait(expiring); !ans.Cancelled || ans.Outcome != "expired" {
		t.Fatalf("到期：%+v", ans)
	}
	runCtx, endRun := context.WithCancel(ctx)
	ended := ask(runCtx, inbox.Dialog{RequestID: "r", Method: "confirm", Title: "这一轮结束"})
	question("r")
	endRun()
	if ans := wait(ended); !ans.Cancelled || ans.Outcome != "expired" {
		t.Fatalf("这一轮结束：%+v", ans)
	}
	var reason string
	if err := e.st.Pool.QueryRow(ctx, `SELECT q.state||'/'||q.expired_reason FROM inbox_questions q JOIN extension_dialogs d ON d.question_message_id=q.message_id WHERE d.request_id='r'`).Scan(&reason); err != nil || reason != "expired/run_ended" {
		t.Fatalf("这一轮结束时提问到期：%q %v", reason, err)
	}
	if err := e.api.Inbox.ReconcileQuestions(ctx); err != nil {
		t.Fatal(err)
	}
	var toAgent int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE to_kind='agent' AND to_agent_id=$1 AND (read_at IS NULL OR from_kind='platform')`, agent.ID).Scan(&toAgent); err != nil || toAgent != 0 {
		t.Fatalf("对话的回答与到期都不应提醒 Agent：%d %v", toAgent, err)
	}
	if page := decode[apitypes.MessagePage](t, e.expect(200, "GET", path+"/messages", nil)); len(page.Messages) != 0 {
		t.Fatalf("不应为回答开启新的一轮：%+v", page.Messages)
	}

	tooMany := make([]string, 11)
	for i := range tooMany {
		tooMany[i] = string(rune('a' + i))
	}
	if ans := wait(ask(ctx, inbox.Dialog{RequestID: "m", Method: "select", Title: "太多", Options: tooMany})); !ans.Cancelled || ans.QuestionID != nil {
		t.Fatalf("选项超过 10 个不提问：%+v", ans)
	}
	if ans, err := e.api.Inbox.AskDialog(ctx, inbox.Dialog{AgentID: uuid.New(), RunID: uuid.New(), RequestID: "t", Method: "confirm", Title: "任务轮次"}); err != nil || !ans.Cancelled || ans.QuestionID != nil {
		t.Fatalf("任务轮次不提问：%+v %v", ans, err)
	}
}
