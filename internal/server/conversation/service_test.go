package conversation_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// fakeExec 是可控的执行器替身：消息中含 "block" 时阻塞，直到被放行或打断；
// 含 "hold" 时阻塞且不理会打断，只等放行（用来让打断消息停在打断队列里）。
type fakeExec struct {
	mu       sync.Mutex
	order    []string
	running  map[uuid.UUID]chan string // agent -> 控制通道："release" / "interrupt"
	runIDs   map[uuid.UUID]uuid.UUID   // agent -> 当前执行的 ID
	started  chan string
	versions map[uuid.UUID]int64
	intr     chan struct{} // 每次调用 Interrupt 后发一个信号
}

func newFakeExec() *fakeExec {
	return &fakeExec{running: map[uuid.UUID]chan string{}, runIDs: map[uuid.UUID]uuid.UUID{}, started: make(chan string, 100), intr: make(chan struct{}, 100)}
}

func (f *fakeExec) Execute(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
	prefix := fmt.Sprintf("本轮运行ID：%s。\n", req.RunID)
	if !strings.HasPrefix(req.Message, prefix) {
		return executor.Result{}, fmt.Errorf("缺少正确本轮runId说明")
	}
	req.Message = strings.TrimPrefix(req.Message, prefix)
	ctl := make(chan string, 4)
	f.mu.Lock()
	if f.running[req.AgentID] != nil {
		f.mu.Unlock()
		return executor.Result{}, fmt.Errorf("同一 Agent 同时有两次执行")
	}
	f.running[req.AgentID] = ctl
	f.runIDs[req.AgentID] = req.RunID
	f.order = append(f.order, req.Message)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.running, req.AgentID); delete(f.runIDs, req.AgentID); f.mu.Unlock() }()
	f.started <- req.Message

	sink.Phase(executor.PhasePreparing)
	sink.Running(1)
	sink.Tool("call-1", "bash", executor.ToolStarted)
	sink.TextDelta("部分")
	if strings.Contains(req.Message, "block") {
		select {
		case c := <-ctl:
			if c == "interrupt" {
				return executor.Result{Outcome: executor.OutcomeInterrupted, Text: "部分", ConfigVersion: 1}, nil
			}
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	for held := strings.Contains(req.Message, "hold"); held; {
		select {
		case c := <-ctl:
			held = c != "release"
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	sink.Tool("call-1", "bash", executor.ToolSucceeded)
	sink.TextDelta("回复")
	return executor.Result{Outcome: executor.OutcomeCompleted, Text: "回复：" + req.Message, ConfigVersion: 1}, nil
}

func (f *fakeExec) signal(agentID uuid.UUID, c string) bool {
	f.mu.Lock()
	ctl := f.running[agentID]
	f.mu.Unlock()
	if ctl == nil {
		return false
	}
	ctl <- c
	return true
}

// Interrupt 与本机执行器的语义相同：只打断 ID 一致的那一次执行。
func (f *fakeExec) Interrupt(_ context.Context, agentID, runID uuid.UUID) error {
	f.mu.Lock()
	match := f.runIDs[agentID] == runID
	f.mu.Unlock()
	if match {
		f.signal(agentID, "interrupt")
	}
	f.intr <- struct{}{}
	return nil
}
func (f *fakeExec) Release(context.Context, uuid.UUID) error { return nil }
func (f *fakeExec) Process(agentID uuid.UUID) executor.ProcessInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running[agentID] != nil {
		return executor.ProcessInfo{State: executor.ProcessBusy}
	}
	return executor.ProcessInfo{State: executor.ProcessStopped}
}
func (f *fakeExec) Shutdown(context.Context) error { return nil }

func (f *fakeExec) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.order)
}

type env struct {
	t      *testing.T
	st     *store.Store
	co     *coord.Coord
	prefix string
	rdb    *redis.Client
	ex     *fakeExec
	svc    *conversation.Service
	agent  uuid.UUID
	inst   uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := pgtest.NewStore(t)
	rdb, prefix := redistest.New(t)
	co := coord.New(rdb, coord.Options{Prefix: prefix, LockTTL: 5 * time.Second, StreamMaxLen: 1000, RunTextTTL: time.Hour})
	ctx := context.Background()
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, co: co, prefix: prefix, rdb: rdb, ex: newFakeExec(), agent: a.ID, inst: uuid.Must(uuid.NewV7())}
	e.svc = e.newService()
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Stop(context.Background()) })
	return e
}

