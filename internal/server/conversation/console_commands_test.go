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

// maintExec adds the maintenance interfaces used by platform commands on top of fakeExec; when gate is non-nil,
// StopIdle blocks there until gate is closed.
type maintExec struct {
	*fakeExec
	entered  chan string
	gate     chan struct{}
	listGate chan struct{} // when non-nil, ListSessions blocks there until it is closed
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

// newMaintEnv is the same as newEnv, but reconciles only once an hour: messages and reminders queued after a platform
// command can only be processed by the command itself continuing.
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

// A message sent while a platform command holds the busy lock can only queue; when the command ends and releases the
// lock it must go on to process it, not wait for the next reconciliation.
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

// After /new reopens the session it must immediately schedule a reminder for Inbox messages received while the session
// was lost: the lock is released first, so the reminder can acquire it and start right away.
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

// /resume lists sessions by only reading files and does not hold the busy lock meanwhile: a message sent at that time
// starts right away as usual.
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

// sessionUpdates returns the contents of session.updated in the Agent's event stream, in chronological order.
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

// Every change of the session state gets a session.updated, including none becoming active after the first reply (this
// step does not go through a session command; the executor records the session file); nothing is pushed again when the
// state did not change.
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
	time.Sleep(200 * time.Millisecond) // wait for the overview status to be recomputed
	if after := e.sessionUpdates(); len(after) != before {
		t.Fatalf("会话没变时不应重复推送：%+v", after[before:])
	}
}
