package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
)

// deliverFrom sends an ordinary Inbox message from another project Agent to
// the node's task instance (agent#task), as the Inbox service does.
func (f *promoteFixture) deliverFrom(text, whenBusy string) uuid.UUID {
	f.t.Helper()
	ctx := context.Background()
	var other uuid.UUID
	if e := f.s.Pool.QueryRow(ctx, `SELECT id FROM agents WHERE name='外部发件人'`).Scan(&other); e != nil {
		var project uuid.UUID
		if e = f.s.Pool.QueryRow(ctx, `SELECT project_id FROM task_records WHERE id=$1`, f.task).Scan(&project); e != nil {
			f.t.Fatal(e)
		}
		a, e := f.s.CreateAgent(ctx, store.ProjectScope(project), "外部发件人", "anthropic", "m", "")
		if e != nil {
			f.t.Fatal(e)
		}
		other = a.ID
	}
	id := uuid.Must(uuid.NewV7())
	if e := f.svc.DeliverInbox(ctx, store.NewInboxMessage{ID: id, From: store.AgentParty(other), To: store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}, Text: text, WhenBusy: whenBusy}); e != nil {
		f.t.Fatal(e)
	}
	return id
}

func (f *promoteFixture) input(action uuid.UUID) taskmodel.TaskNodeQueueActionInput {
	return taskmodel.TaskNodeQueueActionInput{ActionID: action, ExpectedAgentID: f.agent, ExpectedRuntimeID: f.runtime}
}

func (f *promoteFixture) cancel(message, action uuid.UUID) ([]byte, taskmodel.TaskNodeQueueActionReceipt, error) {
	raw, e := f.svc.CancelNodeQueueMessage(context.Background(), auth.Console(), f.task, f.node, message, f.input(action))
	var r taskmodel.TaskNodeQueueActionReceipt
	if e == nil {
		if e2 := json.Unmarshal(raw, &r); e2 != nil {
			f.t.Fatal(e2)
		}
	}
	return raw, r, e
}

func (f *promoteFixture) mustCancel(message uuid.UUID) taskmodel.TaskNodeQueueActionReceipt {
	f.t.Helper()
	_, r, e := f.cancel(message, uuid.New())
	if e != nil {
		f.t.Fatal(e)
	}
	return r
}

func itemMessages(q taskmodel.TaskNodeQueue) []uuid.UUID {
	out := []uuid.UUID{}
	for _, it := range q.Items {
		out = append(out, *it.MessageID)
	}
	return out
}

func conflictReason(t *testing.T, e error) string {
	t.Helper()
	ae, ok := apperr.As(e)
	if !ok || ae.Code != apperr.CodeTaskConflict {
		t.Fatalf("应为 task_conflict：%v", e)
	}
	return ae.Details.(taskmodel.TaskNodeMessageConflictDetails).Reason
}