func (e *env) newService() *conversation.Service {
	return conversation.New(e.st, e.co, e.ex, conversation.Options{
		InstanceID: e.inst, MaxMessage: 1 << 16, LockRenewInterval: 100 * time.Millisecond, ReconcileInterval: 200 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(debugLogs, nil)))
}

// debugLogs is io.Discard unless a test swaps it for diagnostics.
var debugLogs io.Writer = io.Discard

func (e *env) send(text, whenBusy string) (apitypes.Message, error) {
	return e.svc.Send(context.Background(), e.agent, text, whenBusy)
}

func (e *env) mustSend(text, whenBusy string) apitypes.Message {
	e.t.Helper()
	m, err := e.send(text, whenBusy)
	if err != nil {
		e.t.Fatalf("发送 %q 失败：%v", text, err)
	}
	return m
}

func (e *env) waitStarted(msg string) {
	e.t.Helper()
	select {
	case got := <-e.ex.started:
		if got != msg {
			e.t.Fatalf("期望开始执行 %q，实际 %q", msg, got)
		}
	case <-time.After(5 * time.Second):
		e.t.Fatalf("等待 %q 开始执行超时", msg)
	}
}

func (e *env) waitIdle() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := e.svc.Status(context.Background(), e.agent)
		if err == nil && !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("等待 Agent 空闲超时")
}

func (e *env) history() []apitypes.Message {
	e.t.Helper()
	page, err := e.svc.History(context.Background(), e.agent, store.MessageQuery{Limit: 200})
	if err != nil {
		e.t.Fatal(err)
	}
	return page.Messages
}

func TestSendWhenIdle(t *testing.T) {
	e := newEnv(t)
	m := e.mustSend("你好", "")
	if m.Run.Status != "starting" || m.Run.WhenBusy != nil {
		t.Fatalf("空闲时应立即开始，且不记录选择：%+v", m.Run)
	}
	e.waitStarted("你好")
	e.waitIdle()
	h := e.history()
	if len(h) != 2 || h[0].Role != "user" || h[1].Role != "assistant" || h[1].Text != "回复：你好" || h[1].Run.Status != "completed" {
		t.Fatalf("历史不对：%+v", h)
	}
	if len(h[1].Run.Tools) != 2 || h[1].Run.Tools[0].State != "started" {
		t.Fatalf("应记录工具状态：%+v", h[1].Run.Tools)
	}
}

