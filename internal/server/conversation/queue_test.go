package conversation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// sender returns a second agent of the project that sends Inbox messages.
func (e *env) sender() uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT id FROM agents WHERE name='sender'`).Scan(&id); err == nil {
		return id
	}
	a, err := e.st.GetAgent(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := e.st.CreateAgent(context.Background(), store.ProjectScope(*a.ProjectID), "sender", "anthropic", "m", "")
	if err != nil {
		e.t.Fatal(err)
	}
	return s.ID
}

// deliver sends an Inbox message from the sender agent, as the Inbox service does.
func (e *env) deliver(text, whenBusy string) uuid.UUID {
	e.t.Helper()
	id := uuid.Must(uuid.NewV7())
	msg := store.NewInboxMessage{ID: id, From: store.AgentParty(e.sender()), To: store.AgentParty(e.agent), Text: text, WhenBusy: whenBusy}
	if err := e.svc.DeliverInbox(context.Background(), msg); err != nil {
		e.t.Fatalf("投递 %q 失败：%v", text, err)
	}
	return id
}

// queueNames describes the queue: console runs by text, Inbox messages as "inbox:<text>".
func queueNames(items []apitypes.QueueItem) []string {
	out := []string{}
	for _, it := range items {
		name := it.Text
		if it.Kind == "inbox" {
			name = "inbox:" + it.Text
		}
		if it.WhenBusy == "interrupt" {
			name += "!"
		}
		out = append(out, name)
	}
	return out
}

func (e *env) queueNow() []string {
	e.t.Helper()
	q, err := e.svc.Queue(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	return queueNames(q.Items)
}

// queueViews returns GET, snapshot and the newest queue.updated items.
func (e *env) queueViews() (get, snap, event []string) {
	e.t.Helper()
	ctx := context.Background()
	get = e.queueNow()
	s, _, err := e.svc.Snapshot(ctx, e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	snap = queueNames(s.QueueItems)
	msgs, err := e.rdb.XRevRangeN(ctx, e.co.Keys().Events(e.agent.String()), "+", "-", 500).Result()
	if err != nil {
		e.t.Fatal(err)
	}
	for _, m := range msgs {
		if m.Values["type"] != apitypes.EventQueueUpdated {
			continue
		}
		var ev apitypes.SseQueueUpdated
		if err := json.Unmarshal([]byte(m.Values["data"].(string)), &ev); err != nil {
			e.t.Fatal(err)
		}
		return get, snap, queueNames(ev.Items)
	}
	return get, snap, nil
}

func (e *env) expectQueue(want ...string) {
	e.t.Helper()
	if want == nil {
		want = []string{}
	}
	get, snap, event := e.queueViews()
	if !slices.Equal(get, want) || !slices.Equal(snap, want) || !slices.Equal(event, want) {
		e.t.Fatalf("排队列表应为 %v：接口 %v 快照 %v 事件 %v", want, get, snap, event)
	}
}

type queueAct func(ctx context.Context, agentID, subject, actionID uuid.UUID) (json.RawMessage, error)

// act runs one queue action and decodes the receipt.
func (e *env) act(do queueAct, subject, action uuid.UUID) (apitypes.AgentQueueActionReceipt, json.RawMessage, error) {
	raw, err := do(context.Background(), e.agent, subject, action)
	var r apitypes.AgentQueueActionReceipt
	if err == nil {
		if err := json.Unmarshal(raw, &r); err != nil {
			e.t.Fatal(err)
		}
	}
	return r, raw, err
}

func (e *env) mustAct(do queueAct, subject uuid.UUID) apitypes.AgentQueueActionReceipt {
	e.t.Helper()
	r, _, err := e.act(do, subject, uuid.New())
	if err != nil {
		e.t.Fatalf("排队动作失败：%v", err)
	}
	return r
}

func (e *env) runOf(id uuid.UUID) store.Run {
	e.t.Helper()
	r, err := e.st.GetRun(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) carrier(message uuid.UUID) uuid.UUID {
	e.t.Helper()
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT reminder_run_id FROM inbox_messages WHERE id=$1`, message).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) eventCount(kind string) int {
	e.t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_events WHERE agent_id=$1 AND type=$2`, e.agent, kind).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) executed(substr string) int {
	n := 0
	for _, m := range e.ex.messages() {
		if strings.Contains(m, substr) {
			n++
		}
	}
	return n
}

func (e *env) redisHas(id uuid.UUID) bool {
	e.t.Helper()
	qi, qn, err := e.co.QueueContents(context.Background(), e.agent.String())
	if err != nil {
		e.t.Fatal(err)
	}
	return slices.Contains(qi, id.String()) || slices.Contains(qn, id.String())
}

func drainStarted(ch chan string) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func (e *env) read(message uuid.UUID) store.InboxReadEffect {
	e.t.Helper()
	_, eff, err := e.st.MarkInboxRead(context.Background(), message, store.AgentParty(e.agent))
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc.InboxRead(context.Background(), eff)
	return eff
}

// 逐条列出：控制台消息每条一项，提醒承载的 Inbox 消息每条一项，顺序与实际处理顺序一致；
// 提醒开始时只点名它承载的消息，其余未读消息说明稍后另行提醒。
func TestQueueListsEachMessageInProcessingOrder(t *testing.T) {
	e := newEnv(t)
	e.mustSend("hold 当前", "")
	e.waitStarted("hold 当前")
	e.mustSend("普通1", "queue")
	a := e.deliver("收件A", "queue")
	b := e.deliver("收件B", "queue")
	c := e.deliver("收件C", "interrupt")
	e.expectQueue("inbox:收件C!", "普通1", "inbox:收件A", "inbox:收件B")
	if e.carrier(a) != e.carrier(b) || e.carrier(a) == e.carrier(c) {
		t.Fatal("同优先级的消息应并入同一轮提醒，打断消息另起一轮")
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	got := e.ex.messages()
	if len(got) != 4 || !strings.Contains(got[1], c.String()) || strings.Contains(got[1], a.String()) || !strings.Contains(got[1], "另有 2 条未读消息") ||
		got[2] != "普通1" || !strings.Contains(got[3], a.String()) || !strings.Contains(got[3], b.String()) || !strings.Contains(got[3], "sender") {
		t.Fatalf("执行顺序或提醒点名不对：%q", got)
	}
	e.expectQueue()
}

// 逐条转为打断：消息移到打断提醒（没有就新建），原提醒空了即作废；同一 actionId 重放返回首次回执且没有副作用。
func TestPromoteInboxMessageMovesItAndVoidsEmptiedReminder(t *testing.T) {
	e := newEnv(t)
	cur := e.mustSend("hold 当前", "")
	e.waitStarted("hold 当前")
	a := e.deliver("收件A", "queue")
	b := e.deliver("收件B", "queue")
	normal := e.carrier(a)
	action := uuid.New()
	r, raw, err := e.act(e.svc.PromoteInboxMessage, b, action)
	if err != nil || r.Status != "accepted" || r.Item == nil || *r.Item.MessageID != b || r.Item.WhenBusy != "queue" || r.Run == nil || r.Operation != apitypes.QueuePromoteInboxMessage {
		t.Fatalf("转换回执：%+v %v", r, err)
	}
	interrupt := r.Run.ID
	if interrupt == normal || e.carrier(b) != interrupt || e.runOf(normal).Status != "queued" {
		t.Fatal("应新建打断提醒承载 B，原提醒仍承载 A")
	}
	e.expectQueue("inbox:收件B!", "inbox:收件A")
	var by string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->>'byRunId' FROM lifecycle_events WHERE run_id=$1 AND type=$2`, cur.Run.ID, store.EventRunAbortRequested).Scan(&by); err != nil || by != interrupt.String() {
		t.Fatalf("应打断当前回复：%q %v", by, err)
	}
	aborts, promoted := e.eventCount(store.EventRunAbortRequested), e.eventCount(store.EventRunPromoted)
	if _, again, err := e.act(e.svc.PromoteInboxMessage, b, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("同一 actionId 重放应返回首次回执：%v", err)
	}
	if _, _, err := e.act(e.svc.PromoteInboxMessage, a, action); !apperr.Is(err, apperr.CodeActionConflict) {
		t.Fatalf("同一 actionId 换对象应 action_conflict：%v", err)
	}
	if r := e.mustAct(e.svc.PromoteInboxMessage, b); r.Status != "already_interrupt" {
		t.Fatalf("已经是打断：%+v", r)
	}
	if e.eventCount(store.EventRunAbortRequested) != aborts || e.eventCount(store.EventRunPromoted) != promoted {
		t.Fatal("重放与 already_interrupt 不应再打断或记录转换")
	}
	if r := e.mustAct(e.svc.PromoteInboxMessage, a); r.Run.ID != interrupt {
		t.Fatalf("A 应并入已有的打断提醒：%+v", r)
	}
	if v := e.runOf(normal); v.Status != "interrupted" || v.EndReason != "merged_into_interrupt" || e.redisHas(normal) {
		t.Fatalf("拆空的提醒应作废并移出 Redis：%s %s", v.Status, v.EndReason)
	}
	e.expectQueue("inbox:收件A!", "inbox:收件B!")
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 2 || !strings.Contains(got[1], a.String()) || !strings.Contains(got[1], b.String()) {
		t.Fatalf("打断提醒应点名 A 与 B 且只执行一次：%q", got)
	}
	var promotedMsg string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->>'messageId' FROM lifecycle_events WHERE run_id=$1 AND type=$2 ORDER BY at LIMIT 1`, interrupt, store.EventRunPromoted).Scan(&promotedMsg); err != nil || promotedMsg != b.String() {
		t.Fatalf("promoted 事件应带 messageId：%q %v", promotedMsg, err)
	}
	_, _, err = e.act(e.svc.PromoteInboxMessage, a, uuid.New())
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued || ae.Details.(store.RunNotQueuedDetails).MessageState == "" {
		t.Fatalf("已处理的消息应 run_not_queued：%v", err)
	}
}

// 撤回控制台消息与取消 Inbox 消息：Agent 不会收到；回执带原文；承载提醒空了即作废；已取消的消息不计为未读。
func TestCancelQueuedRunAndInboxMessage(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.mustSend("hold 当前", "")
	e.waitStarted("hold 当前")
	mine := e.mustSend("撤回我", "queue")
	action := uuid.New()
	r, raw, err := e.act(e.svc.CancelRun, mine.Run.ID, action)
	if err != nil || r.Status != "accepted" || r.Item == nil || r.Item.Text != "撤回我" || r.Item.Kind != "run" || r.Run.Status != "interrupted" || r.Run.EndReason == nil || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("撤回回执：%+v %v", r, err)
	}
	if e.redisHas(mine.Run.ID) {
		t.Fatal("撤回的执行应移出 Redis 队列")
	}
	if _, again, err := e.act(e.svc.CancelRun, mine.Run.ID, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("重放应返回首次回执：%v", err)
	}
	if r := e.mustAct(e.svc.CancelRun, mine.Run.ID); r.Status != "already_cancelled" {
		t.Fatalf("已经撤回：%+v", r)
	}
	a := e.deliver("收件A", "queue")
	b := e.deliver("收件B", "queue")
	rem := e.carrier(a)
	if r := e.mustAct(e.svc.CancelInboxMessage, a); r.Status != "accepted" || r.Item.Text != "收件A" || r.Run.Status != "queued" {
		t.Fatalf("取消 A：%+v", r)
	}
	if n, _ := e.st.CountInboxUnread(ctx, store.AgentParty(e.agent)); n != 1 {
		t.Fatalf("已取消的消息不计为未读：%d", n)
	}
	e.expectQueue("inbox:收件B")
	if r := e.mustAct(e.svc.CancelInboxMessage, b); r.Run.Status != "interrupted" || *r.Run.EndReason != "user_cancelled" {
		t.Fatalf("取消最后一条后提醒应作废：%+v", r.Run)
	}
	if r := e.mustAct(e.svc.CancelInboxMessage, b); r.Status != "already_cancelled" {
		t.Fatalf("已经取消：%+v", r)
	}
	e.expectQueue()
	if e.redisHas(rem) || e.eventCount(store.EventMessageCancelled) != 2 {
		t.Fatal("作废的提醒应移出 Redis，每条取消记一条事件")
	}
	m, err := e.st.GetInboxMessage(ctx, a, 200)
	if err != nil || m.CancelledAt == nil || m.ReadAt != nil {
		t.Fatalf("消息应记为已取消：%+v %v", m, err)
	}
	store.RedactCancelledFor(store.AgentParty(e.agent), &m)
	if m.Text != store.CancelledNotice || m.Preview != store.CancelledNotice {
		t.Fatalf("收件 Agent 只看到取消说明：%q", m.Text)
	}
	full, _ := e.st.GetInboxMessage(ctx, a, 200)
	store.RedactCancelledFor(store.AgentParty(e.sender()), &full)
	if full.Text != "收件A" {
		t.Fatal("发件方看到全文")
	}
	if changed, _, err := e.st.MarkInboxRead(ctx, a, store.AgentParty(e.agent)); err != nil || changed {
		t.Fatalf("已取消的消息不会变为已读：%v %v", changed, err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 1 {
		t.Fatalf("撤回和取消的都不应执行：%q", got)
	}
}

// 读后离队：收件 Agent 读走的消息立即离开列表；提醒没有待处理消息时随即作废，不再等到开始时跳过。
func TestReadLeavesQueueAndVoidsReminder(t *testing.T) {
	e := newEnv(t)
	e.mustSend("hold 当前", "")
	e.waitStarted("hold 当前")
	a := e.deliver("收件A", "queue")
	b := e.deliver("收件B", "queue")
	rem := e.carrier(a)
	if eff := e.read(a); !eff.QueueChanged || len(eff.Voided) != 0 {
		t.Fatalf("读走 A：%+v", eff)
	}
	e.expectQueue("inbox:收件B")
	if eff := e.read(b); !eff.QueueChanged || !slices.Equal(eff.Voided, []uuid.UUID{rem}) {
		t.Fatalf("读走最后一条应作废提醒：%+v", eff)
	}
	if r := e.runOf(rem); r.Status != "interrupted" || r.EndReason != "no_pending_messages" || e.redisHas(rem) {
		t.Fatalf("作废的提醒：%s %s", r.Status, r.EndReason)
	}
	e.expectQueue()
	// 兜底：绕过读取路径直接标为已读，提醒开始时发现没有待处理消息，作废而不启动 Pi。
	c := e.deliver("收件C", "queue")
	late := e.carrier(c)
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE inbox_messages SET read_at=now() WHERE id=$1`, c); err != nil {
		t.Fatal(err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if r := e.runOf(late); r.Status != "interrupted" || r.EndReason != "no_pending_messages" || len(e.ex.messages()) != 1 {
		t.Fatalf("开始时没有待处理消息应作废：%s %s %q", r.Status, r.EndReason, e.ex.messages())
	}
}

// 停止只作用于这一次回复：以 user_stopped 结束，之后排队的消息照常处理；已结束时返回 run_not_active。
func TestStopOnlyStopsThisReplyAndQueueContinues(t *testing.T) {
	e := newEnv(t)
	cur := e.mustSend("block 当前", "")
	e.waitStarted("block 当前")
	next := e.mustSend("普通1", "queue")
	if _, _, err := e.act(e.svc.StopRun, next.Run.ID, uuid.New()); !apperr.Is(err, apperr.CodeRunNotActive) {
		t.Fatalf("排队中的执行不能停止：%v", err)
	}
	action := uuid.New()
	r, raw, err := e.act(e.svc.StopRun, cur.Run.ID, action)
	if err != nil || r.Status != "accepted" || r.Item != nil || r.Run.ID != cur.Run.ID {
		t.Fatalf("停止回执：%+v %v", r, err)
	}
	e.waitIdleOrDump(names{})
	if v := e.runOf(cur.Run.ID); v.Status != "interrupted" || v.EndReason != "user_stopped" {
		t.Fatalf("被停止的回复：%s %s", v.Status, v.EndReason)
	}
	if v := e.runOf(next.Run.ID); v.Status != "completed" || e.executed("普通1") != 1 {
		t.Fatalf("停止后队列应继续：%s", v.Status)
	}
	if _, again, err := e.act(e.svc.StopRun, cur.Run.ID, action); err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("重放应返回首次回执：%v", err)
	}
	_, _, err = e.act(e.svc.StopRun, cur.Run.ID, uuid.New())
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotActive || ae.Details.(store.RunNotActiveDetails).Status != "interrupted" {
		t.Fatalf("已结束的回复应 run_not_active：%v", err)
	}
	var stop string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT data->>'stop' FROM lifecycle_events WHERE run_id=$1 AND type=$2`, cur.Run.ID, store.EventRunAbortRequested).Scan(&stop); err != nil || stop != "true" {
		t.Fatalf("abort_requested 应带 stop：%q %v", stop, err)
	}
}

// 撤回与出队并发：同一条消息要么被撤回且从未执行，要么已开始（撤回得到 run_not_queued）且只执行一次。
func TestCancelRacesDequeueExactlyOnce(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block 当前" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		q := e.mustSend("排队"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var cancelErr error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, cancelErr = e.act(e.svc.CancelRun, q.Run.ID, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		r := e.runOf(q.Run.ID)
		ran := e.executed("排队" + string(rune('a'+i)))
		switch {
		case cancelErr == nil && r.EndReason == "user_cancelled" && ran == 0:
		case apperr.Is(cancelErr, apperr.CodeRunNotQueued) && r.Status == "completed" && ran == 1:
		default:
			t.Fatalf("第 %d 轮：撤回 %v，状态 %s/%s，执行 %d 次", i, cancelErr, r.Status, r.EndReason, ran)
		}
	}
}

// 逐条转换与提醒开始并发：消息恰好被处理一次（由原提醒或打断提醒点名），没有丢失或重复。
func TestPromoteMessageRacesReminderStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block 当前" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		m := e.deliver("收件"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var err error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, err = e.act(e.svc.PromoteInboxMessage, m, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		if err != nil && !apperr.Is(err, apperr.CodeRunNotQueued) {
			t.Fatalf("第 %d 轮：%v", i, err)
		}
		if n := e.executed(m.String()); n != 1 {
			t.Fatalf("第 %d 轮：消息被点名执行 %d 次（转换 %v）", i, n, err)
		}
		var open int
		if e2 := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND status='queued'`, e.agent).Scan(&open); e2 != nil || open != 0 {
			t.Fatalf("第 %d 轮：仍有排队执行 %d", i, open)
		}
		// 本轮已处理，标为已读，避免下一轮的提醒统计到它。
		e.read(m)
	}
}