// 其他 Agent 发给任务实例的消息也逐条列入节点排队，可以逐条转为打断；领取时提醒只点名本轮承载的消息。
func TestTaskNodeQueueListsAgentMessagesAndPromotesThem(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	d1 := f.deliverFrom("外部一", "queue")
	s1 := f.send("控制台一", "queue")
	d2 := f.deliverFrom("外部二", "queue")
	q := f.queue()
	if !slices.Equal(itemMessages(q), []uuid.UUID{d1, d2, s1}) || q.Items[0].RunID != q.Items[1].RunID || q.Items[0].Text != "外部一" || q.Items[2].Kind != "inbox" {
		t.Fatalf("排队列表：%+v", q.Items)
	}
	var from map[string]any
	if e := json.Unmarshal(q.Items[0].From, &from); e != nil || from["kind"] != "agent" || from["name"] != "外部发件人" {
		t.Fatalf("发件人：%s", q.Items[0].From)
	}
	unscoped := q.Items[0].RunID
	r2 := f.mustPromote(d2)
	if r2.Status != "accepted" || r2.Operation != "promote_inbox_message" || r2.Item == nil || r2.Item.Text != "外部二" || r2.RunID == unscoped || len(f.calls.calls) != 1 {
		t.Fatalf("转换外部消息：%+v %v", r2, f.calls.calls)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d2, d1, s1}) {
		t.Fatalf("转换后：%v", got)
	}
	r1 := f.mustPromote(d1)
	if r1.RunID != r2.RunID {
		t.Fatalf("应并入已有的打断提醒：%+v", r1)
	}
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "merged_into_interrupt" {
		t.Fatalf("拆空的轮次应作废：%+v", v)
	}
	q = f.queue()
	if !slices.Equal(itemMessages(q), []uuid.UUID{d1, d2, s1}) || q.Items[0].WhenBusy != "interrupt" || q.Items[2].WhenBusy != "queue" {
		t.Fatalf("合并后：%+v", q.Items)
	}
	if r := f.mustPromote(d1); r.Status != "already_interrupt" {
		t.Fatalf("已经是打断：%+v", r)
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	turn, ok, e := f.s.ClaimTaskTurn(ctx, f.task, f.runtime)
	if e != nil || !ok || turn.ID != r2.RunID {
		t.Fatalf("应先领取打断提醒：%v %v", turn.ID, e)
	}
	if !strings.Contains(turn.Text, d1.String()) || !strings.Contains(turn.Text, d2.String()) || strings.Contains(turn.Text, s1.String()) || !strings.Contains(turn.Text, "另有 1 条未读消息") {
		t.Fatalf("提醒应只点名本轮承载的消息：%s", turn.Text)
	}
}

// 取消节点排队中的消息（控制台发的与其他 Agent 发的）：不再执行、不计为未读；所在轮次拆空即作废。
func TestTaskNodeQueueCancelBothKinds(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	d1 := f.deliverFrom("外部一", "queue")
	s1 := f.send("控制台一", "queue")
	s2 := f.send("控制台二", "queue")
	q := f.queue()
	unscoped, strict := q.Items[0].RunID, q.Items[1].RunID
	action := uuid.New()
	raw, r, e := f.cancel(s1, action)
	if e != nil || r.Status != "accepted" || r.Operation != "cancel_inbox_message" || r.Item == nil || r.Item.Text != "控制台一" || r.RunID != strict {
		t.Fatalf("取消控制台消息：%+v %v", r, e)
	}
	if again, _, e := f.cancel(s1, action); e != nil || !bytes.Equal(again, raw) {
		t.Fatalf("同一 actionId 重放：%v", e)
	}
	if r := f.mustCancel(s1); r.Status != "already_cancelled" {
		t.Fatalf("已经取消：%+v", r)
	}
	var reason *string
	if e := f.s.Pool.QueryRow(ctx, `SELECT not_executed_reason FROM task_node_inbox_messages WHERE message_id=$1`, s1).Scan(&reason); e != nil || reason == nil || *reason != "cancelled" {
		t.Fatalf("严格消息应记 not_executed=cancelled：%v %v", reason, e)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d1, s2}) {
		t.Fatalf("取消后：%v", got)
	}
	f.mustCancel(s2)
	if v := f.turn(strict); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "user_cancelled" {
		t.Fatalf("拆空的严格轮次应作废：%+v", v)
	}
	if _, e := f.s.Pool.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[{"id":"1","label":"好"}]',$2)`, d1, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	f.mustCancel(d1)
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "user_cancelled" {
		t.Fatalf("拆空的提醒轮次应作废：%+v", v)
	}
	var state string
	if e := f.s.Pool.QueryRow(ctx, `SELECT state FROM inbox_questions WHERE message_id=$1`, d1).Scan(&state); e != nil || state != "cancelled" {
		t.Fatalf("问题应变为 cancelled：%q %v", state, e)
	}
	if got := itemMessages(f.queue()); len(got) != 0 {
		t.Fatalf("全部取消后：%v", got)
	}
	if n, _ := f.s.CountInboxUnread(ctx, store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}); n != 0 {
		t.Fatalf("取消的消息不计为未读：%d", n)
	}
	if len(f.calls.calls) != 0 {
		t.Fatal("取消不应打断当前回复")
	}
	if _, e := f.svc.PromoteNodeInboxMessage(ctx, auth.Console(), f.task, f.node, d1, f.input(uuid.New())); conflictReason(t, e) != "message_cancelled" {
		t.Fatal("已取消的消息不能转为打断")
	}
}

