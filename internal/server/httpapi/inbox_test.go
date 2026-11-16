package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/contracttest"
)

// caller calls an entry point as some identity: the console (no token), the external port (external token) or the Agent
// API (Agent token).
type caller struct {
	t      *testing.T
	base   string // includes /api/v1 or /agent/v1
	client *http.Client
	token  string
}

func (c caller) do(method, path string, body any, hdr ...string) (int, []byte, http.Header) {
	c.t.Helper()
	var r io.Reader
	if b, ok := body.([]byte); ok {
		r = bytes.NewReader(b)
	} else if body != nil {
		j, _ := json.Marshal(body)
		r = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if body != nil {
		if _, ok := body.([]byte); ok {
			req.Header.Set("Content-Type", "application/octet-stream")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, resp.Header
}

func (c caller) expect(status int, method, path string, body any, hdr ...string) []byte {
	c.t.Helper()
	got, out, _ := c.do(method, path, body, hdr...)
	if got != status {
		c.t.Fatalf("%s %s：期望 %d，实际 %d：%s", method, path, status, got, out)
	}
	return out
}

func (c caller) expectError(status int, code, method, path string, body any, hdr ...string) {
	c.t.Helper()
	out := c.expect(status, method, path, body, hdr...)
	var e apitypes.ErrorBody
	if err := json.Unmarshal(out, &e); err != nil || e.Error.Code != code {
		c.t.Fatalf("%s %s：期望错误码 %s：%s", method, path, code, out)
	}
	if c.token != "" && strings.Contains(string(out), c.token) {
		c.t.Fatalf("错误信息不应回显令牌：%s", out)
	}
}

// console returns a console caller whose calls are validated against the contract.
func (e *env) console() caller {
	return caller{t: e.t, base: e.srv.URL + "/api/v1", client: e.client}
}

// external starts the external port's handler (without TLS in tests) and returns a way to call it with token.
func (e *env) external(token string) caller {
	e.t.Helper()
	if e.ext == nil {
		e.ext = httptest.NewServer(e.api.ExternalHandler(nil))
		e.t.Cleanup(e.ext.Close)
		e.extV = contracttest.New(e.t, e.ext.URL)
	}
	return caller{t: e.t, base: e.ext.URL + "/api/v1", client: e.extV.Client(), token: token}
}

// agentCaller issues a token for an Agent and returns a way to call /agent/v1 with it.
func (e *env) agentCaller(agentID uuid.UUID) caller {
	e.t.Helper()
	tok, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.InsertAgentToken(context.Background(), tok.ID, agentID, uuid.Must(uuid.NewV7()), tok.Hash); err != nil {
		e.t.Fatal(err)
	}
	if e.agentV == nil {
		e.agentV = contracttest.NewAgent(e.t, e.srv.URL)
	}
	return caller{t: e.t, base: e.srv.URL + "/agent/v1", client: e.agentV.Client(), token: tok.Plain}
}

// identity creates an external identity and issues a token for it.
func (e *env) identity(name string) (apitypes.ExternalIdentity, string) {
	e.t.Helper()
	x := decode[apitypes.ExternalIdentity](e.t, e.expect(201, "POST", "/external-identities", map[string]any{"name": name}))
	issued := decode[apitypes.ExternalTokenIssued](e.t, e.expect(201, "POST", "/external-identities/"+x.ID.String()+"/tokens", map[string]any{}))
	return x, issued.Secret
}

func (e *env) platformAgent(name string) apitypes.Agent {
	e.t.Helper()
	return decode[apitypes.Agent](e.t, e.expect(201, "POST", "/platform/agents", map[string]any{"name": name,
		"model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
}

func sendInbox(c caller, to, text string, whenBusy ...string) apitypes.InboxMessage {
	c.t.Helper()
	body := map[string]any{"to": to, "text": text}
	if len(whenBusy) > 0 {
		body["whenBusy"] = whenBusy[0]
	}
	return decode[apitypes.InboxMessage](c.t, c.expect(201, "POST", "/inbox/messages", body))
}

func strOf(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// ---- Addresses and sending ----

func TestInboxAddressing(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	dev, a := e.newAgent("开发助手")
	if strOf(dev.Address) != "默认组织/默认项目/开发助手" {
		t.Fatalf("Agent 的地址应为完整三段：%s", strOf(dev.Address))
	}
	plat := e.platformAgent("巡检员")
	if strOf(plat.Address) != "巡检员" {
		t.Fatalf("平台 Agent 的地址应为名称本身：%s", strOf(plat.Address))
	}
	waitIdle(t, e, a)

	m := sendInbox(con, "默认项目/开发助手", "你好")
	if m.From.Kind != "console" || strOf(m.From.Address) != "@console" || m.From.Name != "控制台" || m.From.ID != nil {
		t.Fatalf("发件人应为控制台操作者：%+v", m.From)
	}
	if m.To.Kind != "agent" || strOf(m.To.Address) != "默认组织/默认项目/开发助手" || *m.To.ID != dev.ID || m.ThreadID != m.ID || m.InReplyTo != nil {
		t.Fatalf("收件人与线索不对：%+v", m)
	}
	if strOf(m.WhenBusy) != "queue" || m.ReadAt != nil {
		t.Fatalf("不指定 whenBusy 时应记为 queue，且未读：%+v", m)
	}
	for _, addr := range []string{"默认组织/默认项目/开发助手", dev.ID.String(), strings.ToUpper(dev.ID.String())} {
		if got := sendInbox(con, addr, "同一个"); *got.To.ID != dev.ID {
			t.Fatalf("%s 应解析为同一个 Agent", addr)
		}
	}
	if got := sendInbox(con, "巡检员", "发给平台 Agent"); *got.To.ID != plat.ID || strOf(got.To.Address) != "巡检员" {
		t.Fatalf("单段名称应解析为平台 Agent：%+v", got.To)
	}
	for _, addr := range []string{"默认项目/开发助手#t1", "默认组织/默认项目/开发助手/x", "默认项目/不存在", "不存在", "其他组织/默认项目/开发助手", "@nobody", "@"} {
		con.expectError(404, "not_found", "POST", "/inbox/messages", map[string]any{"to": addr, "text": "x"})
	}
	con.expectError(422, "validation_failed", "POST", "/inbox/messages", map[string]any{"to": "巡检员", "text": "  "})
	con.expectError(413, "payload_too_large", "POST", "/inbox/messages", map[string]any{"to": "巡检员", "text": strings.Repeat("x", 1<<16+1)})
	con.expectError(422, "validation_failed", "POST", "/inbox/messages", map[string]any{"to": "巡检员", "text": "x", "whenBusy": "now"},
		"X-Contract-Invalid-Request", "1")

	// Sending to a person: whenBusy is recorded as empty and no reminder is created.
	ci, _ := e.identity("ci-bot")
	got := sendInbox(con, "@ci-bot", "给外部身份", "interrupt")
	if got.WhenBusy != nil || got.To.Kind != "external" || *got.To.ID != ci.ID || strOf(got.To.Address) != "@ci-bot" {
		t.Fatalf("发给外部身份：%+v", got)
	}
	// A deleted external identity can no longer receive messages.
	e.expect(204, "DELETE", "/external-identities/"+ci.ID.String(), nil)
	con.expectError(404, "not_found", "POST", "/inbox/messages", map[string]any{"to": "@ci-bot", "text": "x"})

	// Lookup by address: both notations, platform Agents, and an empty list when not found or when the address has #.
	for addr, want := range map[string]uuid.UUID{"默认组织/默认项目/开发助手": dev.ID, "默认项目/开发助手": dev.ID, "巡检员": plat.ID, dev.ID.String(): dev.ID} {
		l := decode[apitypes.AgentList](t, e.expect(200, "GET", "/agents?address="+urlQuery(addr), nil))
		if len(l.Agents) != 1 || l.Agents[0].ID != want || l.Agents[0].Address == nil {
			t.Fatalf("按地址 %s 查找：%+v", addr, l)
		}
	}
	for _, addr := range []string{"默认项目/开发助手#t1", "不存在", "@console"} {
		if l := decode[apitypes.AgentList](t, e.expect(200, "GET", "/agents?address="+urlQuery(addr), nil)); len(l.Agents) != 0 {
			t.Fatalf("按地址 %s 查找应为空：%+v", addr, l)
		}
	}
	e.expect(422, "GET", "/agents", nil, "X-Contract-Invalid-Request", "1")

	// Agents in a deleted project are not addressable.
	p := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "临时"}))
	tmp := decode[apitypes.Agent](t, e.expect(201, "POST", "/projects/"+p.ID.String()+"/agents", map[string]any{"name": "甲",
		"model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	waitIdle(t, e, "/agents/"+tmp.ID.String())
	e.expect(204, "DELETE", "/projects/"+p.ID.String(), nil)
	con.expectError(404, "not_found", "POST", "/inbox/messages", map[string]any{"to": "临时/甲", "text": "x"})
	con.expectError(404, "not_found", "POST", "/inbox/messages", map[string]any{"to": tmp.ID.String(), "text": "x"})

	// An Agent created before the rules took effect, with a non-conforming name: its address is empty and it can only
	// be addressed by ID.
	legacy := uuid.Must(uuid.NewV7())
	if _, err := e.st.Pool.Exec(context.Background(), `INSERT INTO agents (id, project_id, name, model_provider, model_id) VALUES ($1, $2, 'a/b', 'anthropic', 'claude-x')`,
		legacy, e.dc.Project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Pool.Exec(context.Background(), `INSERT INTO agent_sessions (agent_id) VALUES ($1)`, legacy); err != nil {
		t.Fatal(err)
	}
	if got := decode[apitypes.Agent](t, e.expect(200, "GET", "/agents/"+legacy.String(), nil)); got.Address != nil {
		t.Fatalf("名称不合规的 Agent 地址应为空：%s", strOf(got.Address))
	}
	if got := sendInbox(con, legacy.String(), "按 ID 发送"); *got.To.ID != legacy || got.To.Address != nil {
		t.Fatalf("名称不合规的 Agent 只能按 ID 寻址：%+v", got.To)
	}
	waitIdle(t, e, "/agents/"+legacy.String())

	// A project Agent and a platform Agent message each other (so the same-organization and platform-owned branches are
	// actually exercised).
	devC, platC := e.agentCaller(dev.ID), e.agentCaller(plat.ID)
	if got := sendInbox(devC, "巡检员", "项目到平台"); got.From.Kind != "agent" || *got.From.ID != dev.ID || *got.To.ID != plat.ID {
		t.Fatalf("项目 Agent 发给平台 Agent：%+v", got)
	}
	if got := sendInbox(platC, "默认项目/开发助手", "平台到项目"); *got.To.ID != dev.ID {
		t.Fatalf("平台 Agent 用两段地址发给项目 Agent（按默认组织解析）：%+v", got)
	}
	if got := sendInbox(devC, "默认项目/开发助手", "同组织两段"); *got.To.ID != dev.ID {
		t.Fatalf("同组织 Agent 用两段地址：%+v", got)
	}
	waitIdle(t, e, a)

	// After a project is renamed, the address changes with it.
	e.expect(200, "PATCH", "/projects/"+e.dc.Project.ID.String(), map[string]any{"name": "主项目"})
	if got := decode[apitypes.Agent](t, e.expect(200, "GET", a, nil)); strOf(got.Address) != "默认组织/主项目/开发助手" {
		t.Fatalf("项目改名后地址应改变：%s", strOf(got.Address))
	}
}

func urlQuery(s string) string {
	r := strings.NewReplacer("%", "%25", "#", "%23", "&", "%26", "+", "%2B", " ", "%20", "?", "%3F")
	return r.Replace(s)
}

// ---- Reminders ----

func reminderRuns(t *testing.T, e *env, agentID uuid.UUID) (queued map[int]int, triggers []uuid.UUID) {
	t.Helper()
	rows, err := e.st.Pool.Query(context.Background(), `SELECT status, priority, trigger_inbox_message_id FROM runs
		WHERE agent_id = $1 AND origin = 'inbox_reminder' ORDER BY queue_seq`, agentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	queued = map[int]int{}
	for rows.Next() {
		var status string
		var pri int
		var trig uuid.UUID
		if err := rows.Scan(&status, &pri, &trig); err != nil {
			t.Fatal(err)
		}
		if status == "queued" {
			queued[pri]++
		}
		triggers = append(triggers, trig)
	}
	return queued, triggers
}

func TestInboxReminder(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	dev, a := e.newAgent("开发助手")
	waitIdle(t, e, a)

	// When idle, a reminder turn starts immediately, marked in the history as a platform reminder, with the reminder
	// text as its body.
	m1 := sendInbox(con, dev.ID.String(), "第一条")
	waitIdle(t, e, a)
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages", nil))
	var user apitypes.Message
	for _, m := range page.Messages {
		if m.Role == "user" {
			user = m
		}
	}
	if user.Origin != "inbox_reminder" || !strings.Contains(user.Text, "the following 1 Inbox messages") || !strings.Contains(user.Text, "loopctl inbox read") || !strings.Contains(user.Text, m1.ID.String()) {
		t.Fatalf("提醒那一轮的用户消息：%+v", user)
	}
	if reply := e.lastReply(a); reply.Origin != "inbox_reminder" || reply.Run.Status != "completed" {
		t.Fatalf("提醒那一轮的回复：%+v", reply)
	}
	var trig *uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT DISTINCT inbox_message_id FROM lifecycle_events WHERE run_id = $1`, user.Run.ID).Scan(&trig); err != nil || trig == nil || *trig != m1.ID {
		t.Fatalf("提醒那一轮的运行记录事件应记下触发的消息：%v %v", trig, err)
	}

	// In events of the regular conversation, the Inbox message field is empty.
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "普通对话"})
	waitIdle(t, e, a)
	normal := e.lastReply(a)
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_events WHERE run_id = $1 AND inbox_message_id IS NOT NULL`, normal.Run.ID).Scan(&n); err != nil || n != 0 || normal.Origin != "console" {
		t.Fatalf("普通对话不应记 Inbox 消息：%d %v %s", n, err, normal.Origin)
	}

	// While busy, three queue messages are queued into just one reminder turn, triggered by the first one.
	agent := e.agentCaller(dev.ID)
	for _, s := range decode[apitypes.InboxMessagePage](t, agent.expect(200, "GET", "/inbox/messages?unread=true", nil)).Messages {
		agent.expect(200, "POST", "/inbox/messages/"+s.ID.String()+"/read", nil)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "block"})
	q1 := sendInbox(con, dev.ID.String(), "排队一")
	sendInbox(con, dev.ID.String(), "排队二")
	sendInbox(con, dev.ID.String(), "排队三")
	if queued, _ := reminderRuns(t, e, dev.ID); queued[1] != 1 || queued[0] != 0 {
		t.Fatalf("三条 queue 消息应只排入一轮普通提醒：%v", queued)
	}
	// 20 concurrent ones also give only one turn.
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { sendInbox(con, dev.ID.String(), "并发"+string(rune('a'+i))) })
	}
	wg.Wait()
	if queued, _ := reminderRuns(t, e, dev.ID); queued[1] != 1 {
		t.Fatalf("并发的 queue 消息应只有一轮普通提醒：%v", queued)
	}
	// An interrupt message interrupts the current run and gets its own interrupt reminder turn, which runs before the
	// regular reminder.
	sendInbox(con, dev.ID.String(), "打断", "interrupt")
	waitIdle(t, e, a)
	_, triggers := reminderRuns(t, e, dev.ID)
	if len(triggers) != 3 || triggers[1] != q1.ID {
		t.Fatalf("合并的提醒只记第一条触发消息：%v", triggers)
	}
	var blockStatus string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT r.status FROM runs r JOIN conversation_messages m ON m.id = r.user_message_id
		WHERE m.text = 'block' AND r.agent_id = $1`, dev.ID).Scan(&blockStatus); err != nil || blockStatus != "interrupted" {
		t.Fatalf("打断消息应打断当前执行：%s %v", blockStatus, err)
	}
	// Run order: the interrupt reminder before the regular reminder.
	rows, _ := e.st.Pool.Query(context.Background(), `SELECT priority FROM runs WHERE agent_id = $1 AND origin = 'inbox_reminder' ORDER BY started_at`, dev.ID)
	var order []int
	for rows.Next() {
		var p int
		_ = rows.Scan(&p)
		order = append(order, p)
	}
	rows.Close()
	if len(order) != 3 || order[1] != 0 || order[2] != 1 {
		t.Fatalf("打断提醒应先于普通提醒执行：%v", order)
	}

	// All messages were read while queued: read messages leave the queue, so this reminder turn is voided at once
	// (no_pending_messages) and Pi is not started.
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "block"})
	sendInbox(con, dev.ID.String(), "很快读掉")
	for _, s := range decode[apitypes.InboxMessagePage](t, agent.expect(200, "GET", "/inbox/messages?unread=true&limit=100", nil)).Messages {
		agent.expect(200, "POST", "/inbox/messages/"+s.ID.String()+"/read", nil)
	}
	e.expect(202, "POST", a+"/messages", map[string]any{"text": "解除", "whenBusy": "interrupt"})
	waitIdle(t, e, a)
	var voided string
	var replies int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT r.status||'/'||coalesce(r.end_reason,''), (SELECT count(*) FROM conversation_messages m WHERE m.run_id = r.id AND m.role = 'assistant')
		FROM runs r WHERE r.agent_id = $1 AND r.origin = 'inbox_reminder' ORDER BY r.queued_at DESC LIMIT 1`, dev.ID).Scan(&voided, &replies); err != nil || voided != "interrupted/no_pending_messages" || replies != 0 {
		t.Fatalf("没有未读时这一轮提醒应作废、不回复：%q %d %v", voided, replies, err)
	}
}

// ---- Threads, reading and viewing ----

func TestInboxThreadsAndViews(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	dev, a := e.newAgent("开发助手")
	waitIdle(t, e, a)
	agent := e.agentCaller(dev.ID)
	ci, ciTok := e.identity("ci-bot")
	ext := e.external(ciTok)

	m := sendInbox(con, "默认项目/开发助手", "请处理")
	waitIdle(t, e, a)

	// The console viewing an Agent's Inbox does not change the read state.
	view := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", a+"/inbox", nil))
	if len(view.Messages) != 1 || view.Messages[0].ReadAt != nil {
		t.Fatalf("Agent 收件箱：%+v", view)
	}
	full := decode[apitypes.InboxMessage](t, e.expect(200, "GET", a+"/inbox/"+m.ID.String(), nil))
	if full.Text != "请处理" || full.ReadAt != nil {
		t.Fatalf("控制台查看全文后仍应未读：%+v", full)
	}
	// An Agent listing summaries does not count as reading; reading one by one marks them read, and reading again does
	// not change the read time.
	l := decode[apitypes.InboxMessagePage](t, agent.expect(200, "GET", "/inbox/messages", nil))
	if len(l.Messages) != 1 || l.Messages[0].ReadAt != nil || l.Messages[0].Preview != "请处理" {
		t.Fatalf("Agent 列出收件箱：%+v", l)
	}
	r1 := decode[apitypes.InboxMessage](t, agent.expect(200, "POST", "/inbox/messages/"+m.ID.String()+"/read", nil))
	r2 := decode[apitypes.InboxMessage](t, agent.expect(200, "POST", "/inbox/messages/"+m.ID.String()+"/read", nil))
	if r1.ReadAt == nil || !r1.ReadAt.Equal(*r2.ReadAt) || r2.Text != "请处理" {
		t.Fatalf("重复读取：%v %v", r1.ReadAt, r2.ReadAt)
	}
	// Messages not sent to oneself cannot be read.
	ext.expectError(404, "not_found", "POST", "/inbox/messages/"+m.ID.String()+"/read", nil)

	// An Agent replies to the console; the console adds to the thread; a non-participant identity cannot reply.
	reply := decode[apitypes.InboxMessage](t, agent.expect(201, "POST", "/inbox/messages/"+m.ID.String()+"/reply", map[string]any{"text": "收到", "whenBusy": "interrupt"}))
	if reply.To.Kind != "console" || reply.ThreadID != m.ID || *reply.InReplyTo != m.ID || reply.WhenBusy != nil {
		t.Fatalf("Agent 的回复：%+v", reply)
	}
	more := decode[apitypes.InboxMessage](t, con.expect(201, "POST", "/inbox/messages/"+m.ID.String()+"/reply", map[string]any{"text": "补充"}))
	if more.To.Kind != "agent" || *more.To.ID != dev.ID || more.ThreadID != m.ID {
		t.Fatalf("控制台在线索中追加：%+v", more)
	}
	ext.expectError(404, "not_found", "POST", "/inbox/messages/"+m.ID.String()+"/reply", map[string]any{"text": "插话"})
	waitIdle(t, e, a)

	// The console's threads: the participants include itself, the unread count only counts messages sent to it; viewing
	// a thread does not change read state; marking a whole thread read lowers the unread total.
	threads := decode[apitypes.InboxThreadPage](t, con.expect(200, "GET", "/inbox/threads", nil))
	if len(threads.Threads) != 1 || threads.UnreadTotal != 1 || threads.Threads[0].UnreadCount != 1 || len(threads.Threads[0].Participants) != 2 ||
		threads.Threads[0].LastMessage.ID != more.ID {
		t.Fatalf("控制台的线索：%+v", threads)
	}
	thread := decode[apitypes.InboxThread](t, con.expect(200, "GET", "/inbox/threads/"+m.ID.String(), nil))
	if len(thread.Messages) != 3 || thread.Messages[1].ID != reply.ID || thread.Messages[1].ReadAt != nil {
		t.Fatalf("线索详情：%+v", thread)
	}
	if got := decode[apitypes.InboxUnreadTotal](t, con.expect(200, "POST", "/inbox/threads/"+m.ID.String()+"/read", nil)); got.UnreadTotal != 0 {
		t.Fatalf("整条标为已读后未读总数应为 0：%+v", got)
	}
	ext.expectError(404, "not_found", "GET", "/inbox/threads/"+m.ID.String(), nil)
	if l := decode[apitypes.InboxThreadPage](t, ext.expect(200, "GET", "/inbox/threads", nil)); len(l.Threads) != 0 {
		t.Fatalf("外部身份只看到自己参与的线索：%+v", l)
	}

	// Exchanges with an external identity and the console's read-only view; direction and unread filters; still
	// viewable after deletion, shown as a deleted identity.
	toCi := sendInbox(con, "@ci-bot", "给你")
	fromCi := sendInbox(ext, "默认项目/开发助手", "外部来信")
	waitIdle(t, e, a)
	all := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/inbox", nil))
	if len(all.Messages) != 2 || all.Messages[0].ID != fromCi.ID || all.Messages[1].ID != toCi.ID {
		t.Fatalf("外部身份收件箱（全部，新的在前）：%+v", all)
	}
	for dir, want := range map[string]uuid.UUID{"received": toCi.ID, "sent": fromCi.ID} {
		got := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/inbox?direction="+dir, nil))
		if len(got.Messages) != 1 || got.Messages[0].ID != want {
			t.Fatalf("方向 %s：%+v", dir, got)
		}
	}
	unread := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/inbox?direction=received&unread=true", nil))
	if len(unread.Messages) != 1 {
		t.Fatalf("只看未读：%+v", unread)
	}
	if got := decode[apitypes.InboxMessage](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/inbox/"+toCi.ID.String(), nil)); got.ReadAt != nil {
		t.Fatalf("控制台查看外部身份的消息后仍应未读：%+v", got)
	}
	e.expect(404, "GET", "/external-identities/"+ci.ID.String()+"/inbox/"+m.ID.String(), nil)
	// An external identity can only see its own Inbox: only messages sent to it are listed.
	mine := decode[apitypes.InboxMessagePage](t, ext.expect(200, "GET", "/inbox/messages?unread=true", nil))
	if len(mine.Messages) != 1 || mine.Messages[0].ID != toCi.ID {
		t.Fatalf("外部身份的收件箱：%+v", mine)
	}
	e.expect(204, "DELETE", "/external-identities/"+ci.ID.String(), nil)
	gone := decode[apitypes.ExternalIdentity](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String(), nil))
	if gone.DeletedAt == nil {
		t.Fatalf("已删除的身份应带删除时间：%+v", gone)
	}
	after := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/inbox", nil))
	if len(after.Messages) != 2 || !after.Messages[0].From.Deleted || after.Messages[0].From.Name != "已删除身份" ||
		strOf(after.Messages[0].From.Address) != "@ci-bot" || *after.Messages[0].From.ID != ci.ID {
		t.Fatalf("删除后的历史消息：%+v", after.Messages[0].From)
	}
	e.expect(404, "GET", "/external-identities/"+ci.ID.String()+"/tokens", nil)
	if l := decode[apitypes.ExternalIdentityList](t, e.expect(200, "GET", "/external-identities", nil)); len(l.Identities) != 0 {
		t.Fatalf("列表不含已删除的身份：%+v", l)
	}
	// A name can be reused after it is released.
	e.identity("ci-bot")
}

func TestInboxPaging(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	plat := e.platformAgent("巡检员")
	agent := e.agentCaller(plat.ID)
	var sent []uuid.UUID
	for i := range 5 {
		sent = append(sent, decode[apitypes.InboxMessage](t, agent.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "第" + string(rune('1'+i)) + "条"})).ID)
	}
	var got []uuid.UUID
	cursor := ""
	for range 10 {
		path := "/inbox/messages?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		p := decode[apitypes.InboxMessagePage](t, con.expect(200, "GET", path, nil))
		for _, m := range p.Messages {
			got = append(got, m.ID)
		}
		if p.NextCursor == nil {
			break
		}
		cursor = *p.NextCursor
	}
	if len(got) != 5 || got[0] != sent[4] || got[4] != sent[0] {
		t.Fatalf("游标分页应不重不漏、新的在前：%v", got)
	}
	con.expectError(422, "validation_failed", "GET", "/inbox/messages?cursor=bad", nil)
	if p := decode[apitypes.InboxMessagePage](t, con.expect(200, "GET", "/inbox/messages?limit=1000", nil)); len(p.Messages) != 5 {
		t.Fatalf("超过上限时按上限返回：%d", len(p.Messages))
	}
}

// ---- Inbox event stream ----

func TestInboxEvents(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	plat := e.platformAgent("巡检员")
	agent := e.agentCaller(plat.ID)

	evs, stop := readSSEPath(t, e, "/inbox/events", "")
	defer stop()
	snap := next(t, evs)
	if snap.event != "snapshot" {
		t.Fatalf("首条应为快照：%+v", snap)
	}
	e.v.ValidateStreamSSE(contracttest.InboxEventsPath, snap.event, []byte(snap.data))

	msg := decode[apitypes.InboxMessage](t, agent.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "报告"}))
	created := next(t, evs)
	var c apitypes.SseInboxMessageCreated
	_ = json.Unmarshal([]byte(created.data), &c)
	if created.event != "inbox.message.created" || c.Message.ID != msg.ID || c.Message.To.Kind != "console" || c.UnreadTotal != 1 {
		t.Fatalf("应收到 inbox.message.created：%+v", created)
	}
	e.v.ValidateStreamSSE(contracttest.InboxEventsPath, created.event, []byte(created.data))
	con.expect(200, "POST", "/inbox/messages/"+msg.ID.String()+"/read", nil)
	read := next(t, evs)
	var rd apitypes.SseInboxMessageRead
	_ = json.Unmarshal([]byte(read.data), &rd)
	if read.event != "inbox.message.read" || rd.ID != msg.ID || rd.UnreadTotal != 0 || rd.To.Kind != "console" {
		t.Fatalf("应收到 inbox.message.read：%+v", read)
	}
	e.v.ValidateStreamSSE(contracttest.InboxEventsPath, read.event, []byte(read.data))
	stop()

	// Resuming with an ID still inside the retained range: no snapshot, nothing lost or duplicated.
	agent.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": "断线期间"})
	evs2, stop2 := readSSEPath(t, e, "/inbox/events", read.id)
	defer stop2()
	first := next(t, evs2)
	if first.event != "inbox.message.created" || !strings.Contains(first.data, "断线期间") {
		t.Fatalf("续传应从断开处继续：%+v", first)
	}
	stop2()
	// The resume position is outside the range (events were trimmed, or the server restarted): a snapshot comes first.
	trimStream(t, func() (string, error) { return e.co.AppendInbox(context.Background(), "test.filler", []byte(`{}`)) })
	evs3, stop3 := readSSEPath(t, e, "/inbox/events", first.id)
	defer stop3()
	if s := next(t, evs3); s.event != "snapshot" || !strings.Contains(s.data, `"unreadTotal":1`) {
		t.Fatalf("续传位置超出范围时应先发快照：%+v", s)
	}
}

