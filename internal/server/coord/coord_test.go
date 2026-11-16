package coord_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/coord"
)

const ()

// clock is a clock that never moves: event IDs within the same millisecond increase by sequence number.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func newCoord(t *testing.T, maxLen int64) (*coord.Coord, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	return coord.New(coord.Options{
		Prefix:       "test:",
		StreamMaxLen: maxLen,
		Now:          clk.now,
	}), clk
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
		t.Fatalf("lock holder = %q, want %q", got, want)
	}
}

func TestKeys(t *testing.T) {
	k := coord.Keys{Prefix: "loopnow:"}
	cases := map[string]string{
		k.Lock("a1"):    "loopnow:agent:a1:lock",
		k.Cmd("a1"):     "loopnow:agent:a1:cmd",
		k.Events("a1"):  "loopnow:agent:a1:events",
		k.RunText("r1"): "loopnow:run:r1:text",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key name %q, want %q", got, want)
		}
	}
	if v := coord.LockValue("r1", "i1"); v != "r1|i1" {
		t.Errorf("LockValue = %q", v)
	}
	c := coord.New(coord.Options{Prefix: "p:"})
	if c.Keys().Prefix != "p:" {
		t.Errorf("Keys().Prefix = %q", c.Keys().Prefix)
	}
}

func TestAcquire(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-1"
	ownerA := coord.LockValue("run-a", "inst")

	// Free: succeeds (busy locks do not expire).
	if !must[bool](t)(c.Acquire(ctx, agent, ownerA)) {
		t.Fatal("should get the lock when it is free")
	}
	assertOwner(t, c, agent, ownerA)

	// Already held: rejected without changing the holder.
	if must[bool](t)(c.Acquire(ctx, agent, coord.LockValue("run-b", "inst"))) {
		t.Fatal("should not get the lock while it is held")
	}
	assertOwner(t, c, agent, ownerA)
}

func TestAcquireConcurrent(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	results := make([]bool, n)
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			results[i], errs[i] = c.Acquire(ctx, "agent-c", coord.LockValue(fmt.Sprint("run-", i), "inst"))
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
		t.Fatalf("concurrent lock acquisition succeeded %d times, want 1", won)
	}
}

func TestHoldRestoreRelease(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-2"
	owner := coord.LockValue("run-a", "inst")
	other := coord.LockValue("run-b", "inst")

	// When the lock is free, Hold sets it to owner; when it is already owner, nothing changes.
	if !must[bool](t)(c.Hold(ctx, agent, owner)) {
		t.Fatal("Hold should get the lock when it is free")
	}
	assertOwner(t, c, agent, owner)
	if !must[bool](t)(c.Hold(ctx, agent, owner)) {
		t.Fatal("Hold should succeed when the holder matches")
	}
	assertOwner(t, c, agent, owner)

	// Rejected without changes when the holder does not match.
	if must[bool](t)(c.Hold(ctx, agent, other)) {
		t.Fatal("Hold should fail when the holder does not match")
	}
	assertOwner(t, c, agent, owner)

	// Restore only sets the lock when it does not exist.
	if must[bool](t)(c.Restore(ctx, agent, other)) {
		t.Fatal("Restore should not overwrite an existing lock")
	}
	assertOwner(t, c, agent, owner)

	// Release deletes after checking the holder.
	if must[bool](t)(c.Release(ctx, agent, other)) {
		t.Fatal("Release should not delete when the holder does not match")
	}
	assertOwner(t, c, agent, owner)
	if !must[bool](t)(c.Release(ctx, agent, owner)) {
		t.Fatal("Release should delete when the holder matches")
	}
	assertOwner(t, c, agent, "")
	if must[bool](t)(c.Release(ctx, agent, owner)) {
		t.Fatal("Release should return false when the lock does not exist")
	}

	if !must[bool](t)(c.Restore(ctx, agent, other)) {
		t.Fatal("Restore should set the lock when it does not exist")
	}
	assertOwner(t, c, agent, other)
}

