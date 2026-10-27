package store_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

type promoteFixture struct {
	t       *testing.T
	s       *store.Store
	svc     *tasks.Service
	calls   *taskInboxInterruptRecorder
	task    uuid.UUID
	node    uuid.UUID
	agent   uuid.UUID
	runtime uuid.UUID
	active  store.TaskTurn
}

func newPromoteFixture(t *testing.T, imageLimit int) *promoteFixture {
	t.Helper()
	ctx := context.Background()
	s := taskConsoleStore(t)
	s.ImageMaxCount = imageLimit
	task, node, agent, initial := taskConsoleRecord(t, s)
	f := &promoteFixture{t: t, s: s, calls: &taskInboxInterruptRecorder{}, task: task, node: node, agent: agent, runtime: initial.RuntimeID}
	f.svc = tasks.New(s, nil, f.calls, tasks.Options{}, quietLogger())
	active, ok, e := s.ClaimTaskTurn(ctx, task, initial.RuntimeID)
	if e != nil || !ok {
		t.Fatal("claim", e)
	}
	if e = s.MarkTaskRunning(ctx, active, 1); e != nil {
		t.Fatal(e)
	}
	f.active = active
	return f
}

func (f *promoteFixture) image() uuid.UUID {
	f.t.Helper()
	id := uuid.Must(uuid.NewV7())
	data := []byte("synthetic-image-" + id.String())
	if _, e := f.s.SaveMessageImage(context.Background(), id, store.ImageOwner{AgentID: f.agent, TaskID: f.task, NodeRunID: f.node, RuntimeID: f.runtime}, messageimage.Ref{ID: id, MimeType: "image/png", Size: int64(len(data)), Width: 1, Height: 1}, data); e != nil {
		f.t.Fatal(e)
	}
	return id
}

func (f *promoteFixture) send(text, whenBusy string, images ...uuid.UUID) uuid.UUID {
	f.t.Helper()
	raw, e := f.svc.SendNodeInboxMessage(context.Background(), auth.Console(), f.task, f.node, taskmodel.TaskNodeMessageInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime, Text: text, WhenBusy: whenBusy, ImageIDs: images}, 65536)
	if e != nil {
		f.t.Fatal(e)
	}
	return receiptMessage(f.t, inboxReceipt(f.t, raw))
}

func (f *promoteFixture) promote(message, action uuid.UUID) ([]byte, taskmodel.TaskNodeQueueActionReceipt, error) {
	raw, e := f.svc.PromoteNodeInboxMessage(context.Background(), auth.Console(), f.task, f.node, message, taskmodel.TaskNodeQueueActionInput{ActionID: action, ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime})
	var r taskmodel.TaskNodeQueueActionReceipt
	if e == nil {
		if e2 := json.Unmarshal(raw, &r); e2 != nil {
			f.t.Fatal(e2)
		}
	}
	return raw, r, e
}

func (f *promoteFixture) mustPromote(message uuid.UUID) taskmodel.TaskNodeQueueActionReceipt {
	f.t.Helper()
	_, r, e := f.promote(message, uuid.New())
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}

type queueShape struct {
	whenBusy string
	messages []uuid.UUID
	images   int
}

// shape groups the per-message queue items back into the turns that carry them.
func shape(q taskmodel.TaskNodeQueue) []queueShape {
	out := []queueShape{}
	var last uuid.UUID
	for _, it := range q.Items {
		if len(out) == 0 || it.RunID != last {
			out = append(out, queueShape{whenBusy: it.WhenBusy, messages: []uuid.UUID{}})
			last = it.RunID
		}
		g := &out[len(out)-1]
		g.messages = append(g.messages, *it.MessageID)
		g.images += len(it.Images)
	}
	return out
}

func sameShape(a, b []queueShape) bool {
	return slices.EqualFunc(a, b, func(x, y queueShape) bool {
		return x.whenBusy == y.whenBusy && slices.Equal(x.messages, y.messages) && x.images == y.images
	})
}

// queue returns the GET read model and checks that the latest queue event equals it.
func (f *promoteFixture) queue() taskmodel.TaskNodeQueue {
	f.t.Helper()
	q, e := f.svc.NodeQueue(context.Background(), auth.Console(), f.task, f.node)
	if e != nil {
		f.t.Fatal(e)
	}
	var raw []byte
	if e = f.s.Pool.QueryRow(context.Background(), `SELECT data FROM task_events WHERE task_id=$1 AND type='task.node.queue.updated' ORDER BY id DESC LIMIT 1`, f.task).Scan(&raw); e != nil {
		f.t.Fatal("queue event", e)
	}
	var ev taskmodel.TaskNodeQueue
	if e = json.Unmarshal(raw, &ev); e != nil {
		f.t.Fatal(e)
	}
	if !sameShape(shape(q), shape(ev)) || q.RuntimeID != f.runtime || ev.NodeRunID != f.node {
		f.t.Fatalf("最新的 task.node.queue.updated 与读取结果不一致：%+v / %+v", ev, q)
	}
	return q
}

