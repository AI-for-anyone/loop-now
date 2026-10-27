package coord_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"loop-now/internal/server/coord"
	"loop-now/internal/testutil/redistest"
)

const (
	testLockTTL    = 30 * time.Second
	testRunTextTTL = time.Hour
)

func newCoord(t *testing.T, maxLen int64) (*coord.Coord, *redis.Client) {
	t.Helper()
	rdb, prefix := redistest.New(t)
	return coord.New(rdb, coord.Options{
		Prefix:       prefix,
		LockTTL:      testLockTTL,
		StreamMaxLen: maxLen,
		RunTextTTL:   testRunTextTTL,
	}), rdb
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertOwner(t *testing.T, c *coord.Coord, agent, want string) {
	t.Helper()
	got, err := c.LockOwner(context.Background(), agent)
	check(t, err)
	if got != want {
		t.Fatalf("锁持有者 = %q，期望 %q", got, want)
	}
}

func assertPTTL(t *testing.T, rdb *redis.Client, key string, limit time.Duration) {
	t.Helper()
	d, err := rdb.PTTL(context.Background(), key).Result()
	check(t, err)
	if d <= 0 || d > limit {
		t.Fatalf("%s 的 PTTL = %v，期望在 (0, %v]", key, d, limit)
	}
}

func TestKeys(t *testing.T) {
	k := coord.Keys{Prefix: "loopnow:"}
	cases := map[string]string{
		k.Lock("a1"):           "loopnow:agent:a1:lock",
		k.QueueInterrupt("a1"): "loopnow:agent:a1:q:interrupt",
		k.QueueNormal("a1"):    "loopnow:agent:a1:q:normal",
		k.Cmd("a1"):            "loopnow:agent:a1:cmd",
		k.Events("a1"):         "loopnow:agent:a1:events",
		k.RunText("r1"):        "loopnow:run:r1:text",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("键名 %q，期望 %q", got, want)
		}
	}
	if v := coord.LockValue("r1", "i1"); v != "r1|i1" {
		t.Errorf("LockValue = %q", v)
	}
	c := coord.New(nil, coord.Options{Prefix: "p:"})
	if c.Keys().Prefix != "p:" {
		t.Errorf("Keys().Prefix = %q", c.Keys().Prefix)
	}
}

func TestAcquireIfIdle(t *testing.T) {
	c, rdb := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-1"
	ownerA := coord.LockValue("run-a", "inst")

	// 锁空闲但普通队列非空：拒绝。
	check(t, c.PushNormal(ctx, agent, "queued-1"))
	if must[bool](t)(c.AcquireIfIdle(ctx, agent, ownerA)) {
		t.Fatal("普通队列非空时不应拿到锁")
	}
	assertOwner(t, c, agent, "")

	// 锁空闲但打断队列非空：拒绝。
	check(t, c.RebuildQueues(ctx, agent, []string{"queued-2"}, nil))
	if must[bool](t)(c.AcquireIfIdle(ctx, agent, ownerA)) {
		t.Fatal("打断队列非空时不应拿到锁")
	}
	assertOwner(t, c, agent, "")

	// 完全空闲：成功并带 TTL。
	check(t, c.RebuildQueues(ctx, agent, nil, nil))
	if !must[bool](t)(c.AcquireIfIdle(ctx, agent, ownerA)) {
		t.Fatal("空闲时应拿到锁")
	}
	assertOwner(t, c, agent, ownerA)
	assertPTTL(t, rdb, c.Keys().Lock(agent), testLockTTL)

	// 已被持有：拒绝且不改变持有者。
	if must[bool](t)(c.AcquireIfIdle(ctx, agent, coord.LockValue("run-b", "inst"))) {
		t.Fatal("锁被持有时不应拿到锁")
	}
	assertOwner(t, c, agent, ownerA)
}

func TestAcquireIfIdleConcurrent(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	results := make([]bool, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			results[i], errs[i] = c.AcquireIfIdle(ctx, "agent-c", coord.LockValue(fmt.Sprint("run-", i), "inst"))
		})
	}
	wg.Wait()
	won := 0
	for i := range n {
		check(t, errs[i])
		if results[i] {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("并发获取锁成功 %d 次，期望 1 次", won)
	}
}

