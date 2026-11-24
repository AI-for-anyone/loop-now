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

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/conversation"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// fakeExec is a controllable executor double: when the message contains "block" it blocks until released or
// interrupted; when it contains "hold" it blocks and ignores interrupts, waiting only for release (used to keep an
// interrupt message sitting in the interrupt queue).
type fakeExec struct {
	mu       sync.Mutex
	order    []string
	running  map[uuid.UUID]chan string // agent -> control channel: "release" / "interrupt"
	runIDs   map[uuid.UUID]uuid.UUID   // agent -> ID of the current run
	started  chan string
	versions map[uuid.UUID]int64
	intr     chan struct{} // signalled after every call to Interrupt
	// sessions, when set, makes the fake behave like a runtime that saves its session: the turn is reported with the
	// Agent's runtime and model, and its session file is recorded with that runtime when the turn ends.
	sessions *store.Store
	// beforeSnapshot, when set, is called after the conversation service has dispatched a message and before the fake
	// reads the Agent's configuration, the way the local executor reads its execution snapshot.
	beforeSnapshot func(req executor.Request)
}

func newFakeExec() *fakeExec {
	return &fakeExec{running: map[uuid.UUID]chan string{}, runIDs: map[uuid.UUID]uuid.UUID{}, started: make(chan string, 100), intr: make(chan struct{}, 100)}
}

