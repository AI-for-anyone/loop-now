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
	"loop-now/internal/server/coord"
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

func (n names) items(q []apitypes.QueueItem) []string {
	out := []string{}
	for _, it := range q {
		out = append(out, n[it.RunID])
	}
	return out
}

// promoteResult is the promote receipt. Queue is the queue as the promotion's own
// transaction left it: the promotion interrupts the running reply, so by the time
// the caller reads the database the driver may already have started the promoted run.
type promoteResult struct {
	Run    apitypes.Run
	Status string
	Queue  []apitypes.QueueItem
}

// promote sends one promote action with a fresh actionId (a new click).
func (e *env) promote(id uuid.UUID) (promoteResult, error) {
	raw, err := e.svc.PromoteToInterrupt(context.Background(), e.agent, id, uuid.New())
	if err != nil {
		return promoteResult{}, err
	}
	var rec apitypes.AgentQueueActionReceipt
	if err := json.Unmarshal(raw, &rec); err != nil {
		e.t.Fatal(err)
	}
	return promoteResult{Run: *rec.Run, Status: rec.Status, Queue: rec.Queue}, nil
}

func (e *env) mustPromote(id uuid.UUID) promoteResult {
	e.t.Helper()
	r, err := e.promote(id)
	if err != nil {
		e.t.Fatalf("promote to interrupt failed: %v", err)
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

// queues returns the names of the queued runs in the database (interrupt and normal, each in dequeue order).
func (e *env) queues(n names) ([]string, []string) {
	e.t.Helper()
	queued, err := e.st.QueuedRuns(context.Background(), e.agent)
	if err != nil {
		e.t.Fatal(err)
	}
	qi, qn := []string{}, []string{}
	for _, r := range queued {
		if r.Priority == 0 {
			qi = append(qi, n[r.ID])
		} else {
			qn = append(qn, n[r.ID])
		}
	}
	return qi, qn
}

// lastQueueEvent returns the newest queue.updated payload on the agent stream.
func (e *env) lastQueueEvent(n names) []string {
	e.t.Helper()
	return e.firstQueueEvent(n, e.newestEvents())
}

// streamMark returns the newest event ID on the agent stream, so queueEventAfter
// can find what the next action emitted.
func (e *env) streamMark() string {
	e.t.Helper()
	if evs := e.newestEvents(); len(evs) > 0 {
		return evs[0].ID
	}
	return "0-0"
}

// queueEventAfter returns the first queue.updated payload after mark: the one the
// next action emitted, even if the driver has moved the queue on since.
func (e *env) queueEventAfter(n names, mark string) []string {
	e.t.Helper()
	evs, err := e.co.ReadEvents(context.Background(), e.agent.String(), mark, 0, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.firstQueueEvent(n, evs)
}

func (e *env) firstQueueEvent(n names, evs []coord.Event) []string {
	e.t.Helper()
	for _, m := range evs {
		if m.Type != apitypes.EventQueueUpdated {
			continue
		}
		var ev apitypes.SseQueueUpdated
		if err := json.Unmarshal(m.Data, &ev); err != nil {
			e.t.Fatal(err)
		}
		return n.of(ev.QueuedRuns)
	}
	e.t.Fatal("no queue.updated event")
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

// Promoting while running: the current reply is interrupted and the promoted message runs before all normally queued
// ones.
func TestPromoteInterruptsRunningReplyAndRunsBeforeQueue(t *testing.T) {
	e := newEnv(t)
	n := names{}
	a := e.send2(n, "block A", "")
	e.waitStarted("block A")
	e.send2(n, "normal 1", "queue")
	two := e.send2(n, "normal 2", "queue")
	mark := e.streamMark()
	got := e.mustPromote(two.Run.ID)
	// The promotion interrupts block A and the driver then starts "normal 2" on its own.
	// Wait for that, so the checks below always run after the queue has moved on
	// and must look at what the promotion itself returned and emitted.
	e.waitStarted("normal 2")
	if got.Run.WhenBusy == nil || *got.Run.WhenBusy != "interrupt" || !slices.Equal(n.items(got.Queue), []string{"normal 2", "normal 1"}) {
		t.Fatalf("wrong promote result: %+v %v", got.Run, n.items(got.Queue))
	}
	if q := e.queueEventAfter(n, mark); !slices.Equal(q, []string{"normal 2", "normal 1"}) {
		t.Fatalf("wrong queue.updated order: %v", q)
	}
	e.waitIdleOrDump(names{})
	if order := e.ex.messages(); !slices.Equal(order, []string{"block A", "normal 2", "normal 1"}) {
		t.Fatalf("wrong execution order: %v", order)
	}
	r, err := e.st.GetRun(context.Background(), a.Run.ID)
	if err != nil || r.Status != "interrupted" || r.EndReason != "user_interrupt" {
		t.Fatalf("the running reply should be interrupted: %+v %v", r, err)
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
		t.Fatalf("the timeline should keep the normal queueing and add the promotion to interrupt: %s", raw)
	}
}

// It goes after existing interrupts and before all normal queued runs; consecutive promotions are ordered by when they
// were promoted; repeating a promotion is idempotent.
func TestPromoteOrderingAfterExistingInterrupts(t *testing.T) {
	e := newEnv(t)
	n := names{}
	e.send2(n, "hold A", "")
	e.waitStarted("hold A")
	e.send2(n, "normal 1", "queue")
	two := e.send2(n, "normal 2", "queue")
	three := e.send2(n, "normal 3", "queue")
	e.send2(n, "interrupt 1", "interrupt") // hold A ignores interrupts, so "interrupt 1" stays in the interrupt queue
	time.Sleep(100 * time.Millisecond)
	drain(e.ex.intr)

	got := e.mustPromote(two.Run.ID)
	want := []string{"interrupt 1", "normal 2", "normal 1", "normal 3"}
	if !slices.Equal(n.items(got.Queue), want) || !slices.Equal(e.snapshotQueue(n), want) || !slices.Equal(e.lastQueueEvent(n), want) {
		t.Fatalf("wrong order after the first promotion: result %v snapshot %v", n.items(got.Queue), e.snapshotQueue(n))
	}
	if qi, qn := e.queues(n); !slices.Equal(qi, []string{"interrupt 1", "normal 2"}) || !slices.Equal(qn, []string{"normal 1", "normal 3"}) {
		t.Fatalf("wrong Redis queues: %v / %v", qi, qn)
	}
	got = e.mustPromote(three.Run.ID)
	want = []string{"interrupt 1", "normal 2", "normal 3", "normal 1"}
	if !slices.Equal(n.items(got.Queue), want) || !slices.Equal(e.snapshotQueue(n), want) {
		t.Fatalf("consecutive promotions should be ordered by when they were promoted: %v", n.items(got.Queue))
	}
	if qi, qn := e.queues(n); !slices.Equal(qi, []string{"interrupt 1", "normal 2", "normal 3"}) || !slices.Equal(qn, []string{"normal 1"}) {
		t.Fatalf("wrong Redis queues: %v / %v", qi, qn)
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
	if !slices.Equal(n.items(again.Queue), want) || events(store.EventRunAbortRequested) != aborts || events(store.EventRunPromoted) != promoted {
		t.Fatal("promoting an interrupt again should be idempotent: no second interrupt, no second record")
	}
	snap, _, err := e.svc.Snapshot(context.Background(), e.agent)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.QueuedRuns {
		wb := *r.WhenBusy
		if (n[r.ID] == "normal 1") != (wb == "queue") {
			t.Fatalf("wrong whenBusy in the snapshot: %s=%s", n[r.ID], wb)
		}
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	if order := e.ex.messages(); !slices.Equal(order, []string{"hold A", "interrupt 1", "normal 2", "normal 3", "normal 1"}) {
		t.Fatalf("wrong actual execution order: %v", order)
	}
}

// Already claimed / already ended returns run_not_queued with the current status; other origins 422; nonexistent or not
// belonging to the Agent 404.
func TestPromoteConflictsAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	running := e.mustSend("block current", "")
	e.waitStarted("block current")
	_, err := e.promote(running.Run.ID)
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeRunNotQueued {
		t.Fatalf("an in-flight run should return run_not_queued: %v", err)
	}
	if d := ae.Details.(store.RunNotQueuedDetails); d.RunID == nil || *d.RunID != running.Run.ID || d.Status == nil || *d.Status != "starting" && *d.Status != "running" {
		t.Fatalf("the conflict details should carry the current status: %+v", d)
	}
	if _, err = e.promote(uuid.New()); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("a nonexistent run should be 404: %v", err)
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
		t.Fatalf("another Agent's run should be 404: %v", err)
	}
	inbox := uuid.Must(uuid.NewV7())
	rem, err := e.st.InsertInboxWithReminder(ctx, &store.NewInboxMessage{ID: inbox, From: store.ConsoleParty, To: store.Party{Kind: "agent", ID: e.agent}, Text: "platform message", WhenBusy: "queue"},
		store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "reminder", WhenBusy: "queue", InstanceID: e.inst, TriggerInboxID: &inbox})
	if err != nil || rem.Run == nil {
		t.Fatal("platform reminder", err)
	}
	if _, err = e.promote(rem.Run.ID); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("platform reminders should not be promotable: %v", err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdleOrDump(names{})
	_, err = e.promote(running.Run.ID)
	if ae, ok = apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued || *ae.Details.(store.RunNotQueuedDetails).Status != "completed" {
		t.Fatalf("an ended run should return run_not_queued(completed): %v", err)
	}
}

// Promotion racing with dequeue: every message runs exactly once, and both Redis queues end up empty.
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
		head := e.send2(n, "block opener", "")
		_ = head
		e.waitStarted("block opener")
		ids := []uuid.UUID{}
		for i := range 6 {
			m := e.send2(n, "normal"+string(rune('a'+round))+string(rune('0'+i)), "queue")
			ids = append(ids, m.Run.ID)
		}
		rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Go(func() {
				if _, err := e.promote(id); err != nil {
					if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeRunNotQueued {
						t.Errorf("promote failed: %v", err)
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
			if dispatched != 1 || status != "completed" && status != "interrupted" || text != "block opener" && counts[text] > 1 {
				t.Fatalf("round %d: %s dispatched %d times, status %s, executed %d times", round, text, dispatched, status, counts[text])
			}
		}
		if qi, qn := e.queues(n); len(qi) != 0 || len(qn) != 0 {
			t.Fatalf("round %d: Redis queues should be empty: %v / %v", round, qi, qn)
		}
	}
}

// When the run that causes the interrupt has already been dequeued and started, marking the interrupt must not land on
// that run itself; covers both paths, sending an interrupt and promoting to interrupt.
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
				t.Fatal("timed out waiting for the hook before marking the interrupt")
			}
			e.ex.signal(e.agent, "release") // A ends naturally; B is dequeued and starts
			e.waitStarted("block B")
			if r, err := e.st.GetRun(context.Background(), by); err != nil || r.Status == "queued" {
				t.Fatalf("B should have started: %+v %v", r, err)
			}
			close(release) // only now mark the interrupt: B is the only in-flight run
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			e.ex.signal(e.agent, "release")
			e.waitIdleOrDump(names{})
			r, err := e.st.GetRun(context.Background(), by)
			if err != nil || r.Status != "completed" {
				t.Fatalf("B was interrupted by the interrupt it caused: %+v %v", r, err)
			}
		})
	}
}

func (e *env) waitIdleOrDump(n names) {
	e.t.Helper()
	// One statement, so a run moving from queued to starting between two reads
	// (as Status does) cannot look idle. The run is recorded as finished before
	// the lock is released, so also wait for the lock, as waitIdle does.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var open int
		err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND status IN ('queued','starting','running','aborting')`, e.agent).Scan(&open)
		if err == nil && open == 0 {
			if owner, err := e.co.LockOwner(context.Background(), e.agent.String()); err == nil && owner == "" {
				return
			}
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
	qi, qn := e.queues(n)
	owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
	e.t.Fatalf("timed out waiting for the Agent to go idle: redis interrupt %v normal %v lock %q execution order %v", qi, qn, owner, e.ex.messages())
}