func TestRenewRestoreRelease(t *testing.T) {
	c, rdb := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-2"
	lockKey := c.Keys().Lock(agent)
	owner := coord.LockValue("run-a", "inst")
	other := coord.LockValue("run-b", "inst")

	// 锁不存在时 Renew 按 owner 补设。
	if !must[bool](t)(c.Renew(ctx, agent, owner)) {
		t.Fatal("锁丢失时 Renew 应补设")
	}
	assertOwner(t, c, agent, owner)
	assertPTTL(t, rdb, lockKey, testLockTTL)

	// 持有者匹配时续期。
	check(t, rdb.PExpire(ctx, lockKey, time.Second).Err())
	if !must[bool](t)(c.Renew(ctx, agent, owner)) {
		t.Fatal("持有者匹配时 Renew 应成功")
	}
	if d := must[time.Duration](t)(rdb.PTTL(ctx, lockKey).Result()); d <= time.Second {
		t.Fatalf("续期后 PTTL = %v，应已刷新", d)
	}

	// 持有者不匹配时拒绝且不改动。
	if must[bool](t)(c.Renew(ctx, agent, other)) {
		t.Fatal("持有者不匹配时 Renew 应失败")
	}
	assertOwner(t, c, agent, owner)

	// Restore 仅在锁不存在时设置。
	if must[bool](t)(c.Restore(ctx, agent, other)) {
		t.Fatal("锁存在时 Restore 不应覆盖")
	}
	assertOwner(t, c, agent, owner)

	// Release 按持有者比对删除。
	if must[bool](t)(c.Release(ctx, agent, other)) {
		t.Fatal("持有者不匹配时 Release 不应删除")
	}
	assertOwner(t, c, agent, owner)
	if !must[bool](t)(c.Release(ctx, agent, owner)) {
		t.Fatal("持有者匹配时 Release 应删除")
	}
	assertOwner(t, c, agent, "")
	if must[bool](t)(c.Release(ctx, agent, owner)) {
		t.Fatal("锁不存在时 Release 应返回 false")
	}

	if !must[bool](t)(c.Restore(ctx, agent, other)) {
		t.Fatal("锁不存在时 Restore 应设置")
	}
	assertOwner(t, c, agent, other)
	assertPTTL(t, rdb, lockKey, testLockTTL)
}

func TestHandoff(t *testing.T) {
	c, rdb := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-3"
	const inst = "inst-x"
	owner := coord.LockValue("run-0", inst)
	if !must[bool](t)(c.AcquireIfIdle(ctx, agent, owner)) {
		t.Fatal("应拿到锁")
	}
	check(t, c.PushNormal(ctx, agent, "n1"))
	check(t, c.PushNormal(ctx, agent, "n2"))
	check(t, c.PushInterrupt(ctx, agent, "i1"))
	check(t, c.PushInterrupt(ctx, agent, "i2"))

	// 持有者不匹配：前置条件失败，不弹出、不改锁。
	next, ok, err := c.Handoff(ctx, agent, coord.LockValue("run-zz", inst), inst)
	check(t, err)
	if ok || next != "" {
		t.Fatalf("错误持有者交接应失败，得到 (%q, %v)", next, ok)
	}
	// currentOwner 为空但锁存在：前置条件失败。
	next, ok, err = c.Handoff(ctx, agent, "", inst)
	check(t, err)
	if ok || next != "" {
		t.Fatalf("锁存在时空持有者交接应失败，得到 (%q, %v)", next, ok)
	}
	assertOwner(t, c, agent, owner)
	qi, qn := must2(t, c, agent)
	if !slices.Equal(qi, []string{"i1", "i2"}) || !slices.Equal(qn, []string{"n1", "n2"}) {
		t.Fatalf("失败的交接不应改动队列：%v %v", qi, qn)
	}

	// 先打断队列、后普通队列，各自 FIFO。
	cur := owner
	for _, want := range []string{"i1", "i2", "n1", "n2"} {
		next, ok, err = c.Handoff(ctx, agent, cur, inst)
		check(t, err)
		if !ok || next != want {
			t.Fatalf("交接得到 (%q, %v)，期望 (%q, true)", next, ok, want)
		}
		cur = coord.LockValue(next, inst)
		assertOwner(t, c, agent, cur)
		assertPTTL(t, rdb, c.Keys().Lock(agent), testLockTTL)
	}

	// 队列为空：删除锁。
	next, ok, err = c.Handoff(ctx, agent, cur, inst)
	check(t, err)
	if !ok || next != "" {
		t.Fatalf("队列为空时交接应得到 (\"\", true)，得到 (%q, %v)", next, ok)
	}
	assertOwner(t, c, agent, "")

	// 锁不存在时 currentOwner 非空：前置条件失败。
	check(t, c.PushNormal(ctx, agent, "n3"))
	next, ok, err = c.Handoff(ctx, agent, cur, inst)
	check(t, err)
	if ok || next != "" {
		t.Fatalf("锁不存在时非空持有者交接应失败，得到 (%q, %v)", next, ok)
	}

	// 锁不存在时 currentOwner 为空：接手队首。
	next, ok, err = c.Handoff(ctx, agent, "", "inst-y")
	check(t, err)
	if !ok || next != "n3" {
		t.Fatalf("空闲交接得到 (%q, %v)，期望 (\"n3\", true)", next, ok)
	}
	assertOwner(t, c, agent, "n3|inst-y")

	// 锁不存在、队列为空、currentOwner 为空：成功且不产生锁。
	check(t, rdb.Del(ctx, c.Keys().Lock(agent)).Err())
	next, ok, err = c.Handoff(ctx, agent, "", inst)
	check(t, err)
	if !ok || next != "" {
		t.Fatalf("空闲且无排队时交接应得到 (\"\", true)，得到 (%q, %v)", next, ok)
	}
	assertOwner(t, c, agent, "")
}