func (f *fakeExec) Execute(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
	compact := req.Origin == executor.OriginConsoleCompact
	prefix := executor.RunIDLine(req.RunID)
	// A Pi command from the conversation page goes to Pi verbatim (without the run ID): a name containing handled is
	// treated as an extension command handled directly, with no reply.
	if !compact && strings.HasPrefix(req.Message, "/") {
		f.mu.Lock()
		f.order = append(f.order, req.Message)
		f.mu.Unlock()
		f.started <- req.Message
		if strings.HasPrefix(req.Message, "/handled") {
			return executor.Result{Outcome: executor.OutcomeCompleted, ConfigVersion: 1, CommandHandled: true,
				CommandNotices: []executor.CommandNotice{{Level: "info", Message: "Fetching…"}, {Level: "warning", Message: "Weekly Limit\n   Used       1%"}}, CommandNoticesTruncated: true}, nil
		}
		return executor.Result{Outcome: executor.OutcomeCompleted, Text: "Reply: " + req.Message, ConfigVersion: 1}, nil
	}
	if !compact && !strings.HasPrefix(req.Message, prefix) {
		return executor.Result{}, fmt.Errorf("missing the correct run ID line for this turn")
	}
	if !compact {
		req.Message = strings.TrimPrefix(req.Message, prefix)
	}
	// The words that steer the fake are read from the message itself, not from a handoff block before it.
	body := req.Message
	if _, after, ok := strings.Cut(body, "</conversation-handoff>"); ok {
		body = after
	}
	// Like the local executor, the runtime is decided from the configuration read when the run starts, before the
	// run counts as started: a change made after that applies from the next message.
	if f.beforeSnapshot != nil {
		f.beforeSnapshot(req)
	}
	sel := executor.Selection{Runtime: "pi"}
	if f.sessions != nil {
		a, err := f.sessions.GetAgent(ctx, req.AgentID)
		if err != nil {
			return executor.Result{}, err
		}
		// A session whose file starts with /gone is lost, as the local executor finds it: marked missing, the run fails.
		if sess, err := f.sessions.GetAgentSession(ctx, req.AgentID); err == nil && sess.State == "active" && strings.HasPrefix(sess.SessionFile, "/gone") {
			if err := f.sessions.MarkSessionMissing(ctx, req.AgentID, &req.RunID); err != nil {
				return executor.Result{}, err
			}
			return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrSessionMissing, ErrorMessage: "The recorded session no longer exists.", ConfigVersion: 1}, nil
		}
		sel = executor.Selection{Runtime: agentruntime.ForProvider(a.ModelProvider).ID, Provider: a.ModelProvider, ModelID: a.ModelID}
		defer func() {
			_ = f.sessions.RecordRuntimeSessionFile(context.Background(), req.AgentID, sel.Runtime, "/fake/"+sel.Runtime+".jsonl")
		}()
	}
	ctl := make(chan string, 4)
	f.mu.Lock()
	if f.running[req.AgentID] != nil {
		f.mu.Unlock()
		return executor.Result{}, fmt.Errorf("two runs at once for the same Agent")
	}
	f.running[req.AgentID] = ctl
	f.runIDs[req.AgentID] = req.RunID
	f.order = append(f.order, req.Message)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); delete(f.running, req.AgentID); delete(f.runIDs, req.AgentID); f.mu.Unlock() }()
	f.started <- req.Message

	sink.Phase(executor.PhasePreparing, sel.Runtime)
	sink.Running(1, sel)
	sink.Tool("call-1", "bash", executor.ToolStarted)
	sink.TextDelta("partial")
	if strings.Contains(body, "block") {
		select {
		case c := <-ctl:
			if c == "interrupt" {
				return executor.Result{Outcome: executor.OutcomeInterrupted, Text: "partial", ConfigVersion: 1}, nil
			}
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	for held := strings.Contains(body, "hold"); held; {
		select {
		case c := <-ctl:
			held = c != "release"
		case <-ctx.Done():
			return executor.Result{}, ctx.Err()
		}
	}
	sink.Tool("call-1", "bash", executor.ToolSucceeded)
	sink.TextDelta(" reply")
	if compact && strings.Contains(req.Message, "nocounts") {
		return executor.Result{Outcome: executor.OutcomeCompleted, ConfigVersion: 1, Compaction: &executor.CompactionResult{NoCounts: true}}, nil
	}
	if compact {
		return executor.Result{Outcome: executor.OutcomeCompleted, ConfigVersion: 1, Compaction: &executor.CompactionResult{TokensBefore: 100, EstimatedTokensAfter: 40}}, nil
	}
	return executor.Result{Outcome: executor.OutcomeCompleted, Text: "Reply: " + req.Message, ConfigVersion: 1}, nil
}

func (f *fakeExec) signal(agentID uuid.UUID, c string) bool {
	f.mu.Lock()
	ctl := f.running[agentID]
	f.mu.Unlock()
	if ctl == nil {
		return false
	}
	// Once the run has returned but has not yet been removed from running, nobody reads ctl any more: give up after a
	// bounded wait, so one late interrupt cannot hang the whole test (scheduling is slow on the Windows runner, where
	// such late arrivals are more common). A run that is in block/hold takes it right away.
	select {
	case ctl <- c:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

// Interrupt has the same semantics as the local executor: it only interrupts the run whose ID matches.
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
	t     *testing.T
	st    *store.Store
	co    *coord.Coord
	ex    *fakeExec
	svc   *conversation.Service
	agent uuid.UUID
	inst  uuid.UUID
}

func newEnv(t *testing.T) *env {
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
	e := &env{t: t, st: st, co: co, ex: newFakeExec(), agent: a.ID, inst: uuid.Must(uuid.NewV7())}
	e.svc = e.newService()
	if err := e.svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Stop(context.Background()) })
	return e
}