// 取消与收件 Agent 读取并发：恰好一方生效，消息不会既已读又已取消。
func TestCancelRacesRead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.mustSend("hold 当前", "")
	e.waitStarted("hold 当前")
	for i := range 20 {
		m := e.deliver("收件"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var cancelErr, readErr error
		var readChanged bool
		wg.Go(func() { _, _, cancelErr = e.act(e.svc.CancelInboxMessage, m, uuid.New()) })
		wg.Go(func() {
			var eff store.InboxReadEffect
			readChanged, eff, readErr = e.st.MarkInboxRead(ctx, m, store.AgentParty(e.agent))
			e.svc.InboxRead(ctx, eff)
		})
		wg.Wait()
		if readErr != nil {
			t.Fatal(readErr)
		}
		v, err := e.st.GetInboxMessage(ctx, m, 200)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case cancelErr == nil && !readChanged && v.CancelledAt != nil && v.ReadAt == nil:
		case apperr.Is(cancelErr, apperr.CodeRunNotQueued) && readChanged && v.ReadAt != nil && v.CancelledAt == nil:
		default:
			t.Fatalf("第 %d 轮：取消 %v 读取 %v，消息 read=%v cancelled=%v", i, cancelErr, readChanged, v.ReadAt, v.CancelledAt)
		}
	}
	e.expectQueue()
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if got := e.ex.messages(); len(got) != 1 {
		t.Fatalf("全部消息已读或取消，提醒不应执行：%q", got)
	}
}