func TestSwap(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-3"
	owner := coord.LockValue("run-0", "inst")
	if !must[bool](t)(c.Acquire(ctx, agent, owner)) {
		t.Fatal("should get the lock")
	}
	// Holder mismatch, or an empty current while the lock exists: no change.
	for _, current := range []string{coord.LockValue("run-zz", "inst"), ""} {
		if must[bool](t)(c.Swap(ctx, agent, current, "x|inst")) {
			t.Fatalf("should not swap the lock when current=%q", current)
		}
		assertOwner(t, c, agent, owner)
	}
	// Hand over to the next run: swap the lock holder.
	next := coord.LockValue("run-1", "inst")
	if !must[bool](t)(c.Swap(ctx, agent, owner, next)) {
		t.Fatal("should swap the lock")
	}
	assertOwner(t, c, agent, next)
	// No next run: delete the lock; when the lock does not exist, a non-empty current does not match.
	if !must[bool](t)(c.Swap(ctx, agent, next, "")) {
		t.Fatal("should delete the lock")
	}
	assertOwner(t, c, agent, "")
	if must[bool](t)(c.Swap(ctx, agent, next, "y|inst")) {
		t.Fatal("a non-empty current should not match when the lock does not exist")
	}
	// An empty current when the lock does not exist: it can take over directly, or do nothing at all.
	if !must[bool](t)(c.Swap(ctx, agent, "", "")) {
		t.Fatal("an empty handoff should succeed when the lock is free")
	}
	assertOwner(t, c, agent, "")
	if !must[bool](t)(c.Swap(ctx, agent, "", "n3|inst-y")) {
		t.Fatal("should be able to take over when the lock is free")
	}
	assertOwner(t, c, agent, "n3|inst-y")
}

