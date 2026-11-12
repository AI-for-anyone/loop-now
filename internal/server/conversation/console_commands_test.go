package conversation_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// maintExec 在 fakeExec 上加平台命令用到的维护接口；gate 不为空时 StopIdle 停在那里，直到 gate 关闭。
type maintExec struct {
	*fakeExec
	entered  chan string
	gate     chan struct{}
	listGate chan struct{} // 不为空时 ListSessions 停在那里，直到它关闭
}

func (m *maintExec) StopIdle(ctx context.Context, _ uuid.UUID, reason string) (bool, error) {
	m.entered <- reason
	if m.gate != nil {
		select {
		case <-m.gate:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return true, nil
}

func (m *maintExec) ExportSession(context.Context, uuid.UUID, string, string) (executor.SessionExport, error) {
	return executor.SessionExport{Filename: "s.jsonl", ContentType: "application/x-ndjson", Data: []byte("{}\n")}, nil
}

func (m *maintExec) ListSessions(ctx context.Context, _ uuid.UUID, _ string) ([]executor.ManagedSession, error) {
	if m.listGate != nil {
		m.entered <- "list"
		select {
		case <-m.listGate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []executor.ManagedSession{}, nil
}

func (m *maintExec) ResolveSession(context.Context, uuid.UUID, string, string) (*executor.ManagedSession, error) {
	return nil, nil
}

// newMaintEnv 与 newEnv 相同，但对账一小时一次：平台命令之后的排队消息与提醒只能靠命令自己接着处理。
func newMaintEnv(t *testing.T) (*env, *maintExec) {
	t.Helper()
	st := sqlitetest.NewStore(t)
	co := coord.New(coord.Options{Prefix: "test:", StreamMaxLen: 1000})
	ctx := context.Background()
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	m := &maintExec{fakeExec: newFakeExec(), entered: make(chan string, 10)}
	e := &env{t: t, st: st, co: co, ex: m.fakeExec, agent: a.ID, inst: uuid.Must(uuid.NewV7())}
	e.svc = conversation.New(st, co, m, conversation.Options{InstanceID: e.inst, MaxMessage: 1 << 16, AbortPollInterval: 100 * time.Millisecond,
		ReconcileInterval: time.Hour}, slog.New(slog.NewTextHandler(debugLogs, nil)))
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Stop(context.Background()) })
	return e, m
}

func (e *env) waitAnyStarted() string {
	e.t.Helper()
	select {
	case got := <-e.ex.started:
		return got
	case <-time.After(5 * time.Second):
		e.t.Fatal("等待执行开始超时")
		return ""
	}
}

// 平台命令占着忙闲锁时发来的消息只能排队；命令结束放开锁时要接着处理它，不能等到下一次对账。
func TestMessageQueuedDuringConsoleCommandStartsWhenItEnds(t *testing.T) {
	e, m := newMaintEnv(t)
	m.gate = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Reload(context.Background(), e.agent)
		done <- err
	}()
	if reason := <-m.entered; reason != "console_reload" {
		t.Fatalf("停止原因：%s", reason)
	}
	queued := e.mustSend("命令期间的消息", "queue")
	if queued.Run.Status != "queued" {
		t.Fatalf("命令进行中消息应排队：%s", queued.Run.Status)
	}
	close(m.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.waitStarted("命令期间的消息")
	e.waitIdle()
}

// /new 重开会话后要立即为会话丢失期间收到的 Inbox 消息安排提醒：先放开锁，提醒才能取得锁、马上开始。
func TestNewSessionStartsUnreadReminderRightAway(t *testing.T) {
	e, m := newMaintEnv(t)
	ctx := context.Background()
	if err := e.st.RecordSessionFile(ctx, e.agent, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.MarkSessionMissing(ctx, e.agent, nil); err != nil {
		t.Fatal(err)
	}
	e.deliver("会话丢失期间的消息", "queue")
	select {
	case got := <-e.ex.started:
		t.Fatalf("会话丢失时不应安排提醒：%q", got)
	case <-time.After(100 * time.Millisecond):
	}
	info, err := e.svc.NewSession(ctx, e.agent)
	if err != nil || info.State != "none" {
		t.Fatalf("重开会话：%+v %v", info, err)
	}
	if reason := <-m.entered; reason != "session_reset" {
		t.Fatalf("停止原因：%s", reason)
	}
	e.waitAnyStarted()
	e.waitIdle()
}

// /resume 列出会话只读文件，期间不占忙闲锁：这时发来的消息照常立即开始。
func TestResumeListingDoesNotHoldBusyLock(t *testing.T) {
	e, m := newMaintEnv(t)
	m.listGate = make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.ListResumeSessions(context.Background(), e.agent)
		done <- err
	}()
	if got := <-m.entered; got != "list" {
		t.Fatalf("应在列出会话：%s", got)
	}
	msg, err := e.send("列表期间的消息", "")
	if err != nil || msg.Run.Status == "queued" {
		t.Fatalf("列出会话时消息应立即开始：%+v %v", msg.Run.Status, err)
	}
	e.waitStarted("列表期间的消息")
	close(m.listGate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	e.waitIdle()
}

// sessionUpdates 是 Agent 事件流里 session.updated 的内容，按时间顺序。
func (e *env) sessionUpdates() []apitypes.SseSessionUpdated {
	e.t.Helper()
	var out []apitypes.SseSessionUpdated
	evs := e.newestEvents()
	slices.Reverse(evs)
	for _, ev := range evs {
		if ev.Type != apitypes.EventSessionUpdated {
			continue
		}
		var u apitypes.SseSessionUpdated
		if err := json.Unmarshal(ev.Data, &u); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, u)
	}
	return out
}

// 会话状态的每次变化都有 session.updated，包括第一轮回复后由 none 变 active（这一步不走会话命令，
// 由执行器记下会话文件）；状态没变时不重复推送。
func TestSessionUpdatedWhenFirstTurnStartsSession(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block 第一轮", "")
	e.waitStarted("block 第一轮")
	if err := e.st.RecordSessionFile(context.Background(), e.agent, "/managed/s.jsonl"); err != nil {
		t.Fatal(err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	deadline := time.Now().Add(5 * time.Second)
	for {
		updates := e.sessionUpdates()
		if n := len(updates); n > 0 && updates[n-1].State == "active" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("会话变 active 后应推送 session.updated：%+v", updates)
		}
		time.Sleep(20 * time.Millisecond)
	}
	before := len(e.sessionUpdates())
	e.mustSend("第二轮", "")
	e.waitStarted("第二轮")
	e.waitIdle()
	time.Sleep(200 * time.Millisecond) // 等概览状态重算
	if after := e.sessionUpdates(); len(after) != before {
		t.Fatalf("会话没变时不应重复推送：%+v", after[before:])
	}
}
