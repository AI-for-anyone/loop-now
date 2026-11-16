package store_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// These tests arrange the loaded history into turns the same way the console conversation stream does (buildTurns in
// web/src/features/chat/turns.ts), and check that "turnsBefore of the earliest loaded page + the turn's position in
// the loaded history" equals its index in the whole conversation.

func voidedRun(r store.Run) bool {
	switch r.EndReason {
	case "user_cancelled", "merged_into_interrupt", "no_pending_messages":
		return true
	}
	return false
}

// refTurns is a Go version of buildTurns: it lists runs in the order their messages appear, adds the turn in progress
// from the live status (current, may be nil), drops queued and voided runs, and stable-sorts by start time (queue
// time when there is none).
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
	global  map[uuid.UUID]int // index of each turn in the whole conversation
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

// newTurnFixture writes a conversation covering the various cases, and returns all its messages and all turns ordered
// as buildTurns would:
//
//	a    completed
//	b    while in progress, receives queued q1, q2 which is withdrawn right away (voided), and interrupt i1; b is
//	     interrupted
//	i1   the interrupt message runs before q1, which was queued earlier
//	q1   completed
//	v    voided after starting because there were no pending messages (no_pending_messages)
//	--   session restart (epoch 2); numbering continues
//	w    taken over while queued by new sleep work (sleep_superseded); has no start time, ordered by queue time
//	c    completed
//	--   a manual compaction (console_compact; no conversation messages, does not count)
//	d    in progress; e is still queued (does not count)
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
	// A manual compaction (/compact) run has no user message and no reply, does not enter the conversation stream, and
	// does not count as a turn.
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

// window is the loaded consecutive pages (pages[0] is the earliest); atLatest means the latest has been loaded, in
// which case the console appends the turn in progress at the end.
// Returns the number of turns whose numbering does not match their index in the whole conversation.
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

// name is the turn's name in this fixture (a, b, i1, ...), used in error messages.
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

// Paging from the latest page towards older ones (the console's normal fetch): for every page size, after each page
// is loaded, the numbering matches the whole conversation; interrupt messages running first, voided runs, queued runs,
// a session restart and turns without a start time in between do not affect it.
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

// Around-run fetch: for the anchored page and every page paged out to either side, turnsBefore equals that of the page
// a normal fetch returns starting from the same message; after paging forward to the latest, the numbering matches the
// whole conversation. Before the latest has been loaded, the only case that makes later numbers too small is an
// interrupt message after the loaded history running before a message queued within it (see the note on
// MessagePage.turnsBefore).
func TestMessagePageTurnNumbersAroundRun(t *testing.T) {
	f := newTurnFixture(t)
	// fromHere is the turnsBefore this page should give under the normal fetch when everything from seq onward is
	// loaded: the index of the first turn in the whole conversation at that point.
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
				// First page forward to the latest, then back to the earliest, checking after each page is loaded.
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

// hiddenAbove reports whether the loaded history (seq first..last) has this case: a turn that has no messages in the
// loaded range but has messages later than last (an interrupt message sent after the loaded range that ran before a
// message queued within it; or a turn in progress during the whole loaded range, with its reply afterwards), and that
// is ordered before some turn in the loaded history. The console then does not show it, and later turn numbers are
// too small until its messages are loaded.
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

// Empty page and empty conversation: with no messages, turnsBefore is computed from the position where this page would
// have started.
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
	// After the latest message: the messages after it only contain the turn of d in progress (added from the live
	// status), and there are 6 turns before it.
	if p := f.list(store.MessageQuery{After: &last, Limit: 5}); len(p.Items) != 0 || p.TurnsBefore != 6 || p.TurnsTotal != 7 {
		t.Fatalf("最新一条之后：%+v", p)
	}
	// The latest page with only one message still queued: the user message of d (in progress) is on an earlier page,
	// and d is still the first turn after this page.
	if p := f.list(store.MessageQuery{Limit: 1}); len(p.Items) != 1 || p.Items[0].Run.Status != "queued" || p.TurnsBefore != 6 {
		t.Fatalf("只有排队消息的一页：%+v", p)
	}
}