func TestEventsAndText(t *testing.T) {
	c, _ := newCoord(t, 1000)
	ctx := context.Background()
	const agent = "agent-5"
	const run = "run-5"

	text, last, err := c.Snapshot(ctx, agent, run)
	check(t, err)
	if text != "" || last != "0-0" {
		t.Fatalf("empty stream snapshot = (%q, %q)", text, last)
	}
	if first := must[string](t)(c.FirstEventID(ctx, agent)); first != "" {
		t.Fatalf("empty stream FirstEventID = %q", first)
	}
	if evs := must[[]coord.Event](t)(c.ReadEvents(ctx, agent, "0-0", 10, 0)); len(evs) != 0 {
		t.Fatalf("an empty stream should yield no events: %v", evs)
	}

	startID := must[string](t)(c.AppendEvent(ctx, agent, "run_started", []byte(`{"runId":"run-5"}`)))
	ids := []string{startID}
	deltas := []string{"你好", ", ", "world", "", "!"} // check-english: zh (multi-byte text round trip through the stream)
	for i, d := range deltas {
		id := must[string](t)(c.AppendText(ctx, agent, run, d, "text_delta", fmt.Appendf(nil, `{"i":%d}`, i)))
		if coord.CompareIDs(id, ids[len(ids)-1]) <= 0 {
			t.Fatalf("entry IDs should increase: %s followed by %s", ids[len(ids)-1], id)
		}
		ids = append(ids, id)
	}

	text, last, err = c.Snapshot(ctx, agent, run)
	check(t, err)
	if want := strings.Join(deltas, ""); text != want {
		t.Fatalf("snapshot text = %q, want %q", text, want)
	}
	if last != ids[len(ids)-1] {
		t.Fatalf("snapshot lastID = %q, want %q", last, ids[len(ids)-1])
	}
	if got := must[string](t)(c.RunText(ctx, run)); got != text {
		t.Fatalf("RunText = %q, want %q", got, text)
	}
	// With an empty runID the text is not read.
	if text2, last2, err := c.Snapshot(ctx, agent, ""); err != nil || text2 != "" || last2 != last {
		t.Fatalf("snapshot with empty runID = (%q, %q, %v)", text2, last2, err)
	}

	// Read from after the snapshot position: a blocking read that times out returns an empty result, not an error.
	start := time.Now()
	evs, err := c.ReadEvents(ctx, agent, last, 10, 100*time.Millisecond)
	check(t, err)
	if len(evs) != 0 {
		t.Fatalf("there should be no events after the snapshot: %v", evs)
	}
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("ReadEvents should block until the timeout")
	}

	// Events appended during a blocking read are seen by it.
	done := make(chan []coord.Event, 1)
	go func() {
		evs, _ := c.ReadEvents(ctx, agent, last, 10, 5*time.Second)
		done <- evs
	}()
	time.Sleep(100 * time.Millisecond)
	laterID := must[string](t)(c.AppendText(ctx, agent, run, "more text", "text_delta", []byte("later")))
	ids = append(ids, laterID)
	select {
	case evs := <-done:
		if len(evs) != 1 || evs[0].ID != laterID || evs[0].Type != "text_delta" || string(evs[0].Data) != "later" {
			t.Fatalf("blocking read got %+v, want only %s", evs, laterID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("blocking read did not return")
	}

	// Reading from a middle position: returns the remaining events in order without duplicates.
	mid := 2
	evs, err = c.ReadEvents(ctx, agent, ids[mid], 100, 0)
	check(t, err)
	gotIDs := make([]string, len(evs))
	for i, e := range evs {
		gotIDs[i] = e.ID
	}
	if !slices.Equal(gotIDs, ids[mid+1:]) {
		t.Fatalf("read after %s got %v, want %v", ids[mid], gotIDs, ids[mid+1:])
	}
	if evs[0].Type != "text_delta" || string(evs[0].Data) != fmt.Sprintf(`{"i":%d}`, mid) {
		t.Fatalf("unexpected event content: %+v", evs[0])
	}

	// COUNT limits the number of entries.
	evs, err = c.ReadEvents(ctx, agent, "0-0", 2, 0)
	check(t, err)
	if len(evs) != 2 || evs[0].ID != startID || evs[0].Type != "run_started" || string(evs[0].Data) != `{"runId":"run-5"}` {
		t.Fatalf("read with COUNT=2 got %+v", evs)
	}

	if first := must[string](t)(c.FirstEventID(ctx, agent)); first != startID {
		t.Fatalf("FirstEventID = %q, want %q", first, startID)
	}

	check(t, c.DeleteRunText(ctx, run))
	if got := must[string](t)(c.RunText(ctx, run)); got != "" {
		t.Fatalf("RunText after delete = %q", got)
	}
	if text, _, err := c.Snapshot(ctx, agent, run); err != nil || text != "" {
		t.Fatalf("snapshot text after delete = %q (%v)", text, err)
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
		t.Fatalf("early entries should be trimmed: oldest kept %s, first written %s", retained, firstID)
	}
	if evs := must[[]coord.Event](t)(c.ReadEvents(ctx, agent, "0-0", 0, 0)); len(evs) != 10 || evs[9].ID != lastID {
		t.Fatalf("only the latest 10 entries should be kept: %d", len(evs))
	}
	if _, last, err := c.Snapshot(ctx, agent, "run-6"); err != nil || last != lastID {
		t.Fatalf("snapshot lastID after trim = %q (%v), want %q", last, err, lastID)
	}
	if text := must[string](t)(c.RunText(ctx, "run-6")); len(text) != 250 {
		t.Fatalf("reply text length = %d, want 250", len(text))
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
		{"1700000000000-2", "1700000000000-10", -1}, // numerically, not lexicographically
		{"999-0", "1000-0", -1},
		{"1700000000001-0", "1700000000000-99", 1},
		{"5", "5-0", 0},
		{"18446744073709551615-18446744073709551615", "18446744073709551615-18446744073709551614", 1},
	}
	for _, tc := range cases {
		if got := coord.CompareIDs(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareIDs(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
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
	// Commands for other Agents must not be received.
	check(t, c.PublishInterrupt(ctx, "agent-other", "run-x"))
	check(t, c.PublishInterrupt(ctx, agent, "run-1"))
	check(t, c.PublishInterrupt(ctx, agent, "run-2"))
	for _, want := range []string{"run-1", "run-2"} {
		select {
		case got := <-ch:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("did not receive %q", want)
		}
	}

	cancel()
	select {
	case _, ok := <-ch:
		for ok {
			_, ok = <-ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the channel should close after ctx ends")
	}
}