func (e *env) newService() *conversation.Service {
	return conversation.New(e.st, e.co, e.ex, conversation.Options{
		InstanceID: e.inst, MaxMessage: 1 << 16, AbortPollInterval: 100 * time.Millisecond, ReconcileInterval: 200 * time.Millisecond,
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
		e.t.Fatalf("sending %q failed: %v", text, err)
	}
	return m
}

func (e *env) waitStarted(msg string) {
	e.t.Helper()
	select {
	case got := <-e.ex.started:
		if got != msg {
			e.t.Fatalf("want %q to start, got %q", msg, got)
		}
	case <-time.After(5 * time.Second):
		e.t.Fatalf("timed out waiting for %q to start", msg)
	}
}

// waitIdle waits until the Agent is idle: no in-flight or queued run in the database, and the busy lock released (a run
// is first recorded as ended and the lock released afterwards, so when only looking at the status, a send right after
// might not get the lock yet).
func (e *env) waitIdle() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, err := e.svc.Status(context.Background(), e.agent)
		if err == nil && !st.Busy && st.Queued.Interrupt == 0 && st.Queued.Normal == 0 {
			if owner, err := e.co.LockOwner(context.Background(), e.agent.String()); err == nil && owner == "" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatal("timed out waiting for the Agent to go idle")
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
	m := e.mustSend("Hello", "")
	if m.Run.Status != "starting" || m.Run.WhenBusy != nil {
		t.Fatalf("should start right away when idle, without recording a choice: %+v", m.Run)
	}
	e.waitStarted("Hello")
	e.waitIdle()
	h := e.history()
	if len(h) != 2 || h[0].Role != "user" || h[1].Role != "assistant" || h[1].Text != "Reply: Hello" || h[1].Run.Status != "completed" {
		t.Fatalf("wrong history: %+v", h)
	}
	if len(h[1].Run.Tools) != 2 || h[1].Run.Tools[0].State != "started" {
		t.Fatalf("tool states should be recorded: %+v", h[1].Run.Tools)
	}
}

func TestBusyWithoutChoice(t *testing.T) {
	e := newEnv(t)
	first := e.mustSend("block slow", "")
	e.waitStarted("block slow")
	_, err := e.send("second message", "")
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeAgentBusy {
		t.Fatalf("a busy send without a choice should return agent_busy: %v", err)
	}
	d := ae.Details.(apitypes.AgentBusyDetails)
	if d.CurrentRunID == nil || *d.CurrentRunID != first.Run.ID {
		t.Fatalf("the details should give the current run: %+v", d)
	}
	if n := len(e.history()); n != 1 {
		t.Fatalf("a rejected message should not be saved: %d", n)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}

func TestQueueAndInterruptOrdering(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block current", "")
	e.waitStarted("block current")
	e.mustSend("normal 1", "queue")
	e.mustSend("normal 2", "queue")
	i1 := e.mustSend("block interrupt 1", "interrupt")
	if *i1.Run.WhenBusy != "interrupt" || i1.Run.Status != "queued" {
		t.Fatalf("an interrupt message should queue and record the choice: %+v", i1.Run)
	}
	e.waitStarted("block interrupt 1")
	// Another interrupt while an interrupt is in progress: it goes after "interrupt 1" and before the normal
	// messages.
	e.mustSend("interrupt 2", "interrupt")
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	got := e.ex.messages()
	want := []string{"block current", "block interrupt 1", "interrupt 2", "normal 1", "normal 2"}
	if !slices.Equal(got, want) {
		t.Fatalf("wrong processing order: %v", got)
	}
	h := e.history()
	var interrupted *apitypes.Message
	for i := range h {
		if h[i].Role == "assistant" && h[i].Run.Status == "interrupted" {
			interrupted = &h[i]
		}
	}
	if interrupted == nil || *interrupted.Run.EndReason != "user_interrupt" || interrupted.Text != "partial" {
		t.Fatalf("the interrupted reply should be recorded as interrupted and keep its text: %+v", interrupted)
	}
}

// An interrupt only affects the turn that it was aimed at: after A is confirmed to be aborting and before the executor
// is called, A happens to end naturally and B has started; this late interrupt must not land on B. The timing is fixed
// with test hooks instead of hoping for luck over repeated runs.
func TestLateInterruptDoesNotHitNextRun(t *testing.T) {
	e := newEnv(t)
	var target atomic.Value // ID of run A
	reached := make(chan struct{}, 8)
	release := make(chan struct{})
	conversation.SetTestHookBeforeInterrupt(func(runID uuid.UUID) {
		if id, _ := target.Load().(uuid.UUID); id == runID {
			reached <- struct{}{}
			<-release // hold the interrupt aimed at A until B has started
		}
	})
	t.Cleanup(func() { conversation.SetTestHookBeforeInterrupt(nil) })

	a := e.mustSend("block A", "")
	target.Store(a.Run.ID)
	e.waitStarted("block A")
	e.mustSend("block B", "interrupt") // A becomes aborting, and the interrupt command aimed at A is published
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the interrupt aimed at A to reach the hook")
	}
	// The interrupt has not reached the executor yet, and A happens to end naturally; then B starts.
	e.ex.signal(e.agent, "release")
	e.waitStarted("block B")
	close(release) // let the late interrupt through
	select {
	case <-e.ex.intr:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the late interrupt to reach the executor")
	}
	time.Sleep(200 * time.Millisecond) // the abort-poll loop may deliver it once more; give it time
	e.ex.signal(e.agent, "release")    // B ends normally
	e.waitIdle()

	status := map[string]string{}
	for _, m := range e.history() {
		if m.Role == "user" {
			status[m.Text] = m.Run.Status
		}
	}
	if status["block A"] != "completed" || status["block B"] != "completed" {
		t.Fatalf("the late interrupt should not hit B: A=%s, B=%s", status["block A"], status["block B"])
	}
}

func TestConcurrentSendsExactlyOnce(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block opener", "")
	e.waitStarted("block opener")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if _, err := e.send(fmt.Sprintf("m%02d", i), "queue"); err != nil {
				t.Errorf("send failed: %v", err)
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
		t.Fatalf("every message should be processed exactly once in queue order:\ngot %v\nwant %v", got, wantOrder)
	}
}

func TestLockLoss(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block current", "")
	e.waitStarted("block current")
	e.mustSend("queued 1", "queue")
	e.mustSend("queued 2", "queue")
	// The busy lock is lost (simulated by releasing it directly; busy locks do not expire, but the driver must cope
	// with a missing lock).
	ctx := context.Background()
	owner, _ := e.co.LockOwner(ctx, e.agent.String())
	if _, err := e.co.Release(ctx, e.agent.String(), owner); err != nil {
		t.Fatal(err)
	}
	// With the lock lost the Agent is still judged busy (the database has in-flight and queued runs), and no second run
	// is started.
	if _, err := e.send("no choice", ""); !apperr.Is(err, apperr.CodeAgentBusy) {
		t.Fatalf("the Agent should still be busy after losing the lock: %v", err)
	}
	// The lock is restored from the database (on send or on reconciliation).
	deadline := time.Now().Add(30 * time.Second)
	for {
		owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
		if owner != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the lock was not restored from the database")
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
	if got := e.ex.messages(); !slices.Equal(got, []string{"block current", "queued 1", "queued 2"}) {
		t.Fatalf("queued messages should each be processed once, in their original order: %v", got)
	}
}

// When the service stops, this instance's in-flight runs are closed as interrupted by a server restart, and the reply
// text produced so far is written to the message (the reply text lives only in memory).
func TestStopEndsInFlightRunsWithTheirText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.mustSend("block current", "")
	e.waitStarted("block current")
	active, err := e.st.ActiveRun(ctx, e.agent)
	if err != nil || active == nil {
		t.Fatal("there should be an in-flight run", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for text, _ := e.co.RunText(ctx, active.ID.String()); text != "partial"; text, _ = e.co.RunText(ctx, active.ID.String()) {
		if time.Now().After(deadline) {
			t.Fatalf("the reply text did not arrive: %q", text)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := e.st.GetRun(ctx, active.ID)
	if r.Status != "interrupted" || r.EndReason != "server_restart" {
		t.Fatalf("on stop, an in-flight run should end as interrupted by a server restart: %+v", r)
	}
	var reply *apitypes.Message
	for _, m := range e.history() {
		if m.Role == "assistant" && m.Run.ID == active.ID {
			reply = &m
		}
	}
	if reply == nil || reply.Text != "partial" {
		t.Fatalf("the text produced so far should be written: %+v", reply)
	}
}

func TestRestartRecovery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	other := uuid.Must(uuid.NewV7())
	// Simulate leftovers from the previous process: one in-flight run and one queued message.
	_, run, err := e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "old one", Start: true, InstanceID: other})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.co.AppendText(ctx, e.agent.String(), run.ID.String(), "text produced so far", "run.text", []byte(`{}`))
	_, _ = e.co.Restore(ctx, e.agent.String(), coord.LockValue(run.ID.String(), other.String()))
	_, _, err = e.st.InsertMessageAndRun(ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: "queued one", WhenBusy: "queue", InstanceID: other})
	if err != nil {
		t.Fatal(err)
	}

	svc2 := e.newService()
	if err := svc2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer svc2.Stop(ctx)
	e.waitIdle()
	r, _ := e.st.GetRun(ctx, run.ID)
	if r.Status != "interrupted" || r.EndReason != "server_restart" {
		t.Fatalf("a leftover run should be marked as interrupted by a server restart: %+v", r)
	}
	var reply *apitypes.Message
	for _, m := range e.history() {
		if m.Role == "assistant" && m.Run.ID == run.ID {
			reply = &m
		}
	}
	if reply == nil || reply.Text != "text produced so far" {
		t.Fatalf("the text received so far should be kept: %+v", reply)
	}
	if !slices.Contains(e.ex.messages(), "queued one") {
		t.Fatal("the queued message should be processed as usual")
	}
}

// A lost session does not hold the conversation back: the next message is accepted under the same session number.
// A reset still starts a new session number.
func TestLostSessionAndReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.st.RecordSessionFile(ctx, e.agent, "/tmp/x.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.MarkSessionMissing(ctx, e.agent, nil); err != nil {
		t.Fatal(err)
	}
	if m := e.mustSend("Hello", ""); m.SessionEpoch != 1 {
		t.Fatalf("accepted under the same session number: %d", m.SessionEpoch)
	}
	e.waitStarted("Hello")
	e.waitIdle()
	sess, err := e.svc.ResetSession(ctx, e.agent)
	if err != nil || sess.State != "none" || sess.Epoch != 2 {
		t.Fatalf("after a reset it should be a new session with the epoch increased by one: %+v %v", sess, err)
	}
	m := e.mustSend("block new session", "")
	if m.SessionEpoch != 2 {
		t.Fatalf("the new message's session epoch should be 2: %d", m.SessionEpoch)
	}
	e.waitStarted("block new session")
	if _, err := e.svc.ResetSession(ctx, e.agent); !apperr.Is(err, apperr.CodeAgentNotIdle) {
		t.Fatalf("a reset should be rejected while a run is in flight: %v", err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}

func TestStatusAndHistoryPaging(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range 3 {
		msg := fmt.Sprintf("message %d", i)
		e.mustSend(msg, "")
		e.waitStarted(msg)
		e.waitIdle()
	}
	latest, err := e.svc.History(ctx, e.agent, store.MessageQuery{Limit: 2})
	page, more := latest.Messages, latest.HasMore
	if err != nil || len(page) != 2 || !more || latest.HasMoreAfter || page[1].Role != "assistant" {
		t.Fatalf("wrong latest page: %d %v %v", len(page), more, err)
	}
	olderPage, err := e.svc.History(ctx, e.agent, store.MessageQuery{Before: &page[0].ID, Limit: 10})
	older := olderPage.Messages
	if err != nil || len(older) != 4 || olderPage.HasMore || !olderPage.HasMoreAfter {
		t.Fatalf("wrong older page: %d %v", len(older), err)
	}
	st, err := e.svc.Status(ctx, e.agent)
	if err != nil || st.Busy || st.Session.State != "none" {
		t.Fatalf("wrong status: %+v %v", st, err)
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
	if err != nil || !snap.Status.Busy || snap.CurrentText == nil || *snap.CurrentText != "partial" || lastID == "" {
		t.Fatalf("wrong snapshot: %+v %s %v", snap, lastID, err)
	}
	e.ex.signal(e.agent, "release")
	e.waitIdle()
}

// newestEvents returns all events kept in the Agent's event stream, newest first.
func (e *env) newestEvents() []coord.Event {
	e.t.Helper()
	evs, err := e.co.ReadEvents(context.Background(), e.agent.String(), "0-0", 0, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	slices.Reverse(evs)
	return evs
}