func must2(t *testing.T, c *coord.Coord, agent string) (interrupt, normal []string) {
	t.Helper()
	qi, qn, err := c.QueueContents(context.Background(), agent)
	check(t, err)
	return qi, qn
}

func TestRebuildQueues(t *testing.T) {
	c, rdb := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-4"
	check(t, c.PushNormal(ctx, agent, "old-n"))
	check(t, c.PushInterrupt(ctx, agent, "old-i"))

	check(t, c.RebuildQueues(ctx, agent, []string{"i3", "i1", "i2"}, []string{"n2", "n1"}))
	qi, qn := must2(t, c, agent)
	if !slices.Equal(qi, []string{"i3", "i1", "i2"}) || !slices.Equal(qn, []string{"n2", "n1"}) {
		t.Fatalf("重建后队列 = %v %v", qi, qn)
	}

	check(t, c.RebuildQueues(ctx, agent, nil, []string{"n9"}))
	qi, qn = must2(t, c, agent)
	if len(qi) != 0 || !slices.Equal(qn, []string{"n9"}) {
		t.Fatalf("重建后队列 = %v %v", qi, qn)
	}

	check(t, c.RebuildQueues(ctx, agent, []string{}, nil))
	n, err := rdb.Exists(ctx, c.Keys().QueueInterrupt(agent), c.Keys().QueueNormal(agent)).Result()
	check(t, err)
	if n != 0 {
		t.Fatalf("空内容重建后应删除键，仍有 %d 个", n)
	}
}