func (f *promoteFixture) turn(id uuid.UUID) store.TaskTurn {
	f.t.Helper()
	var status string
	var void *string
	if e := f.s.Pool.QueryRow(context.Background(), `SELECT status,void_reason FROM task_turns WHERE id=$1`, id).Scan(&status, &void); e != nil {
		f.t.Fatal(e)
	}
	return store.TaskTurn{ID: id, Status: status, VoidReason: void}
}

func TestTaskNodeInboxPromoteSplitMergeVoidAndClaimOrder(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	m1, m2, m3 := f.send("一", "queue"), f.send("二", "queue"), f.send("三", "queue")
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"queue", []uuid.UUID{m1, m2, m3}, 0}}) {
		t.Fatalf("三条普通消息应合并在一组：%+v", q)
	}
	normal := q.Items[0].RunID

	// 一组里拆出一条：新建打断组排在前面，并打断当前回复。
	r2 := f.mustPromote(m2)
	if r2.Status != "accepted" || r2.RunID == normal || len(f.calls.calls) != 1 || f.calls.calls[0] != [2]uuid.UUID{f.runtime, f.active.ID} {
		t.Fatalf("拆出：%+v 打断 %v", r2, f.calls.calls)
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m2}, 0}, {"queue", []uuid.UUID{m1, m3}, 0}}) {
		t.Fatalf("拆出后的排队：%+v", got)
	}
	// 并入已有打断组；当前回复已在打断中，不再重复打断。
	r1 := f.mustPromote(m1)
	if r1.RunID != r2.RunID || len(f.calls.calls) != 1 {
		t.Fatalf("应并入已有打断组：%+v %v", r1, f.calls.calls)
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m1, m2}, 0}, {"queue", []uuid.UUID{m3}, 0}}) {
		t.Fatalf("并入后的排队：%+v", got)
	}
	// 拆空：原组作废并标记 merged_into_interrupt，web 据此隐藏。
	action := uuid.New()
	raw3, r3, e := f.promote(m3, action)
	if e != nil || r3.RunID != r2.RunID {
		t.Fatal("拆空", e)
	}
	if v := f.turn(normal); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "merged_into_interrupt" {
		t.Fatalf("拆空的组应作废：%+v", v)
	}
	page, e := f.s.TaskNodeMessages(ctx, f.task, f.node, "", 100)
	if e != nil {
		t.Fatal(e)
	}
	var hidden bool
	for _, m := range page.Messages {
		if m.RunID == normal && m.VoidReason != nil && *m.VoidReason == "merged_into_interrupt" {
			hidden = true
		}
		if m.RunID != normal && m.VoidReason != nil {
			t.Fatalf("其他轮次不应带 voidReason：%+v", m)
		}
	}
	if !hidden {
		t.Fatal("作废组的 TaskMessage 缺少 voidReason")
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{m1, m2, m3}, 0}}) {
		t.Fatalf("拆空后的排队：%+v", got)
	}
	// 同一 actionId 重放返回原响应；已是打断的消息用新 actionId 返回 already_interrupt；都不再打断。
	again, _, e := f.promote(m3, action)
	if e != nil || string(again) != string(raw3) {
		t.Fatal("同一动作重放应返回原响应", e)
	}
	if _, r, e := f.promote(m3, uuid.New()); e != nil || r.Status != "already_interrupt" || r.RunID != r2.RunID || len(f.calls.calls) != 1 {
		t.Fatalf("已是打断应幂等：%+v %v", r, e)
	}
	var whenBusy []string
	rows, e := f.s.Pool.Query(ctx, `SELECT when_busy FROM inbox_messages WHERE id=ANY($1) ORDER BY created_at`, []uuid.UUID{m1, m2, m3})
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var w string
		_ = rows.Scan(&w)
		whenBusy = append(whenBusy, w)
	}
	rows.Close()
	var promoted int
	if e = f.s.Pool.QueryRow(ctx, `SELECT count(*) FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.promoted'`, r2.RunID).Scan(&promoted); e != nil {
		t.Fatal(e)
	}
	if !slices.Equal(whenBusy, []string{"interrupt", "interrupt", "interrupt"}) || promoted != 3 {
		t.Fatalf("消息 whenBusy 与 run.promoted 记录：%v %d", whenBusy, promoted)
	}

	// 实际领取顺序与排队列表一致；领取后列表清空并推送空列表。
	if _, e = f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	claimed, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
	if e != nil || !ok || claimed.ID != r2.RunID {
		t.Fatalf("应先领取打断组：%v %v", claimed.ID, e)
	}
	if got := f.queue(); len(got.Items) != 0 {
		t.Fatalf("领取后排队应为空：%+v", got)
	}
	// 已被领取：冲突并带上所在轮次与状态。
	_, _, e = f.promote(m1, uuid.New())
	ae, isApp := apperr.As(e)
	if !isApp || ae.Code != apperr.CodeTaskConflict {
		t.Fatalf("已领取应冲突：%v", e)
	}
	d := ae.Details.(taskmodel.TaskNodeMessageConflictDetails)
	if d.Reason != "message_dispatched" || d.MessageID == nil || *d.MessageID != m1 || d.RunID == nil || *d.RunID != r2.RunID || d.RunStatus == nil || *d.RunStatus != "starting" {
		t.Fatalf("冲突详情：%+v", d)
	}
}

