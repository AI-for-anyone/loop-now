package store_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// 这组测试按控制台对话流（web/src/features/chat/turns.ts 的 buildTurns）的做法把已加载的历史排成轮次，
// 检查“已加载的最早一页的 turnsBefore + 轮次在已加载历史中的位置”等于它在整个对话里的下标。

func voidedRun(r store.Run) bool {
	switch r.EndReason {
	case "user_cancelled", "merged_into_interrupt", "no_pending_messages":
		return true
	}
	return false
}

// refTurns 是 buildTurns 的 Go 版本：按消息出现的先后列出执行，补上实时状态里进行中的那一轮（current，可为空），
// 去掉排队中的与作废的，按开始时间（没有时按入队时间）稳定排序。
func refTurns(items []store.MessageWithRun, current *store.Run) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var runs []store.Run
	for _, it := range items {
		if !seen[it.Run.ID] {
			seen[it.Run.ID] = true
			runs = append(runs, it.Run)
		}
	}
	if current != nil && !seen[current.ID] {
		runs = append(runs, *current)
	}
	runs = slices.DeleteFunc(runs, func(r store.Run) bool { return r.Status == "queued" || voidedRun(r) })
	slices.SortStableFunc(runs, func(a, b store.Run) int {
		key := func(r store.Run) int64 {
			if r.StartedAt != nil {
				return r.StartedAt.UnixMicro()
			}
			return r.QueuedAt.UnixMicro()
		}
		return cmp.Compare(key(a), key(b))
	})
	ids := make([]uuid.UUID, len(runs))
	for i, r := range runs {
		ids[i] = r.ID
	}
	return ids
}

type turnFixture struct {
	*recEnv
	all     []store.MessageWithRun
	global  map[uuid.UUID]int // 每一轮在整个对话里的下标
	order   []uuid.UUID
	names   map[uuid.UUID]string
	current *store.Run
}

func (e *recEnv) list(q store.MessageQuery) store.MessagePage {
	e.t.Helper()
	page, err := e.s.ListMessages(e.ctx, e.agent, q)
	if err != nil {
		e.t.Fatal(err)
	}
	return page
}

func (e *recEnv) start(r store.Run) {
	e.t.Helper()
	if _, ok, err := e.s.StartQueuedRun(e.ctx, r.ID, uuid.Must(uuid.NewV7())); err != nil || !ok {
		e.t.Fatalf("开始排队执行失败：%v %v", ok, err)
	}
}

func (e *recEnv) finish(r store.Run, status, reason string) {
	e.t.Helper()
	if _, _, ok, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: status, EndReason: reason, Text: "reply"}); err != nil || !ok {
		e.t.Fatalf("结束执行失败：%v %v", ok, err)
	}
}