// ---- External port and Agent API ----

func TestExternalAccess(t *testing.T) {
	e := newEnv(t)
	dev, a := e.newAgent("开发助手")
	waitIdle(t, e, a)
	ci, tok := e.identity("ci-bot")
	ext := e.external(tok)

	// No token, a malformed one, or an Agent token: 401.
	anon := e.external("")
	anon.expectError(401, "unauthenticated", "GET", "/context", nil)
	e.external("lne_bad").expectError(401, "unauthenticated", "GET", "/context", nil)
	agentTok := e.agentCaller(dev.ID).token
	e.external(agentTok).expectError(401, "unauthenticated", "GET", "/context", nil)
	// A token in the URL is not read.
	anon.expectError(401, "unauthenticated", "GET", "/context?access_token="+tok, nil)

	// Current identity and externally open endpoints.
	who := decode[apitypes.Whoami](t, ext.expect(200, "GET", "/whoami", nil))
	if who.Kind != "external" || strOf(who.Address) != "@ci-bot" || who.Name != "ci-bot" {
		t.Fatalf("whoami：%+v", who)
	}
	if w := decode[apitypes.Whoami](t, e.expect(200, "GET", "/whoami", nil)); w.Kind != "console" || strOf(w.Address) != "@console" {
		t.Fatalf("控制台的 whoami：%+v", w)
	}
	ext.expect(409, "PUT", a+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk-audit-secret-value"})
	_, body, hdr := ext.do("PATCH", a, map[string]any{"prompt": "由外部修改", "expectedPromptVersion": 1})
	if got := decode[apitypes.Agent](t, body); got.Prompt != "由外部修改" {
		t.Fatalf("外部身份修改 Agent 配置：%s", body)
	}
	audits, err := e.st.ListAudit(context.Background(), 10)
	if err != nil || len(audits) == 0 || audits[0].ActorAddress != "@ci-bot" || audits[0].Operation != "updateAgent" ||
		audits[0].RequestID == nil || audits[0].RequestID.String() != hdr.Get("Loop-Now-Request-Id") || audits[0].Target["agentId"] != dev.ID.String() ||
		*audits[0].ActorID != ci.ID || audits[0].Status != 200 {
		t.Fatalf("外部身份的修改应记审计：%+v %v", audits, err)
	}

	// External health status is redacted: the database part only has ok, no raw error text or WAL information.
	_, hb, _ := ext.do("GET", "/health", nil)
	var raw map[string]map[string]any
	_ = json.Unmarshal(hb, &raw)
	if len(raw["database"]) != 1 || raw["database"]["ok"] != true || strings.Contains(string(hb), `"error"`) || strings.Contains(string(hb), "wal") {
		t.Fatalf("对外的健康状态只应有 database.ok：%s", hb)
	}
	if _, cb, _ := e.do("GET", "/health", nil); !strings.Contains(string(cb), `"error"`) || !strings.Contains(string(cb), `"walBytes"`) || !strings.Contains(string(cb), `"walWarning":false`) {
		t.Fatalf("控制台的健康状态应含 error 与 WAL 信息：%s", cb)
	}

	// Console-only endpoints: 403.
	for _, c := range []struct{ method, path string }{
		{"POST", a + "/messages"}, {"GET", a + "/events"}, {"GET", "/overview/events"}, {"GET", "/inbox/events"},
		{"GET", a + "/inbox"}, {"GET", "/external-identities"}, {"POST", "/external-identities"},
		{"GET", "/external-identities/" + ci.ID.String() + "/inbox"}, {"PUT", "/projects/" + e.dc.Project.ID.String() + "/prompt"},
	} {
		ext.expectError(403, "forbidden", c.method, c.path, map[string]any{"text": "x", "name": "x"}, "X-Contract-Invalid-Request", "1")
	}
	if n := countRuns(t, e, dev.ID); n != 0 {
		t.Fatalf("被拒绝的直接对话不应保存消息：%d", n)
	}
	// External identity sending: whenBusy defaults to queue.
	got := sendInbox(ext, "默认项目/开发助手", "外部来信")
	if got.From.Kind != "external" || strOf(got.WhenBusy) != "queue" {
		t.Fatalf("外部身份发送：%+v", got)
	}
	waitIdle(t, e, a)

	// 401 after revocation (other tokens unaffected), for an expired token, and after the identity is deleted.
	tokens := decode[apitypes.ExternalTokenList](t, e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/tokens", nil))
	if len(tokens.Tokens) != 1 || !strings.HasPrefix(tok, tokens.Tokens[0].Prefix) || tokens.Tokens[0].LastUsedAt == nil {
		t.Fatalf("令牌列表只有元数据：%+v", tokens)
	}
	if strings.Contains(string(e.expect(200, "GET", "/external-identities/"+ci.ID.String()+"/tokens", nil)), tok[12:]) {
		t.Fatal("令牌列表不应含令牌明文")
	}
	second := decode[apitypes.ExternalTokenIssued](t, e.expect(201, "POST", "/external-identities/"+ci.ID.String()+"/tokens",
		map[string]any{"expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339)}))
	e.expect(204, "DELETE", "/external-identities/"+ci.ID.String()+"/tokens/"+tokens.Tokens[0].ID.String(), nil)
	e.expect(204, "DELETE", "/external-identities/"+ci.ID.String()+"/tokens/"+tokens.Tokens[0].ID.String(), nil)
	ext.expectError(401, "unauthenticated", "GET", "/whoami", nil)
	e.external(second.Secret).expect(200, "GET", "/whoami", nil)
	e.expect(422, "POST", "/external-identities/"+ci.ID.String()+"/tokens", map[string]any{"expiresAt": time.Now().Add(-time.Minute).Format(time.RFC3339)})
	expired, _ := auth.NewToken(auth.ExternalPrefix)
	past := time.Now().Add(-time.Second)
	if _, err := e.st.InsertExternalToken(context.Background(), ci.ID, expired.ID, expired.Hash, auth.DisplayPrefix(expired.ID), &past); err != nil {
		t.Fatal(err)
	}
	e.external(expired.Plain).expectError(401, "unauthenticated", "GET", "/whoami", nil)
	e.expect(204, "DELETE", "/external-identities/"+ci.ID.String(), nil)
	e.external(second.Secret).expectError(401, "unauthenticated", "GET", "/whoami", nil)

	// Naming rules.
	for _, name := range []string{"console", "platform", "CI", ".ci", strings.Repeat("a", 65), "a b"} {
		e.expect(422, "POST", "/external-identities", map[string]any{"name": name}, "X-Contract-Invalid-Request", "1")
	}
	e.identity("ci.bot_2-x")
	e.expect(409, "POST", "/external-identities", map[string]any{"name": "ci.bot_2-x"})

	// The audit contains no tokens and no request bodies.
	rows, err := e.st.Pool.Query(context.Background(), `SELECT concat_ws('|', id, at, request_id, actor_kind, actor_id, actor_address, method, operation, target, status, on_behalf_of) FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		for _, secret := range []string{tok, second.Secret, "由外部修改", "sk-audit-secret-value", "ANTHROPIC_API_KEY", "lne_", "lna_"} {
			if strings.Contains(line, secret) {
				t.Fatalf("审计中不应出现令牌或请求体：%s", line)
			}
		}
	}
}

func countRuns(t *testing.T, e *env, agentID uuid.UUID) int {
	t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id = $1`, agentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTokensOnConsoleAndAgentEntries(t *testing.T) {
	e := newEnv(t)
	dev, a := e.newAgent("开发助手")
	waitIdle(t, e, a)
	_, extTok := e.identity("ci-bot")
	agent := e.agentCaller(dev.ID)

	// The console API accepts no tokens at all.
	for _, tok := range []string{extTok, agent.token} {
		c := e.console()
		c.token = tok
		c.expectError(403, "forbidden", "GET", "/context", nil)
	}
	// The Agent API only accepts Agent tokens; requests with an Origin are rejected.
	who := decode[apitypes.Whoami](t, agent.expect(200, "GET", "/whoami", nil))
	if who.Kind != "agent" || strOf(who.Address) != "默认组织/默认项目/开发助手" || who.Name != "开发助手" {
		t.Fatalf("Agent 的 whoami：%+v", who)
	}
	other := agent
	other.token = extTok
	other.expectError(401, "unauthenticated", "GET", "/whoami", nil)
	other.token = ""
	other.expectError(401, "unauthenticated", "GET", "/whoami", nil)
	agent.expectError(403, "forbidden_origin", "GET", "/whoami", nil, "Origin", "http://localhost:5173")
	// The Agent API has only 5 endpoints; other paths give 404 (not in the contract, so requested with a client that
	// does not validate against the contract).
	raw := agent
	raw.client = http.DefaultClient
	raw.expect(404, "GET", "/inbox/threads", nil)

	// 401 after revocation; 401 after the Agent is deleted.
	_, id, _, _ := auth.ParseToken(agent.token)
	if err := e.st.RevokeAgentToken(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	agent.expectError(401, "unauthenticated", "GET", "/whoami", nil)
	fresh := e.agentCaller(dev.ID)
	fresh.expect(200, "GET", "/inbox/messages", nil)
	e.expect(204, "DELETE", a, nil)
	fresh.expectError(401, "unauthenticated", "GET", "/inbox/messages", nil)
}

// ---- Names of built-in Skills ----

func (e *env) expectError(status int, code, method, path string, body any) {
	e.t.Helper()
	var eb apitypes.ErrorBody
	if err := json.Unmarshal(e.expect(status, method, path, body), &eb); err != nil || eb.Error.Code != code {
		e.t.Fatalf("%s %s：期望错误码 %s：%+v", method, path, code, eb)
	}
}

func TestBuiltinSkillNameReserved(t *testing.T) {
	e := newEnv(t)
	e.expectError(409, "name_conflict", "POST", "/skills", map[string]any{"skillMd": "---\nname: loopctl\ndescription: 冒名\n---\n"})
	e.expectError(409, "name_conflict", "POST", "/skills", zipUpload(t, "loopctl", "冒名"))
	tpl := decode[apitypes.SkillTemplate](t, e.expect(201, "POST", "/skills", map[string]any{"skillMd": "---\nname: notes\ndescription: 记笔记\n---\n"}))
	e.expectError(409, "name_conflict", "PUT", "/skills/"+tpl.ID.String()+"/skill-md", map[string]any{"content": "---\nname: loopctl\ndescription: 改名\n---\n"})
	_, a := e.newAgent("开发助手")
	waitIdle(t, e, a)
	k := decode[apitypes.AgentSkill](t, e.expect(201, "POST", a+"/skills", map[string]any{"templateId": tpl.ID}))
	e.expectError(409, "name_conflict", "PUT", a+"/skills/"+k.ID.String()+"/skill-md", map[string]any{"content": "---\nname: loopctl\ndescription: 改名\n---\n"})
}

// ---- Lost session ----

func TestInboxSessionMissing(t *testing.T) {
	e := newEnv(t)
	con := e.console()
	dev, a := e.newAgent("开发助手")
	waitIdle(t, e, a)
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state = 'missing', session_file = '/nonexistent/session.jsonl' WHERE agent_id = $1`, dev.ID); err != nil {
		t.Fatal(err)
	}
	// While the session is lost: messages are saved as usual, no reminder is scheduled.
	first := sendInbox(con, dev.ID.String(), "会话丢失期间一")
	sendInbox(con, dev.ID.String(), "会话丢失期间二")
	if queued, triggers := reminderRuns(t, e, dev.ID); len(triggers) != 0 || len(queued) != 0 {
		t.Fatalf("会话丢失时不应安排提醒：%v %v", queued, triggers)
	}
	if page := decode[apitypes.InboxMessagePage](t, e.expect(200, "GET", a+"/inbox?unread=true", nil)); len(page.Messages) != 2 {
		t.Fatalf("消息应照常保存：%+v", page)
	}
	// After a person confirms reopening the session: with unread messages, a reminder turn is scheduled at once,
	// triggered by the earliest unread message.
	e.expect(200, "POST", a+"/session/reset", nil)
	waitIdle(t, e, a)
	_, triggers := reminderRuns(t, e, dev.ID)
	if len(triggers) != 1 || triggers[0] != first.ID {
		t.Fatalf("重开会话后应安排一轮提醒，触发消息为最早的未读消息：%v", triggers)
	}
	page := decode[apitypes.MessagePage](t, e.expect(200, "GET", a+"/messages", nil))
	var user apitypes.Message
	for _, m := range page.Messages {
		if m.Role == "user" {
			user = m
		}
	}
	if user.Origin != "inbox_reminder" || !strings.Contains(user.Text, "the following 2 Inbox messages") || user.Run.Status != "completed" {
		t.Fatalf("重开会话后的提醒：%+v", user)
	}
	// Reopening the session with no unread messages schedules no reminder.
	agent := e.agentCaller(dev.ID)
	for _, s := range decode[apitypes.InboxMessagePage](t, agent.expect(200, "GET", "/inbox/messages?unread=true", nil)).Messages {
		agent.expect(200, "POST", "/inbox/messages/"+s.ID.String()+"/read", nil)
	}
	e.expect(200, "POST", a+"/session/reset", nil)
	waitIdle(t, e, a)
	if _, triggers := reminderRuns(t, e, dev.ID); len(triggers) != 1 {
		t.Fatalf("没有未读消息时重开会话不应安排提醒：%v", triggers)
	}
}