func TestBusyWithoutChoice(t *testing.T) {
	e := newEnv(t)
	first := e.mustSend("block 慢", "")
	e.waitStarted("block 慢")
	_, err := e.send("第二条", "")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeAgentBusy {
		t.Fatalf("忙时未选择应返回 agent_busy：%v", err)
	}
	d := ae.Details.(apitypes.AgentBusyDetails)
	if d.CurrentRunID != first.Run.ID {
		t.Fatalf("详情应给出当前执行：%+v", d)
	}
	if n := len(e.history()); n != 1 {
		t.Fatalf("被拒绝的消息不应保存：%d", n)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}

func TestQueueAndInterruptOrdering(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block 当前", "")
	e.waitStarted("block 当前")
	e.mustSend("普通1", "queue")
	e.mustSend("普通2", "queue")
	i1 := e.mustSend("block 打断1", "interrupt")
	if *i1.Run.WhenBusy != "interrupt" || i1.Run.Status != "queued" {
		t.Fatalf("打断消息应排队并记录选择：%+v", i1.Run)
	}
	e.waitStarted("block 打断1")
	// 打断进行中再来一条打断：排在打断1之后、普通消息之前。
	e.mustSend("打断2", "interrupt")
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	got := e.ex.messages()
	want := []string{"block 当前", "block 打断1", "打断2", "普通1", "普通2"}
	if !slices.Equal(got, want) {
		t.Fatalf("处理顺序不对：%v", got)
	}
	h := e.history()
	var interrupted *apitypes.Message
	for i := range h {
		if h[i].Role == "assistant" && h[i].Run.Status == "interrupted" {
			interrupted = &h[i]
		}
	}
	if interrupted == nil || *interrupted.Run.EndReason != "user_interrupt" || interrupted.Text != "部分" {
		t.Fatalf("被打断的回复应记为已打断并保留文字：%+v", interrupted)
	}
}

// 打断只作用于发起它的那一轮：确认 A 处于打断中之后、调用执行器之前，A 恰好自然结束、B 已开始，
// 这次迟到的打断不能落到 B 上。用测试钩子把时序固定下来，不靠反复运行碰运气。
func TestLateInterruptDoesNotHitNextRun(t *testing.T) {
	e := newEnv(t)
	var target atomic.Value // 执行 A 的 ID
	reached := make(chan struct{}, 8)
	release := make(chan struct{})
	conversation.SetTestHookBeforeInterrupt(func(runID uuid.UUID) {
		if id, _ := target.Load().(uuid.UUID); id == runID {
			reached <- struct{}{}
			<-release // 卡住对 A 的打断，直到 B 已经开始
		}
	})
	t.Cleanup(func() { conversation.SetTestHookBeforeInterrupt(nil) })

	a := e.mustSend("block A", "")
	target.Store(a.Run.ID)
	e.waitStarted("block A")
	e.mustSend("block B", "interrupt") // A 转为打断中，并发布针对 A 的打断命令
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("等待针对 A 的打断到达钩子超时")
	}
	// 打断还没交给执行器，A 恰好自然结束；随后 B 开始。
	e.ex.signal(e.agent, "release")
	e.waitStarted("block B")
	close(release) // 放行迟到的打断
	select {
	case <-e.ex.intr:
	case <-time.After(5 * time.Second):
		t.Fatal("等待迟到的打断交给执行器超时")
	}
	time.Sleep(200 * time.Millisecond) // 续期协程可能再交一次，给它时间
	e.ex.signal(e.agent, "release")    // B 正常结束
	e.waitIdle()

	status := map[string]string{}
	for _, m := range e.history() {
		if m.Role == "user" {
			status[m.Text] = m.Run.Status
		}
	}
	if status["block A"] != "completed" || status["block B"] != "completed" {
		t.Fatalf("迟到的打断不应作用到 B：A=%s，B=%s", status["block A"], status["block B"])
	}
}

func TestConcurrentSendsExactlyOnce(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block 开头", "")
	e.waitStarted("block 开头")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := e.send(fmt.Sprintf("m%02d", i), "queue"); err != nil {
				t.Errorf("发送失败：%v", err)
			}
		})
	}
	wg.Wait()
	queued, _ := e.st.QueuedRuns(context.Background(), e.agent)
	var wantOrder []string
	for _, r := range queued {
		m, _ := e.st.GetMessage(context.Background(), r.UserMessageID)
		wantOrder = append(wantOrder, m.Text)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	got := e.ex.messages()[1:]
	if !slices.Equal(got, wantOrder) || len(got) != 20 {
		t.Fatalf("每条消息应恰好处理一次且按排队顺序：\n得到 %v\n期望 %v", got, wantOrder)
	}
}

