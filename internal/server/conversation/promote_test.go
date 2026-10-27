package conversation_test

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
)

// names maps run IDs to the message text the test sent.
type names map[uuid.UUID]string

func (n names) of(runs []apitypes.Run) []string {
	out := []string{}
	for _, r := range runs {
		out = append(out, n[r.ID])
	}
	return out
}

func (e *env) send2(n names, text, whenBusy string) apitypes.Message {
	e.t.Helper()
	m := e.mustSend(text, whenBusy)
	n[m.Run.ID] = text
	return m
}

// promoteResult is the promote receipt plus the queued runs read right after it.
type promoteResult struct {
	Run        apitypes.Run
	Status     string
	QueuedRuns []apitypes.Run
}

// promote sends one promote action with a fresh actionId (a new click).
func (e *env) promote(id uuid.UUID) (promoteResult, error) {
	ctx := context.Background()
	raw, err := e.svc.PromoteToInterrupt(ctx, e.agent, id, uuid.New())
	if err != nil {
		return promoteResult{}, err
	}
	var rec apitypes.AgentQueueActionReceipt
	if err := json.Unmarshal(raw, &rec); err != nil {
		e.t.Fatal(err)
	}
	out := promoteResult{Run: *rec.Run, Status: rec.Status, QueuedRuns: []apitypes.Run{}}
	queued, err := e.st.QueuedRuns(ctx, e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range queued {
		out.QueuedRuns = append(out.QueuedRuns, apitypes.FromRun(r))
	}
	return out, nil
}

func (e *env) mustPromote(id uuid.UUID) promoteResult {
	e.t.Helper()
	r, err := e.promote(id)
	if err != nil {
		e.t.Fatalf("转为打断失败：%v", err)
	}
	return r
}

func (e *env) snapshotQueue(n names) []string {
	e.t.Helper()
	snap, _, err := e.svc.Snapshot(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	return n.of(snap.QueuedRuns)
}

func (e *env) redisQueues(n names) ([]string, []string) {
	e.t.Helper()
	qi, qn, err := e.co.QueueContents(context.Background(), e.agent.String())
	if err != nil {
		e.t.Fatal(err)
	}
	conv := func(ids []string) []string {
		out := []string{}
		for _, id := range ids {
			out = append(out, n[uuid.MustParse(id)])
		}
		return out
	}
	return conv(qi), conv(qn)
}

// lastQueueEvent returns the newest queue.updated payload on the agent stream.
func (e *env) lastQueueEvent(n names) []string {
	e.t.Helper()
	msgs, err := e.rdb.XRevRangeN(context.Background(), e.co.Keys().Events(e.agent.String()), "+", "-", 200).Result()
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
		return n.of(ev.QueuedRuns)
	}
	e.t.Fatal("没有 queue.updated 事件")
	return nil
}

func drain(ch chan struct{}) int {
	n := 0
	for {
		select {
		case <-ch:
			n++
		default:
			return n
		}
	}
}

// 运行中转换：当前回复被打断，转换的消息排在所有普通排队之前执行。
func TestPromoteInterruptsRunningReplyAndRunsBeforeQueue(t *testing.T) {
	e := newEnv(t)
	n := names{}
	a := e.send2(n, "block A", "")
	e.waitStarted("block A")
	e.send2(n, "普通1", "queue")
	two := e.send2(n, "普通2", "queue")
	got := e.mustPromote(two.Run.ID)
	if got.Run.WhenBusy == nil || *got.Run.WhenBusy != "interrupt" || !slices.Equal(n.of(got.QueuedRuns), []string{"普通2", "普通1"}) {
		t.Fatalf("转换结果不对：%+v %v", got.Run, n.of(got.QueuedRuns))
	}
	if q := e.lastQueueEvent(n); !slices.Equal(q, []string{"普通2", "普通1"}) {
		t.Fatalf("queue.updated 顺序不对：%v", q)
	}
	e.waitIdleOrDump(names{})
	if order := e.ex.messages(); !slices.Equal(order, []string{"block A", "普通2", "普通1"}) {
		t.Fatalf("执行顺序不对：%v", order)
	}
	r, err := e.st.GetRun(context.Background(), a.Run.ID)
	if err != nil || r.Status != "interrupted" || r.EndReason != "user_interrupt" {
		t.Fatalf("进行中的回复应被打断：%+v %v", r, err)
	}
	tl, err := (&runrecord.Service{Store: e.st}).Timeline(context.Background(), e.agent, two.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(tl)
	var parsed struct {
		Entries []struct {
			Type, WhenBusy, From, To string
		} `json:"entries"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var queued, promoted bool
	for _, en := range parsed.Entries {
		queued = queued || en.Type == "queued" && en.WhenBusy == "queue"
		promoted = promoted || en.Type == "promoted" && en.From == "queue" && en.To == "interrupt"
	}
	if !queued || !promoted {
		t.Fatalf("时间线应保留普通排队并追加转为打断：%s", raw)
	}
}

// 排在已有打断之后、全部普通排队之前；连续转换按转换先后排列；重复转换幂等。
func TestPromoteOrderingAfterExistingInterrupts(t *testing.T) {
	e := newEnv(t)
	n := names{}
	e.send2(n, "hold A", "")
	e.waitStarted("hold A")
	e.send2(n, "普通1", "queue")
	two := e.send2(n, "普通2", "queue")
	three := e.send2(n, "普通3", "queue")
	e.send2(n, "打断1", "interrupt") // hold A 不理会打断，打断1停在打断队列
	time.Sleep(100 * time.Millisecond)
	drain(e.ex.intr)

	got := e.mustPromote(two.Run.ID)
	want := []string{"打断1", "普通2", "普通1", "普通3"}
	if !slices.Equal(n.of(got.QueuedRuns), want) || !slices.Equal(e.snapshotQueue(n), want) || !slices.Equal(e.lastQueueEvent(n), want) {
		t.Fatalf("第一次转换后顺序不对：结果 %v 快照 %v", n.of(got.QueuedRuns), e.snapshotQueue(n))
	}
	if qi, qn := e.redisQueues(n); !slices.Equal(qi, []string{"打断1", "普通2"}) || !slices.Equal(qn, []string{"普通1", "普通3"}) {
		t.Fatalf("Redis 队列不对：%v / %v", qi, qn)
	}
	got = e.mustPromote(three.Run.ID)
	want = []string{"打断1", "普通2", "普通3", "普通1"}
	if !slices.Equal(n.of(got.QueuedRuns), want) || !slices.Equal(e.snapshotQueue(n), want) {
		t.Fatalf("连续转换应按转换先后：%v", n.of(got.QueuedRuns))
	}
	if qi, qn := e.redisQueues(n); !slices.Equal(qi, []string{"打断1", "普通2", "普通3"}) || !slices.Equal(qn, []string{"普通1"}) {
		t.Fatalf("Redis 队列不对：%v / %v", qi, qn)
	}
	events := func(kind string) int {
		var c int
		if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM lifecycle_events WHERE agent_id=$1 AND type=$2`, e.agent, kind).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	aborts, promoted := events(store.EventRunAbortRequested), events(store.EventRunPromoted)
	again := e.mustPromote(two.Run.ID)
	if !slices.Equal(n.of(again.QueuedRuns), want) || events(store.EventRunAbortRequested) != aborts || events(store.EventRunPromoted) != promoted {
		t.Fatal("已是打断时应幂等，不重复打断、不重复记录")
	}
	snap, _, err := e.svc.Snapshot(context.Background(), e.agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.QueuedRuns {
		wb := *r.WhenBusy
		if (n[r.ID] == "普通1") != (wb == "queue") {
			t.Fatalf("快照里的 whenBusy 不对：%s=%s", n[r.ID], wb)
		}
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if order := e.ex.messages(); !slices.Equal(order, []string{"hold A", "打断1", "普通2", "普通3", "普通1"}) {
		t.Fatalf("实际执行顺序不对：%v", order)
	}
}

// 已领取／已结束返回 run_not_queued 并带当前状态；其他来源 422；不存在或不属于该 Agent 404。
func TestPromoteConflictsAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	running := e.mustSend("block 当前", "")
	e.waitStarted("block 当前")
	_, err := e.promote(running.Run.ID)
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeRunNotQueued {
		t.Fatalf("进行中的执行应返回 run_not_queued：%v", err)
	}
	if d := ae.Details.(store.RunNotQueuedDetails); d.RunID == nil || *d.RunID != running.Run.ID || d.Status == nil || *d.Status != "starting" && *d.Status != "running" {
		t.Fatalf("冲突详情应带当前状态：%+v", d)
	}
	if _, err = e.promote(uuid.New()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("不存在的执行应 404：%v", err)
	}
	dc, err := e.st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	other, err := e.st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "b", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.svc.PromoteToInterrupt(ctx, other.ID, running.Run.ID, uuid.New()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("其他 Agent 的执行应 404：%v", err)
	}
	inbox := uuid.Must(uuid.NewV7())
	rem, err := e.st.InsertInboxWithReminder(ctx, &store.NewInboxMessage{ID: inbox, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: e.agent}, Text: "平台消息", WhenBusy: "queue"},
		store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "提醒", WhenBusy: "queue", InstanceID: e.inst, TriggerInboxID: &inbox})
	if err != nil || rem.Run == nil {
		t.Fatal("平台提醒", err)
	}
	if _, err = e.promote(rem.Run.ID); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("平台提醒不应开放：%v", err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	_, err = e.promote(running.Run.ID)
	if ae, ok = apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued || *ae.Details.(store.RunNotQueuedDetails).Status != "completed" {
		t.Fatalf("已结束的执行应返回 run_not_queued(completed)：%v", err)
	}
}

// 转换与出队并发：每条消息恰好执行一次，Redis 两个队列最终都为空。
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

var raceLogs = &lockedBuf{}

func TestPromoteRacesHandoffExactlyOnce(t *testing.T) {
	debugLogs = raceLogs
	t.Cleanup(func() { debugLogs = io.Discard })
	e := newEnv(t)
	for round := range 15 {
		n := names{}
		head := e.send2(n, "block 开头", "")
		_ = head
		e.waitStarted("block 开头")
		ids := []uuid.UUID{}
		for i := range 6 {
			m := e.send2(n, "普通"+string(rune('a'+round))+string(rune('0'+i)), "queue")
			ids = append(ids, m.Run.ID)
		}
		rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Go(func() {
				if _, err := e.promote(id); err != nil {
					if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued {
						t.Errorf("转换失败：%v", err)
					}
				}
			})
		}
		wg.Go(func() { e.ex.signal(e.agent, "release") })
		wg.Wait()
		e.waitIdleOrDump(n)
		for len(e.ex.started) > 0 {
			<-e.ex.started
		}
		// A later promotion interrupts whatever is active, including an earlier
		// promoted run that just started (same as sending interrupt twice), so a
		// run may end interrupted before reaching the executor. Exactly once means:
		// dispatched once, terminal, executed at most once, nothing left queued.
		counts := map[string]int{}
		for _, m := range e.ex.messages() {
			counts[m]++
		}
		for id, text := range n {
			var dispatched int
			var status string
			if err := e.st.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM lifecycle_events WHERE run_id=$1 AND type=$2),(SELECT status FROM runs WHERE id=$1)`, id, store.EventRunDispatched).Scan(&dispatched, &status); err != nil {
				t.Fatal(err)
			}
			if dispatched != 1 || status != "completed" && status != "interrupted" || text != "block 开头" && counts[text] > 1 {
				t.Fatalf("第 %d 轮：%s 派发 %d 次、状态 %s、执行 %d 次", round, text, dispatched, status, counts[text])
			}
		}
		if qi, qn := e.redisQueues(n); len(qi) != 0 || len(qn) != 0 {
			t.Fatalf("第 %d 轮：Redis 队列应为空：%v / %v", round, qi, qn)
		}
	}
}

// 引发打断的执行已被出队并开始时，标记打断不能落到它自己身上；发送打断与转换打断两条路径都覆盖。
func TestTriggerAlreadyStartedIsNotInterruptedByItself(t *testing.T) {
	for _, path := range []string{"send", "promote"} {
		t.Run(path, func(t *testing.T) {
			e := newEnv(t)
			var armed atomic.Bool
			reached := make(chan uuid.UUID, 1)
			release := make(chan struct{})
			conversation.SetTestHookBeforeMarkAborting(func(by uuid.UUID) {
				if armed.CompareAndSwap(true, false) {
					reached <- by
					<-release
				}
			})
			t.Cleanup(func() { conversation.SetTestHookBeforeMarkAborting(nil) })
			e.mustSend("block A", "")
			e.waitStarted("block A")
			done := make(chan error, 1)
			var queued uuid.UUID
			if path == "promote" {
				queued = e.mustSend("block B", "queue").Run.ID
			}
			armed.Store(true)
			go func() {
				var err error
				if path == "send" {
					_, err = e.svc.Send(context.Background(), e.agent, "block B", "interrupt")
				} else {
					_, err = e.promote(queued)
				}
				done <- err
			}()
			var by uuid.UUID
			select {
			case by = <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("等待标记打断前的钩子超时")
			}
			e.ex.signal(e.agent, "release") // A 自然结束，B 被出队并开始
			e.waitStarted("block B")
			if r, err := e.st.GetRun(context.Background(), by); err != nil || r.Status == "queued" {
				t.Fatalf("B 应已开始：%+v %v", r, err)
			}
			close(release) // 现在才标记打断：B 已是唯一进行中的执行
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			e.ex.signal(e.agent, "release")
			e.waitIdleOrDump(names{})
			r, err := e.st.GetRun(context.Background(), by)
			if err != nil || r.Status != "completed" {
				t.Fatalf("B 被自己引发的打断打断了：%+v %v", r, err)
			}
		})
	}
}

func (e *env) waitIdleOrDump(n names) {
	e.t.Helper()
	// One statement, so a run moving from queued to starting between two reads
	// (as Status does) cannot look idle.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var open int
		err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND status IN ('queued','starting','running','aborting')`, e.agent).Scan(&open)
		if err == nil && open == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, _ := e.st.Pool.Query(context.Background(), `SELECT id,status,priority,coalesce(when_busy,'') FROM runs WHERE agent_id=$1 AND status NOT IN ('completed','interrupted','failed') ORDER BY priority,queue_seq`, e.agent)
	for rows.Next() {
		var id uuid.UUID
		var st, wb string
		var p int
		_ = rows.Scan(&id, &st, &p, &wb)
		e.t.Logf("db run %s %s p=%d wb=%s", n[id], st, p, wb)
	}
	rows.Close()
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "conversation") && !strings.Contains(g, "waitIdleOrDump") {
			e.t.Log(g)
		}
	}
	raceLogs.mu.Lock()
	for _, line := range strings.Split(raceLogs.b.String(), "\n") {
		if strings.Contains(line, "level=WARN") || strings.Contains(line, "level=ERROR") {
			e.t.Log(line)
		}
	}
	raceLogs.mu.Unlock()
	qi, qn := e.redisQueues(n)
	owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
	e.t.Fatalf("等待 Agent 空闲超时：redis 打断 %v 普通 %v 锁 %q 执行顺序 %v", qi, qn, owner, e.ex.messages())
}