// 读后作废与提醒开始并发：提醒要么在读取时作废，要么开始后因没有待处理消息作废，要么照常执行一次；不会卡在队列里。
func TestReadVoidRacesReminderStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := "block 当前" + string(rune('a'+i))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		m := e.deliver("收件"+string(rune('a'+i)), "queue")
		rem := e.carrier(m)
		var wg sync.WaitGroup
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { e.read(m) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		r := e.runOf(rem)
		ran := e.executed(m.String())
		switch {
		case r.Status == "interrupted" && r.EndReason == "no_pending_messages" && ran == 0:
		case r.Status == "completed" && ran == 1:
		default:
			t.Fatalf("第 %d 轮：提醒 %s/%s，执行 %d 次", i, r.Status, r.EndReason, ran)
		}
	}
}

// 停止与回复结束、下一条开始并发：停止只作用于路径中的那一次执行，下一条从不被停止。
func TestStopRacesFinishAndNextStart(t *testing.T) {
	e := newEnv(t)
	for i := range 15 {
		cur := e.mustSend("block 当前"+string(rune('a'+i)), "queue")
		e.waitStarted("block 当前" + string(rune('a'+i)))
		next := e.mustSend("下一条"+string(rune('a'+i)), "queue")
		var wg sync.WaitGroup
		var err error
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() { _, _, err = e.act(e.svc.StopRun, cur.Run.ID, uuid.New()) })
		wg.Wait()
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		c, n := e.runOf(cur.Run.ID), e.runOf(next.Run.ID)
		if n.Status != "completed" || n.AbortRequestedAt != nil {
			t.Fatalf("第 %d 轮：下一条不应被停止：%s", i, n.Status)
		}
		switch {
		case err == nil && (c.EndReason == "user_stopped" || c.Status == "completed"):
		case apperr.Is(err, apperr.CodeRunNotActive) && c.Status == "completed":
		default:
			t.Fatalf("第 %d 轮：停止 %v，当前回复 %s/%s", i, err, c.Status, c.EndReason)
		}
	}
}