func TestRedisDataLoss(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block 当前", "")
	e.waitStarted("block 当前")
	e.mustSend("排队1", "queue")
	e.mustSend("排队2", "queue")
	if err := redistest.DeletePrefix(context.Background(), e.rdb, e.prefix); err != nil {
		t.Fatal(err)
	}
	// 数据丢失后仍判定为忙，不会开始第二次执行。
	if _, err := e.send("没选择", ""); !apperr.Is(err, apperr.CodeAgentBusy) {
		t.Fatalf("Redis 数据丢失后仍应判定为忙：%v", err)
	}
	// 对账在默认配置下应在 30 秒内（这里是 200ms 周期）重建锁与队列。
	deadline := time.Now().Add(30 * time.Second)
	for {
		i, n, _ := e.co.QueueContents(context.Background(), e.agent.String())
		owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
		if len(i)+len(n) == 2 && owner != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("对账未能重建锁与队列")
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	if got := e.ex.messages(); !slices.Equal(got, []string{"block 当前", "排队1", "排队2"}) {
		t.Fatalf("排队消息应按原顺序各处理一次：%v", got)
	}
}

func TestRestartRecovery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	other := uuid.Must(uuid.NewV7())
	// 模拟上一次运行遗留：一个进行中的执行与一条排队消息。
	_, run, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "旧的", Start: true, InstanceID: other})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.co.AppendText(ctx, e.agent.String(), run.ID.String(), "已产生的文字", "run.text", []byte(`{}`))
	_, _ = e.co.Restore(ctx, e.agent.String(), coord.LockValue(run.ID.String(), other.String()))
	_, queued, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "排队的", WhenBusy: "queue", InstanceID: other})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.co.PushNormal(ctx, e.agent.String(), queued.ID.String())

	svc2 := e.newService()
	if err := svc2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer svc2.Stop(ctx)
	e.waitIdle()
	r, _ := e.st.GetRun(ctx, run.ID)
	if r.Status != "interrupted" || r.EndReason != "server_restart" {
		t.Fatalf("遗留执行应标为服务端重启中断：%+v", r)
	}
	var reply *apitypes.Message
	for _, m := range e.history() {
		if m.Role == "assistant" && m.Run.ID == run.ID {
			reply = &m
		}
	}
	if reply == nil || reply.Text != "已产生的文字" {
		t.Fatalf("应保留已收到的文字：%+v", reply)
	}
	if !slices.Contains(e.ex.messages(), "排队的") {
		t.Fatal("排队消息应照常处理")
	}
}

func TestSessionMissingAndReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.st.RecordSessionFile(ctx, e.agent, "/tmp/x.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.MarkSessionMissing(ctx, e.agent, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.send("你好", ""); !apperr.Is(err, apperr.CodeSessionMissing) {
		t.Fatalf("会话丢失时应拒绝：%v", err)
	}
	if n := len(e.history()); n != 0 {
		t.Fatal("被拒绝的消息不应保存")
	}
	sess, err := e.svc.ResetSession(ctx, e.agent)
	if err != nil || sess.State != "none" || sess.Epoch != 2 {
		t.Fatalf("重开会话后应为新会话、序号加一：%+v %v", sess, err)
	}
	m := e.mustSend("block 新会话", "")
	if m.SessionEpoch != 2 {
		t.Fatalf("新消息的会话序号应为 2：%d", m.SessionEpoch)
	}
	e.waitStarted("block 新会话")
	if _, err := e.svc.ResetSession(ctx, e.agent); !apperr.Is(err, apperr.CodeAgentNotIdle) {
		t.Fatalf("有进行中的执行时应拒绝重开：%v", err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}

func TestStatusAndHistoryPaging(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range 3 {
		msg := fmt.Sprintf("消息%d", i)
		e.mustSend(msg, "")
		e.waitStarted(msg)
		e.waitIdle()
	}
	latest, err := e.svc.History(ctx, e.agent, store.MessageQuery{Limit: 2})
	page, more := latest.Messages, latest.HasMore
	if err != nil || len(page) != 2 || !more || latest.HasMoreAfter || page[1].Role != "assistant" {
		t.Fatalf("最新一页不对：%d %v %v", len(page), more, err)
	}
	olderPage, err := e.svc.History(ctx, e.agent, store.MessageQuery{Before: &page[0].ID, Limit: 10})
	older := olderPage.Messages
	if err != nil || len(older) != 4 || olderPage.HasMore || !olderPage.HasMoreAfter {
		t.Fatalf("更早的一页不对：%d %v", len(older), err)
	}
	st, err := e.svc.Status(ctx, e.agent)
	if err != nil || st.Busy || st.Session.State != "none" {
		t.Fatalf("状态不对：%+v %v", st, err)
	}
	e.mustSend("block x", "")
	e.waitStarted("block x")
	var snap apitypes.SseSnapshot
	var lastID string
	var err2 error
	for range 100 {
		snap, lastID, err2 = e.svc.Snapshot(ctx, e.agent)
		if err2 == nil && snap.CurrentText != nil && *snap.CurrentText != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	err = err2
	if err != nil || !snap.Status.Busy || snap.CurrentText == nil || *snap.CurrentText != "部分" || lastID == "" {
		t.Fatalf("快照不对：%+v %s %v", snap, lastID, err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}