// 节点 Agent 在回复中读走排队消息：消息离开排队，所在轮次没有待处理消息即作废（no_pending_messages）。
func TestTaskNodeQueueReadVoidsTurn(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	s1 := f.send("控制台一", "queue")
	d1 := f.deliverFrom("外部一", "queue")
	q := f.queue()
	strict, unscoped := q.Items[0].RunID, q.Items[1].RunID
	me := store.Party{Kind: "agent", ID: f.agent, RuntimeID: f.runtime}
	if _, handled, changed, e := f.s.ReadTaskNodeInbox(ctx, me, s1, f.active.ID); e != nil || !handled || !changed {
		t.Fatalf("读取严格消息：%v %v %v", handled, changed, e)
	}
	if v := f.turn(strict); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "no_pending_messages" {
		t.Fatalf("读走后严格轮次应作废：%+v", v)
	}
	if got := itemMessages(f.queue()); !slices.Equal(got, []uuid.UUID{d1}) {
		t.Fatalf("读走后：%v", got)
	}
	changed, eff, e := f.s.MarkInboxRead(ctx, d1, me)
	if e != nil || !changed || !eff.QueueChanged {
		t.Fatalf("读取外部消息：%v %+v %v", changed, eff, e)
	}
	if v := f.turn(unscoped); v.Status != "interrupted" || v.VoidReason == nil || *v.VoidReason != "no_pending_messages" {
		t.Fatalf("读走后提醒轮次应作废：%+v", v)
	}
	if got := itemMessages(f.queue()); len(got) != 0 {
		t.Fatalf("全部读走后：%v", got)
	}
	if _, e := f.svc.CancelNodeQueueMessage(ctx, auth.Console(), f.task, f.node, d1, f.input(uuid.New())); conflictReason(t, e) != "message_read" {
		t.Fatal("已读的消息不能取消")
	}
}

// 停止节点当前这一轮：置为 aborting 并通知执行器；重复停止为 already_stopping；已结束的轮次返回 run_not_active。
func TestTaskNodeStopMarksOnlyThisTurn(t *testing.T) {
	ctx := context.Background()
	f := newPromoteFixture(t, 0)
	action := uuid.New()
	raw, e := f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(action))
	var r taskmodel.TaskNodeQueueActionReceipt
	if e != nil || json.Unmarshal(raw, &r) != nil || r.Status != "accepted" || r.Operation != "stop_run" || r.RunID != f.active.ID || r.MessageID != nil || r.Item != nil {
		t.Fatalf("停止：%s %v", raw, e)
	}
	if v := f.turn(f.active.ID); v.Status != "aborting" || len(f.calls.calls) != 1 || f.calls.calls[0] != [2]uuid.UUID{f.runtime, f.active.ID} {
		t.Fatalf("应打断这一轮：%+v %v", v, f.calls.calls)
	}
	if again, e := f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(action)); e != nil || !bytes.Equal(again, raw) {
		t.Fatalf("同一 actionId 重放：%v", e)
	}
	raw, e = f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(uuid.New()))
	if e != nil || json.Unmarshal(raw, &r) != nil || r.Status != "already_stopping" || len(f.calls.calls) != 1 {
		t.Fatalf("已在停止中：%s %v", raw, e)
	}
	var stopped bool
	if e := f.s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_events WHERE task_turn_id=$1 AND type='run.abort_requested' AND data->>'stop'='true')`, f.active.ID).Scan(&stopped); e != nil || !stopped {
		t.Fatalf("应记下停止：%v %v", stopped, e)
	}
	if _, e := f.s.UpdateTaskTurn(ctx, f.task, f.active.ID, "interrupted", "", nil, nil); e != nil {
		t.Fatal(e)
	}
	_, e = f.svc.StopNodeRun(ctx, auth.Console(), f.task, f.node, f.active.ID, f.input(uuid.New()))
	if conflictReason(t, e) != "run_not_active" {
		t.Fatal("已结束的轮次不能停止")
	}
}