func TestEventsAndText(t *testing.T) {
	c, rdb := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-5"
	const run = "run-5"

	text, last, err := c.Snapshot(ctx, agent, run)
	check(t, err)
	if text != "" || last != "0-0" {
		t.Fatalf("空事件流快照 = (%q, %q)", text, last)
	}
	if first := must[string](t)(c.FirstEventID(ctx, agent)); first != "" {
		t.Fatalf("空事件流 FirstEventID = %q", first)
	}
	if evs := must[[]coord.Event](t)(c.ReadEvents(ctx, agent, "0-0", 10, 0)); len(evs) != 0 {
		t.Fatalf("空事件流不应读到事件：%v", evs)
	}

	startID := must[string](t)(c.AppendEvent(ctx, agent, "run_started", []byte(`{"runId":"run-5"}`)))
	ids := []string{startID}
	deltas := []string{"你好", "，", "world", "", "！"}
	for i, d := range deltas {
		id := must[string](t)(c.AppendText(ctx, agent, run, d, "text_delta", fmt.Appendf(nil, `{"i":%d}`, i)))
		if coord.CompareIDs(id, ids[len(ids)-1]) <= 0 {
			t.Fatalf("条目 ID 应递增：%s 后为 %s", ids[len(ids)-1], id)
		}
		ids = append(ids, id)
	}
	assertPTTL(t, rdb, c.Keys().RunText(run), testRunTextTTL)

	text, last, err = c.Snapshot(ctx, agent, run)
	check(t, err)
	if want := strings.Join(deltas, ""); text != want {
		t.Fatalf("快照文字 = %q，期望 %q", text, want)
	}
	if last != ids[len(ids)-1] {
		t.Fatalf("快照 lastID = %q，期望 %q", last, ids[len(ids)-1])
	}
	if got := must[string](t)(c.RunText(ctx, run)); got != text {
		t.Fatalf("RunText = %q，期望 %q", got, text)
	}
	// runID 为空时不读文字。
	if text2, last2, err := c.Snapshot(ctx, agent, ""); err != nil || text2 != "" || last2 != last {
		t.Fatalf("空 runID 快照 = (%q, %q, %v)", text2, last2, err)
	}

	// 从快照位置之后读取：阻塞超时返回空结果而非错误。
	start := time.Now()
	evs, err := c.ReadEvents(ctx, agent, last, 10, 100*time.Millisecond)
	check(t, err)
	if len(evs) != 0 {
		t.Fatalf("快照之后不应有事件：%v", evs)
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("ReadEvents 应阻塞到超时")
	}

	// 阻塞读取期间追加的事件能被读到。
	done := make(chan []coord.Event, 1)
	go func() {
		evs, _ := c.ReadEvents(ctx, agent, last, 10, 5*time.Second)
		done <- evs
	}()
	time.Sleep(100 * time.Millisecond)
	laterID := must[string](t)(c.AppendText(ctx, agent, run, "后续", "text_delta", []byte("later")))
	ids = append(ids, laterID)
	select {
	case evs := <-done:
		if len(evs) != 1 || evs[0].ID != laterID || evs[0].Type != "text_delta" || string(evs[0].Data) != "later" {
			t.Fatalf("阻塞读取得到 %+v，期望仅 %s", evs, laterID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("阻塞读取未返回")
	}

	// 从中间位置读取：按顺序返回剩余事件且不重复。
	mid := 2
	evs, err = c.ReadEvents(ctx, agent, ids[mid], 100, 0)
	check(t, err)
	gotIDs := make([]string, len(evs))
	for i, e := range evs {
		gotIDs[i] = e.ID
	}
	if !slices.Equal(gotIDs, ids[mid+1:]) {
		t.Fatalf("从 %s 之后读到 %v，期望 %v", ids[mid], gotIDs, ids[mid+1:])
	}
	if evs[0].Type != "text_delta" || string(evs[0].Data) != fmt.Sprintf(`{"i":%d}`, mid) {
		t.Fatalf("事件内容不符：%+v", evs[0])
	}

	// COUNT 限制条数。
	evs, err = c.ReadEvents(ctx, agent, "0-0", 2, 0)
	check(t, err)
	if len(evs) != 2 || evs[0].ID != startID || evs[0].Type != "run_started" || string(evs[0].Data) != `{"runId":"run-5"}` {
		t.Fatalf("COUNT=2 读取得到 %+v", evs)
	}

	if first := must[string](t)(c.FirstEventID(ctx, agent)); first != startID {
		t.Fatalf("FirstEventID = %q，期望 %q", first, startID)
	}

	check(t, c.DeleteRunText(ctx, run))
	if got := must[string](t)(c.RunText(ctx, run)); got != "" {
		t.Fatalf("删除后 RunText = %q", got)
	}
	if text, _, err := c.Snapshot(ctx, agent, run); err != nil || text != "" {
		t.Fatalf("删除后快照文字 = %q（%v）", text, err)
	}
}

func TestStreamTrim(t *testing.T) {
	c, _ := newCoord(t, 10)
	ctx := context.Background()
	const agent = "agent-6"
	var firstID, lastID string
	for i := range 500 {
		var id string
		if i%2 == 0 {
			id = must[string](t)(c.AppendEvent(ctx, agent, "e", nil))
		} else {
			id = must[string](t)(c.AppendText(ctx, agent, "run-6", "x", "e", nil))
		}
		if i == 0 {
			firstID = id
		}
		lastID = id
	}
	retained := must[string](t)(c.FirstEventID(ctx, agent))
	if coord.CompareIDs(retained, firstID) <= 0 {
		t.Fatalf("MAXLEN ~ 应裁剪早期条目：最早保留 %s，首条 %s", retained, firstID)
	}
	if _, last, err := c.Snapshot(ctx, agent, "run-6"); err != nil || last != lastID {
		t.Fatalf("裁剪后快照 lastID = %q（%v），期望 %q", last, err, lastID)
	}
	if text := must[string](t)(c.RunText(ctx, "run-6")); len(text) != 250 {
		t.Fatalf("回复文字长度 = %d，期望 250", len(text))
	}
}

func TestCompareIDs(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0-0", "0-0", 0},
		{"1-0", "0-0", 1},
		{"0-0", "1-0", -1},
		{"1700000000000-1", "1700000000000-0", 1},
		{"1700000000000-2", "1700000000000-10", -1}, // 按数值而非字典序
		{"999-0", "1000-0", -1},
		{"1700000000001-0", "1700000000000-99", 1},
		{"5", "5-0", 0},
		{"18446744073709551615-18446744073709551615", "18446744073709551615-18446744073709551614", 1},
	}
	for _, tc := range cases {
		if got := coord.CompareIDs(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareIDs(%q, %q) = %d，期望 %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestInterruptPubSub(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const agent = "agent-7"

	ch, err := c.SubscribeInterrupts(ctx, agent)
	check(t, err)
	// 其他 Agent 的命令不应收到。
	check(t, c.PublishInterrupt(ctx, "agent-other", "run-x"))
	check(t, c.PublishInterrupt(ctx, agent, "run-1"))
	check(t, c.PublishInterrupt(ctx, agent, "run-2"))
	for _, want := range []string{"run-1", "run-2"} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("收到 %q，期望 %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("未收到 %q", want)
		}
	}

	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ctx 结束后通道应关闭")
	}
}

func TestPing(t *testing.T) {
	c, _ := newCoord(t, 1000)
	check(t, c.Ping(context.Background()))
}

func TestPromote(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-promote"
	for _, id := range []string{"n1", "n2", "n3"} {
		check(t, c.PushNormal(ctx, agent, id))
	}
	check(t, c.PushInterrupt(ctx, agent, "i1"))
	if got := must[int64](t)(c.Promote(ctx, agent, "n2")); got != 1 {
		t.Fatalf("应从普通队列移到打断队列尾部：%d", got)
	}
	if got := must[int64](t)(c.Promote(ctx, agent, "n3")); got != 1 {
		t.Fatalf("第二次转换：%d", got)
	}
	qi, qn := must2(t, c, agent)
	if !slices.Equal(qi, []string{"i1", "n2", "n3"}) || !slices.Equal(qn, []string{"n1"}) {
		t.Fatalf("转换后的队列不对：%v %v", qi, qn)
	}
	if got := must[int64](t)(c.Promote(ctx, agent, "n2")); got != 2 {
		t.Fatalf("已在打断队列应返回 2：%d", got)
	}
	if got := must[int64](t)(c.Promote(ctx, agent, "gone")); got != 0 {
		t.Fatalf("两个队列都没有应返回 0：%d", got)
	}
	qi, qn = must2(t, c, agent)
	if !slices.Equal(qi, []string{"i1", "n2", "n3"}) || !slices.Equal(qn, []string{"n1"}) {
		t.Fatalf("幂等与缺失不应改动队列：%v %v", qi, qn)
	}
}

// 转换与交接并发：每个执行恰好被交接一次，不会同时出现在两个队列。
func TestPromoteRacesHandoff(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const inst = "inst-race"
	for round := range 30 {
		agent := fmt.Sprintf("agent-race-%d", round)
		ids := []string{}
		for i := range 40 {
			id := fmt.Sprintf("r%d-%d", round, i)
			ids = append(ids, id)
			check(t, c.PushNormal(ctx, agent, id))
		}
		owner := coord.LockValue("head", inst)
		if !must[bool](t)(c.AcquireIfIdle(ctx, agent, "")) && !must[bool](t)(c.Restore(ctx, agent, owner)) {
			t.Fatal("锁")
		}
		popped := []string{}
		var wg sync.WaitGroup
		wg.Go(func() {
			cur := owner
			for {
				next, ok, err := c.Handoff(ctx, agent, cur, inst)
				if err != nil || !ok {
					t.Errorf("交接失败：%v %v", ok, err)
					return
				}
				if next == "" {
					return
				}
				popped = append(popped, next)
				cur = coord.LockValue(next, inst)
			}
		})
		for _, id := range ids {
			wg.Go(func() {
				if _, err := c.Promote(ctx, agent, id); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		seen := map[string]int{}
		for _, id := range popped {
			seen[id]++
		}
		qi, qn := must2(t, c, agent)
		for _, id := range append(qi, qn...) {
			seen[id]++
		}
		for _, id := range ids {
			if seen[id] != 1 {
				t.Fatalf("第 %d 轮：%s 出现 %d 次（已交接 %v，剩余 %v / %v）", round, id, seen[id], popped, qi, qn)
			}
		}
	}
}