func TestTaskNodeInboxPromoteQueueOrderMatchesClaims(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	a := f.send("a", "queue")
	b := f.send("b", "interrupt") // 新建打断组并打断当前回复
	c := f.send("c", "queue")     // 并入 a 所在普通组
	f.mustPromote(a)              // 并入 b 所在打断组
	d := f.send("d", "queue")     // 并入 c 所在普通组
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"interrupt", []uuid.UUID{a, b}, 0}, {"queue", []uuid.UUID{c, d}, 0}}) {
		t.Fatalf("排队列表：%+v", shape(q))
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	var order []uuid.UUID
	for range shape(q) {
		turn, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
		if e != nil || !ok {
			t.Fatal("claim", e)
		}
		order = append(order, turn.ID)
		if _, e = f.s.UpdateTaskTurn(ctx, f.task, turn.ID, "completed", "", nil, nil); e != nil {
			t.Fatal(e)
		}
	}
	if !slices.Equal(order, []uuid.UUID{q.Items[0].RunID, q.Items[2].RunID}) {
		t.Fatalf("实际领取顺序与排队列表不一致：%v / %+v", order, q.Items)
	}
}

func TestTaskNodeInboxPromoteImageLimit(t *testing.T) {
	f := newPromoteFixture(t, 2)
	i0 := f.send("打断带一图", "interrupt", f.image())
	ma := f.send("普通两图", "queue", f.image(), f.image())
	mb := f.send("普通一图", "queue", f.image()) // 普通组已满两图，另开一组
	q := f.queue()
	if !sameShape(shape(q), []queueShape{{"interrupt", []uuid.UUID{i0}, 1}, {"queue", []uuid.UUID{ma}, 2}, {"queue", []uuid.UUID{mb}, 1}}) {
		t.Fatalf("初始排队：%+v", shape(q))
	}
	// 1+2 超过上限：不能并入已有打断组，新建打断组排在它之后；原普通组拆空作废。
	ra := f.mustPromote(ma)
	if ra.RunID == q.Items[0].RunID {
		t.Fatal("超过图片上限仍并入了已有打断组")
	}
	if v := f.turn(q.Items[1].RunID); v.VoidReason == nil {
		t.Fatal("拆空的普通组应作废")
	}
	// 1+1 不超上限：并入最早的打断组。
	rb := f.mustPromote(mb)
	if rb.RunID != q.Items[0].RunID {
		t.Fatal("应并入最早、仍有余量的打断组")
	}
	if got := shape(f.queue()); !sameShape(got, []queueShape{{"interrupt", []uuid.UUID{i0, mb}, 2}, {"interrupt", []uuid.UUID{ma}, 2}}) {
		t.Fatalf("图片上限下的排队：%+v", got)
	}
}

func TestTaskNodeInboxPromoteRejects(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	m := f.send("普通", "queue")
	if _, _, e := f.promote(uuid.New(), uuid.New()); !apperr.Is(e, apperr.CodeNotFound) {
		t.Fatalf("不存在的消息应 404：%v", e)
	}
	_, e := f.svc.PromoteNodeInboxMessage(ctx, auth.Console(), f.task, f.node, m, taskmodel.TaskNodeQueueActionInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: uuid.New()})
	if ae, ok := apperr.As(e); !ok || ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason != "target_mismatch" {
		t.Fatalf("目标不一致应冲突：%v", e)
	}
	if _, e = f.s.Pool.Exec(ctx, `UPDATE task_node_inbox_messages SET not_executed_reason='node_changed',not_executed_at=now() WHERE message_id=$1`, m); e != nil {
		t.Fatal(e)
	}
	_, _, e = f.promote(m, uuid.New())
	if ae, ok := apperr.As(e); !ok || ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason != "message_not_executed" {
		t.Fatalf("已失效的消息应冲突：%v", e)
	}
	if len(f.calls.calls) != 0 {
		t.Fatal("被拒绝的请求不应打断")
	}
	if _, e = f.svc.PromoteNodeInboxMessage(ctx, auth.Principal{Kind: auth.KindAgent, ID: f.agent}, f.task, f.node, m, taskmodel.TaskNodeQueueActionInput{ActionID: uuid.New(), ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime}); !apperr.Is(e, apperr.CodeForbidden) {
		t.Fatalf("非控制台应 403：%v", e)
	}
}