// newTurnFixture 写出一段覆盖各种情况的对话，返回它的全部消息与按 buildTurns 排好的全部轮次：
//
//	a    完成
//	b    进行中时收到排队的 q1、随即撤回的 q2（作废）、打断 i1；b 被打断
//	i1   打断消息先于更早排队的 q1 执行
//	q1   完成
//	v    开始后发现没有待处理消息而作废（no_pending_messages）
//	——   会话重开（epoch 2），编号继续
//	w    排队中被 sleep 的新工作承接（sleep_superseded），没有开始时间，按入队时间排
//	c    完成
//	——   一次手动压缩（console_compact，没有对话消息，不算）
//	d    进行中；e 仍在排队（不算）
func newTurnFixture(t *testing.T) *turnFixture {
	e := newRecEnv(t)
	a := e.send(true, "")
	e.finish(a, "completed", "")
	b := e.send(true, "")
	q1 := e.send(false, "queue")
	q2 := e.send(false, "queue")
	if err := e.s.Pool.InTx(e.ctx, func(tx *db.Tx) error {
		_, _, _, err := e.s.CancelQueuedRunTx(e.ctx, tx, e.agent, q2.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	i1 := e.send(false, "interrupt")
	if _, err := e.s.MarkAborting(e.ctx, e.agent, i1.ID); err != nil {
		t.Fatal(err)
	}
	e.finish(b, "interrupted", "user_interrupt")
	e.start(i1)
	e.finish(i1, "completed", "")
	e.start(q1)
	e.finish(q1, "completed", "")
	v := e.send(false, "")
	e.start(v)
	e.finish(v, "interrupted", "no_pending_messages")
	if _, err := e.s.ResetSession(e.ctx, e.agent); err != nil {
		t.Fatal(err)
	}
	w := e.send(false, "")
	mustExec(t, e.s, `UPDATE runs SET status = 'interrupted', end_reason = 'sleep_superseded', ended_at = now() WHERE id = $1 AND status = 'queued'`, w.ID)
	c := e.send(true, "")
	e.finish(c, "completed", "")
	// 手动压缩（/compact）的执行没有用户消息也没有回复，不进对话流，不算一轮。
	mustExec(t, e.s, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin, queued_at, started_at, running_at, ended_at)
		VALUES ($1, $2, NULL, 1, 1, (SELECT coalesce(max(queue_seq), 0) + 1 FROM runs), 'completed', 'console_compact', now(), now(), now(), now())`, uuid.Must(uuid.NewV7()), e.agent)
	d := e.send(true, "")
	e.send(false, "queue")

	f := &turnFixture{recEnv: e, global: map[uuid.UUID]int{}}
	full := e.list(store.MessageQuery{Limit: 200})
	f.all = full.Items
	if len(f.all) != 16 {
		t.Fatalf("消息条数不对：%d", len(f.all))
	}
	if f.current, _ = e.s.ActiveRun(e.ctx, e.agent); f.current == nil || f.current.ID != d.ID {
		t.Fatal("d 应在进行中")
	}
	f.order = refTurns(f.all, f.current)
	want := []uuid.UUID{a.ID, b.ID, i1.ID, q1.ID, w.ID, c.ID, d.ID}
	if !slices.Equal(f.order, want) {
		t.Fatalf("全部轮次的顺序不对：%v，期望 %v", f.order, want)
	}
	f.names = map[uuid.UUID]string{}
	for i, id := range f.order {
		f.global[id] = i
		f.names[id] = []string{"a", "b", "i1", "q1", "w", "c", "d"}[i]
	}
	if full.TurnsBefore != 0 || full.TurnsTotal != int64(len(want)) {
		t.Fatalf("一页取完时 turnsBefore=%d turnsTotal=%d", full.TurnsBefore, full.TurnsTotal)
	}
	var epochs []int
	for _, it := range f.all {
		if !slices.Contains(epochs, it.SessionEpoch) {
			epochs = append(epochs, it.SessionEpoch)
		}
	}
	if len(epochs) != 2 {
		t.Fatalf("对话应跨两个会话：%v", epochs)
	}
	return f
}

// window 是已加载的连续几页（pages[0] 最早）；atLatest 表示已加载到最新，这时控制台把进行中的那一轮补在末尾。
// 返回编号与整个对话里的下标不一致的轮次数。
func (f *turnFixture) numbering(pages []store.MessagePage, atLatest bool) (wrong int, detail string) {
	var items []store.MessageWithRun
	for _, p := range pages {
		items = append(items, p.Items...)
	}
	var current *store.Run
	if atLatest {
		current = f.current
	}
	for i, id := range refTurns(items, current) {
		if got, want := int(pages[0].TurnsBefore)+i, f.global[id]; got != want {
			wrong++
			detail += " " + f.name(id)
		}
	}
	return wrong, detail
}

// name 是轮次在这组夹具里的名字（a、b、i1……），用于报错。
func (f *turnFixture) name(id uuid.UUID) string {
	if n, ok := f.names[id]; ok {
		return n
	}
	return id.String()
}

func seqs(pages []store.MessagePage) (first, last int64) {
	for _, p := range pages {
		if len(p.Items) > 0 {
			if first == 0 {
				first = p.Items[0].Seq
			}
			last = p.Items[len(p.Items)-1].Seq
		}
	}
	return first, last
}

// 从最新一页起向更早翻页（控制台的普通取法），每种页大小、每加载一页后编号都与整个对话一致；
// 打断消息先执行、作废的执行、排队中的执行、会话重开、没有开始时间的轮次夹在中间都不影响。
func TestMessagePageTurnNumbersWhilePaging(t *testing.T) {
	f := newTurnFixture(t)
	for limit := 1; limit <= len(f.all); limit++ {
		pages := []store.MessagePage{f.list(store.MessageQuery{Limit: limit})}
		for {
			oldest := pages[0]
			if oldest.TurnsTotal != int64(len(f.order)) {
				t.Fatalf("limit=%d：turnsTotal=%d，期望 %d", limit, oldest.TurnsTotal, len(f.order))
			}
			if wrong, detail := f.numbering(pages, true); wrong > 0 {
				first, last := seqs(pages)
				t.Errorf("limit=%d，已加载 seq %d..%d，turnsBefore=%d：%d 轮编号不对%s", limit, first, last, oldest.TurnsBefore, wrong, detail)
			}
			if !oldest.HasMore || len(oldest.Items) == 0 {
				break
			}
			before := oldest.Items[0].ID
			pages = append([]store.MessagePage{f.list(store.MessageQuery{Before: &before, Limit: limit})}, pages...)
		}
		if first, _ := seqs(pages); first != f.all[0].Seq {
			t.Fatalf("limit=%d：没有翻到最早的消息", limit)
		}
	}
}

// 定位取法：定位页及向两边翻出的每一页，turnsBefore 都与普通取法从同一条消息起的一页相同；
// 向后翻到最新之后，编号与整个对话一致。还没有加载到最新时，只有已加载历史之后的打断消息
// 先于其中排队的消息执行这一种情况会让排在后面的编号偏小（见 MessagePage.turnsBefore 的说明）。
func TestMessagePageTurnNumbersAroundRun(t *testing.T) {
	f := newTurnFixture(t)
	// fromHere 是普通取法下从 seq 起向后全部加载时这一页应给的 turnsBefore：那时第一轮在整个对话里的下标。
	fromHere := func(seq int64) int64 {
		i := slices.IndexFunc(f.all, func(it store.MessageWithRun) bool { return it.Seq >= seq })
		if turns := refTurns(f.all[i:], f.current); len(turns) > 0 {
			return int64(f.global[turns[0]])
		}
		return int64(len(f.order))
	}
	knownGaps := 0
	for _, anchor := range f.all {
		if anchor.Role != "user" {
			continue
		}
		for limit := 1; limit <= len(f.all); limit++ {
			run := anchor.Run.ID
			pages := []store.MessagePage{f.list(store.MessageQuery{AroundRun: &run, Limit: limit})}
		load:
			for {
				oldest, newest := pages[0], pages[len(pages)-1]
				if want := fromHere(oldest.Items[0].Seq); oldest.TurnsBefore != want {
					t.Fatalf("定位 %s limit=%d：最早一页 turnsBefore=%d，普通取法为 %d", f.name(run), limit, oldest.TurnsBefore, want)
				}
				if oldest.TurnsTotal != int64(len(f.order)) || newest.TurnsTotal != int64(len(f.order)) {
					t.Fatalf("定位 %s limit=%d：turnsTotal 不对", f.name(run), limit)
				}
				atLatest := !newest.HasMoreAfter
				if wrong, detail := f.numbering(pages, atLatest); wrong > 0 {
					first, last := seqs(pages)
					if atLatest || !f.hiddenAbove(first, last) {
						t.Errorf("定位 %s limit=%d，已加载 seq %d..%d：%d 轮编号不对%s", f.name(run), limit, first, last, wrong, detail)
					}
					knownGaps++
				}
				// 先向更新翻到最新，再向更早翻到最早，每加载一页检查一次。
				switch {
				case newest.HasMoreAfter:
					after := newest.Items[len(newest.Items)-1].ID
					pages = append(pages, f.list(store.MessageQuery{After: &after, Limit: limit}))
				case oldest.HasMore:
					before := oldest.Items[0].ID
					pages = append([]store.MessagePage{f.list(store.MessageQuery{Before: &before, Limit: limit})}, pages...)
				default:
					break load
				}
			}
			if first, _ := seqs(pages); first != f.all[0].Seq {
				t.Fatalf("定位 %s limit=%d：没有翻到最早的消息", f.name(run), limit)
			}
		}
	}
	t.Logf("定位取法还没有加载到最新、已加载范围之后有排在前面的轮次的窗口：%d 个", knownGaps)
}

// hiddenAbove 判断已加载历史（seq first..last）是否有这种情况：一轮在已加载范围里没有消息、却有比 last 更晚的消息
// （打断消息发在已加载范围之后、先于其中排队的消息执行；或一轮在整个已加载范围期间都在进行，回复在之后），
// 并且它排在已加载历史中的某一轮之前。这时控制台不显示它，后面的轮次编号偏小，加载到它的消息后恢复。
func (f *turnFixture) hiddenAbove(first, last int64) bool {
	shown := map[uuid.UUID]bool{}
	maxShown := -1
	for _, it := range f.all {
		if i, ok := f.global[it.Run.ID]; ok && it.Seq >= first && it.Seq <= last {
			shown[it.Run.ID] = true
			maxShown = max(maxShown, i)
		}
	}
	for _, it := range f.all {
		if i, ok := f.global[it.Run.ID]; ok && !shown[it.Run.ID] && it.Seq > last && i < maxShown {
			return true
		}
	}
	return false
}

// 空页与空对话：没有消息时 turnsBefore 按这一页本该开始的位置计算。
func TestMessagePageTurnNumbersEmpty(t *testing.T) {
	e := newRecEnv(t)
	if p := e.list(store.MessageQuery{Limit: 50}); len(p.Items) != 0 || p.TurnsBefore != 0 || p.TurnsTotal != 0 || p.HasMore {
		t.Fatalf("空对话：%+v", p)
	}

	f := newTurnFixture(t)
	first, last := f.all[0].ID, f.all[len(f.all)-1].ID
	if p := f.list(store.MessageQuery{Before: &first, Limit: 5}); len(p.Items) != 0 || p.TurnsBefore != 0 || p.TurnsTotal != 7 {
		t.Fatalf("最早一条之前：%+v", p)
	}
	// 最新一条之后：之后的消息里只有进行中的 d 所在的一轮（由实时状态补上），它之前有 6 轮。
	if p := f.list(store.MessageQuery{After: &last, Limit: 5}); len(p.Items) != 0 || p.TurnsBefore != 6 || p.TurnsTotal != 7 {
		t.Fatalf("最新一条之后：%+v", p)
	}
	// 只有一条仍在排队的消息的最新一页：进行中的 d 的用户消息在更早的页里，它仍是这一页之后的第一轮。
	if p := f.list(store.MessageQuery{Limit: 1}); len(p.Items) != 1 || p.Items[0].Run.Status != "queued" || p.TurnsBefore != 6 {
		t.Fatalf("只有排队消息的一页：%+v", p)
	}
}