// 收件 Agent 在回复中读取 Inbox 与执行结束、开始并发：读取路径先锁 Agent 行，与 FinishRun 同一加锁顺序，不会死锁。
func TestReadsConcurrentWithFinishRun(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	for round := range 5 {
		cur := "block 当前" + string(rune('a'+round))
		e.mustSend(cur, "queue")
		e.waitStarted(cur)
		msgs := []uuid.UUID{}
		for i := range 8 {
			msgs = append(msgs, e.deliver("收件"+string(rune('a'+round))+string(rune('0'+i)), "queue"))
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(msgs)+2)
		for _, m := range msgs {
			wg.Go(func() {
				_, eff, err := e.st.MarkInboxRead(ctx, m, store.AgentParty(e.agent))
				e.svc.InboxRead(ctx, eff)
				errs <- err
			})
		}
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Go(func() {
			_, err := e.send("并发"+string(rune('a'+round)), "queue")
			errs <- err
		})
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("第 %d 轮：%v", round, err)
			}
		}
		e.waitIdleOrDump(names{})
		drainStarted(e.ex.started)
		var unread int
		if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE to_agent_id=$1 AND read_at IS NULL`, e.agent).Scan(&unread); err != nil || unread != 0 {
			t.Fatalf("第 %d 轮：读取应全部生效：%d %v", round, unread, err)
		}
	}
}
