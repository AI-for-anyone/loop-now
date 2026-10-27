package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

// agentCall sends one request to the Agent API with a freshly issued token of agentID.
func (s *server) agentCall(agentID uuid.UUID, method, path string, body any) (int, []byte) {
	s.t.Helper()
	token, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		s.t.Fatal(err)
	}
	if err = s.a.Store.InsertAgentToken(context.Background(), token.ID, agentID, s.a.InstanceID, token.Hash); err != nil {
		s.t.Fatal(err)
	}
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, strings.TrimSuffix(s.url, "/api/v1")+"/agent/v1"+path, r)
	req.Header.Set("Authorization", "Bearer "+token.Plain)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func queueKinds(items []apitypes.QueueItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Kind+":"+it.Text+":"+it.WhenBusy)
	}
	return out
}

// 普通对话的排队重做，真实 App + fakePi：逐条列表与快照、取消（收件 Agent 只看到取消说明、发件方与控制台看到全文）、
// 逐条转为打断、停止当前回复后队列继续。
func TestQueueActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	path, receiver := s.createAgent("Receiver")
	_, sender := s.createAgent("Sender")
	send := func(text, whenBusy string) apitypes.Run {
		body := map[string]any{"text": text}
		if whenBusy != "" {
			body["whenBusy"] = whenBusy
		}
		return decode[apitypes.SendMessageResult](t, s.must(202, "POST", path+"/messages", body)).Run
	}
	first := send("!sleep=30s !noabort !text=first", "")
	waitFor(t, "第一条进入运行", func() bool {
		r, e := s.a.Store.GetRun(ctx, first.ID)
		return e == nil && r.Status == "running"
	})
	mine := send("!text=mine", "queue")
	inbox := func(text string) uuid.UUID {
		code, raw := s.agentCall(sender.ID, "POST", "/inbox/messages", map[string]any{"to": "默认项目/Receiver", "text": text, "whenBusy": "queue"})
		if code != 201 {
			t.Fatalf("Sender 发信：%d %s", code, raw)
		}
		return decode[apitypes.InboxMessage](t, raw).ID
	}
	m1, m2 := inbox("请看构建结果"), inbox("第二条留言")
	q := decode[apitypes.AgentQueue](t, s.must(200, "GET", path+"/queue", nil))
	want := []string{"run:!text=mine:queue", "inbox:请看构建结果:queue", "inbox:第二条留言:queue"}
	if !slices.Equal(queueKinds(q.Items), want) || q.Items[1].From.Kind != "agent" || q.Items[1].From.ID == nil || *q.Items[1].From.ID != sender.ID || q.Items[0].From.Kind != "console" {
		t.Fatalf("排队列表：%+v", q.Items)
	}
	var snap apitypes.SseSnapshot
	firstSSESnapshot(t, s.url+path+"/events", &snap)
	if !slices.Equal(queueKinds(snap.QueueItems), want) {
		t.Fatalf("SSE 快照的逐条列表：%+v", snap.QueueItems)
	}

	// 取消别人发来的一条：收件 Agent 只看到取消说明，发件方与控制台看到全文；Inbox 事件流推送取消。
	r := decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/inbox-messages/"+m2.String()+"/cancel", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Item == nil || r.Item.Text != "第二条留言" || r.Operation != "cancel_inbox_message" {
		t.Fatalf("取消回执：%+v", r)
	}
	code, raw := s.agentCall(receiver.ID, "POST", "/inbox/messages/"+m2.String()+"/read", map[string]any{})
	got := decode[apitypes.InboxMessage](t, raw)
	if code != 200 || got.Text != store.CancelledNotice || got.CancelledAt == nil || got.ReadAt != nil || strings.Contains(string(raw), "第二条留言") {
		t.Fatalf("收件 Agent 读取已取消的消息：%d %s", code, raw)
	}
	code, raw = s.agentCall(receiver.ID, "GET", "/inbox/messages?unread=true", nil)
	if code != 200 || strings.Contains(string(raw), m2.String()) || !strings.Contains(string(raw), m1.String()) {
		t.Fatalf("已取消的消息不计为未读：%d %s", code, raw)
	}
	code, raw = s.agentCall(sender.ID, "GET", "/inbox/threads/"+m2.String(), nil)
	if code != 200 || !strings.Contains(string(raw), "第二条留言") || !strings.Contains(string(raw), `"cancelledAt":"`) {
		t.Fatalf("发件方看到全文与取消时间：%d %s", code, raw)
	}
	if console := decode[apitypes.InboxMessage](t, s.must(200, "GET", path+"/inbox/"+m2.String(), nil)); console.Text != "第二条留言" || console.CancelledAt == nil {
		t.Fatalf("控制台看到全文：%+v", console)
	}
	events, err := s.a.Coord.ReadInbox(ctx, "0", 1000, 0)
	if err != nil || !slices.ContainsFunc(events, func(e coord.Event) bool {
		return e.Type == apitypes.EventInboxCancelled && strings.Contains(string(e.Data), m2.String())
	}) {
		t.Fatalf("应推送 inbox.message.cancelled：%v", err)
	}

	// 逐条转为打断：别人发来的消息先于自己排队的消息处理，当前回复被打断。
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/inbox-messages/"+m1.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Run == nil || r.Run.WhenBusy == nil || *r.Run.WhenBusy != "interrupt" || !slices.Equal(queueKinds(r.Queue), []string{"inbox:请看构建结果:interrupt", "run:!text=mine:queue"}) {
		t.Fatalf("转换回执：%+v", r)
	}
	promoted := r.Run.ID
	waitFor(t, "全部执行结束", func() bool {
		for _, id := range []uuid.UUID{first.ID, promoted, mine.ID} {
			if v, e := s.a.Store.GetRun(ctx, id); e != nil || v.EndedAt == nil {
				return false
			}
		}
		return true
	})
	a, p, m := runOf(t, s, first.ID), runOf(t, s, promoted), runOf(t, s, mine.ID)
	if a.EndReason != "user_interrupt" || p.Status != "completed" || m.Status != "completed" || !p.StartedAt.Before(*m.StartedAt) {
		t.Fatalf("转换的消息应先于排队的控制台消息：first=%s promoted=%s@%v mine=%s@%v", a.EndReason, p.Status, p.StartedAt, m.Status, m.StartedAt)
	}
	var prompt string
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT text FROM conversation_messages WHERE id=$1`, p.UserMessageID).Scan(&prompt); err != nil || !strings.Contains(prompt, m1.String()) || strings.Contains(prompt, m2.String()) {
		t.Fatalf("打断提醒应只点名转换的消息：%q %v", prompt, err)
	}
	if q := decode[apitypes.AgentQueue](t, s.must(200, "GET", path+"/queue", nil)); len(q.Items) != 0 {
		t.Fatalf("排队应已清空：%+v", q.Items)
	}

	// 停止当前回复：以 user_stopped 结束，之后排队的消息照常处理；已结束的回复不能再停止。
	long := send("!sleep=30s !noabort !text=long", "")
	waitFor(t, "长回复进入运行", func() bool {
		v, e := s.a.Store.GetRun(ctx, long.ID)
		return e == nil && v.Status == "running"
	})
	after := send("!text=after", "queue")
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/runs/"+long.ID.String()+"/stop", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Run.ID != long.ID {
		t.Fatalf("停止回执：%+v", r)
	}
	waitFor(t, "停止后队列继续", func() bool {
		v, e := s.a.Store.GetRun(ctx, after.ID)
		return e == nil && v.Status == "completed"
	})
	if v := runOf(t, s, long.ID); v.Status != "interrupted" || v.EndReason != "user_stopped" {
		t.Fatalf("被停止的回复：%s %s", v.Status, v.EndReason)
	}
	if code, body := s.call("POST", path+"/runs/"+long.ID.String()+"/stop", map[string]any{"actionId": uuid.New()}); code != 409 || !strings.Contains(string(body), "run_not_active") {
		t.Fatalf("已结束的回复应 409 run_not_active：%d %s", code, body)
	}
	tl := s.must(200, "GET", path+"/runs/"+long.ID.String()+"/timeline", nil)
	if !bytes.Contains(tl, []byte(`"stop":true`)) || !bytes.Contains(tl, []byte(`"user_stopped"`)) {
		t.Fatalf("时间线应记下停止：%s", tl)
	}
	// 撤回自己排队的消息：Agent 不会收到，从队列移除。
	busy := send("!sleep=30s !noabort !text=busy", "")
	waitFor(t, "再次进入运行", func() bool {
		v, e := s.a.Store.GetRun(ctx, busy.ID)
		return e == nil && v.Status == "running"
	})
	withdraw := send("!text=withdraw", "queue")
	r = decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/runs/"+withdraw.ID.String()+"/cancel", map[string]any{"actionId": uuid.New()}))
	if r.Status != "accepted" || r.Item.Text != "!text=withdraw" || r.Run.EndReason == nil || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("撤回回执：%+v", r)
	}
	s.must(200, "POST", path+"/runs/"+busy.ID.String()+"/stop", map[string]any{"actionId": uuid.New()})
	s.waitIdle(path)
	if v := runOf(t, s, withdraw.ID); v.StartedAt != nil || v.EndReason != "user_cancelled" {
		t.Fatalf("撤回的消息不应执行：%+v", v)
	}
}

func runOf(t *testing.T, s *server, id uuid.UUID) store.Run {
	t.Helper()
	r, err := s.a.Store.GetRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// 节点停止，真实 App + fakePi：有排队项时节点继续处理下一条；没有排队项时节点转阻塞（已被人停止）。
func TestQueueNodeStopActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	_, manager := s.createAgent("Manager")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	start := func(name string) (taskmodel.Detail, apitypes.Agent, uuid.UUID) {
		_, worker := s.createAgent(name)
		raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: name, Request: map[string]any{},
			Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=30s !noabort !text=working", AgentID: worker.ID,
				InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
		if err != nil {
			t.Fatal(err)
		}
		task := decode[taskmodel.Detail](t, raw)
		var active uuid.UUID
		waitFor(t, name+" 节点进入运行", func() bool {
			return s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, task.Nodes[0].RuntimeID).Scan(&active) == nil
		})
		return task, worker, active
	}
	body := func(worker apitypes.Agent, task taskmodel.Detail) map[string]any {
		return map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": task.Nodes[0].RuntimeID}
	}
	turnState := func(id uuid.UUID) (string, string) {
		var status, code string
		if err := s.a.Store.Pool.QueryRow(ctx, `SELECT status,coalesce(error_code,'') FROM task_turns WHERE id=$1`, id).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		return status, code
	}

	// 没有排队项：停止后节点转阻塞。
	task, worker, active := start("Alone")
	base := "/tasks/" + task.Task.ID.String() + "/nodes/" + task.Nodes[0].ID.String()
	r := decode[taskmodel.TaskNodeQueueActionReceipt](t, s.must(200, "POST", base+"/runs/"+active.String()+"/stop", body(worker, task)))
	if r.Status != "accepted" || r.RunID != active {
		t.Fatalf("停止回执：%+v", r)
	}
	waitFor(t, "节点转阻塞", func() bool {
		d, e := s.a.Tasks.Get(ctx, auth.Console(), task.Task.ID)
		return e == nil && d.Nodes[0].Status == "blocked"
	})
	if st, code := turnState(active); st != "interrupted" || code != "user_stopped" {
		t.Fatalf("被停止的轮次：%s %s", st, code)
	}
	d, _ := s.a.Tasks.Get(ctx, auth.Console(), task.Task.ID)
	if d.Task.Reason == nil || !strings.Contains(*d.Task.Reason, "已被人停止") {
		t.Fatalf("阻塞原因：%+v", d.Task.Reason)
	}
	if code, raw := s.call("POST", base+"/runs/"+active.String()+"/stop", body(worker, task)); code != 409 {
		t.Fatalf("已结束的轮次不能停止：%d %s", code, raw)
	}

	// 有排队项：停止后节点继续处理下一条。
	task, worker, active = start("Busy")
	base = "/tasks/" + task.Task.ID.String() + "/nodes/" + task.Nodes[0].ID.String()
	s.must(201, "POST", base+"/inbox-messages", map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": task.Nodes[0].RuntimeID, "text": "!text=next", "whenBusy": "queue"})
	q := decode[taskmodel.TaskNodeQueue](t, s.must(200, "GET", base+"/queue", nil))
	if len(q.Items) != 1 || q.Items[0].Text != "!text=next" {
		t.Fatalf("节点排队：%+v", q.Items)
	}
	next := q.Items[0].RunID
	s.must(200, "POST", base+"/runs/"+active.String()+"/stop", body(worker, task))
	waitFor(t, "停止后领取下一条", func() bool {
		var started bool
		return s.a.Store.Pool.QueryRow(ctx, `SELECT started_at IS NOT NULL FROM task_turns WHERE id=$1`, next).Scan(&started) == nil && started
	})
	if st, code := turnState(active); st != "interrupted" || code != "user_stopped" {
		t.Fatalf("被停止的轮次：%s %s", st, code)
	}
}
