package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

func handoffWait(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !predicate() {
		select {
		case <-deadline.C:
			t.Fatal("handoff barrier not reached")
		case <-tick.C:
		}
	}
}

func handoffLease(t *testing.T, e *Executor, ids ...uuid.UUID) *RuntimeHandoff {
	t.Helper()
	h, err := e.AcquireRuntimeHandoff(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func handoffHandle(t *testing.T, h *RuntimeHandoff, id uuid.UUID) RuntimeHandle {
	t.Helper()
	v, err := h.Handle(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func handoffResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("handoff operation did not return")
		return nil
	}
}

func TestRuntimeHandoffPreservesGroupAndRejectsOldGeneration(t *testing.T) {
	h := newHarness(t, nil, "")
	taskID := uuid.New()
	member := func(s *agentconfig.Snapshot) { s.TaskID, s.ConfigurationAgentID = taskID, uuid.New() }
	a, b, other := h.agent(member), h.agent(member), h.agent(nil)
	h.exec(a, "first")
	h.exec(b, "followup")
	h.exec(other, "unaffected")
	oldPID, otherPID := h.pid(a), h.pid(other)
	h.e.mu.Lock()
	oldProc := h.e.procs[a]
	h.e.mu.Unlock()
	paths := []string{}
	want := map[string]string{}
	for _, id := range []uuid.UUID{a, b} {
		d := h.e.dirs(id)
		for _, p := range []string{filepath.Join(d.home, "keep"), filepath.Join(d.workspace, "deployed-artifact")} {
			if err := os.WriteFile(p, []byte("original:"+p), 0o600); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		h.src.mu.Lock()
		paths = append(paths, h.src.recorded[id])
		h.src.mu.Unlock()
	}
	for _, p := range paths {
		v, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want[p] = string(v)
	}
	lease := handoffLease(t, h.e, b, a)
	ordered := []uuid.UUID{a, b}
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	if !slices.Equal(lease.ids, ordered) {
		t.Fatal("group not UUID ordered")
	}
	oldHandle := handoffHandle(t, lease, a)
	for _, id := range []uuid.UUID{a, b} {
		if err := lease.StopIdle(context.Background(), id, handoffHandle(t, lease, id)); err != nil {
			t.Fatal(err)
		}
		if h.pid(id) != 0 {
			t.Fatal("old process still published")
		}
	}
	if err := syscall.Kill(oldPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("old Pi not reaped: %v", err)
	}
	if err := syscall.Kill(-oldPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("old Pi process group survived: %v", err)
	}
	for p, v := range want {
		got, err := os.ReadFile(p)
		if err != nil || string(got) != v {
			t.Fatal("HOME/session/workspace changed during handoff")
		}
	}
	if h.pid(other) != otherPID {
		t.Fatal("unrelated runtime changed")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	h.exec(a, "continue")
	h.src.mu.Lock()
	continuedSession := h.src.recorded[a]
	h.src.mu.Unlock()
	if continuedSession != oldProc.sessionFile {
		t.Fatal("handoff reforked/replaced original session")
	}
	newPID := h.pid(a)
	if newPID == 0 || newPID == oldPID {
		t.Fatal("expected a new process generation")
	}
	next := handoffLease(t, h.e, a)
	if err := next.StopIdle(context.Background(), a, oldHandle); !errors.Is(err, ErrRuntimeHandleMismatch) {
		t.Fatal("old handle accepted", err)
	}
	// Re-deliver actual old lifecycle callbacks after the replacement is alive.
	h.e.idleExpired(oldProc)
	h.e.watch(oldProc)
	h.e.discard(oldProc, nil)
	// A watcher may already have captured removed=true before the handoff.
	// Even that delayed unlink must revalidate the persisted process epoch.
	h.e.removeProcPidFile(oldProc)
	if h.pid(a) != newPID || syscall.Kill(newPID, 0) != nil {
		t.Fatal("late old callback touched replacement")
	}
	if _, err := os.Stat(h.e.dirs(a).pidFile); err != nil {
		t.Fatal("late callback removed new pid record")
	}
}

func TestRuntimeHandoffCompetingGroupsHaveSingleOwner(t *testing.T) {
	h := newHarness(t, nil, "")
	a, b := h.agent(nil), h.agent(nil)
	for range 20 {
		start := make(chan struct{})
		type result struct {
			lease *RuntimeHandoff
			err   error
		}
		results := make(chan result, 2)
		for _, group := range [][]uuid.UUID{{b, a}, {a, b}} {
			go func() {
				<-start
				lease, err := h.e.AcquireRuntimeHandoff(context.Background(), group)
				results <- result{lease, err}
			}()
		}
		close(start)
		var winner *RuntimeHandoff
		for range 2 {
			select {
			case r := <-results:
				if r.err == nil {
					if winner != nil {
						t.Fatal("both competing groups acquired ownership")
					}
					winner = r.lease
				} else if !errors.Is(r.err, ErrRuntimeBusy) {
					t.Fatal(r.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("opposite group order deadlocked")
			}
		}
		if winner == nil {
			t.Fatal("no group acquired ownership")
		}
		if err := winner.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type handoffSource struct {
	Source
	read chan int64
}

func (s *handoffSource) ExecutionSnapshot(ctx context.Context, id uuid.UUID) (agentconfig.Snapshot, error) {
	v, err := s.Source.ExecutionSnapshot(ctx, id)
	s.read <- v.ConfigVersion
	return v, err
}

func TestRuntimeHandoffNoProcessBlocksSnapshotAndCancellation(t *testing.T) {
	h := newHarness(t, nil, "")
	a, b := h.agent(nil), h.agent(nil)
	source := &handoffSource{Source: h.src, read: make(chan int64, 4)}
	h.e.src = source
	lease := handoffLease(t, h.e, b, a)
	if err := h.e.Release(context.Background(), a); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("Release bypassed handoff", err)
	}
	if err := h.e.FreezeForFork(context.Background(), b); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("Freeze bypassed handoff", err)
	}
	if err := lease.StopIdle(context.Background(), a, RuntimeHandle{}); !errors.Is(err, ErrRuntimeHandleMismatch) {
		t.Fatal("zero handle accepted for absent process", err)
	}
	if err := lease.StopIdle(context.Background(), a, handoffHandle(t, lease, b)); !errors.Is(err, ErrRuntimeHandleMismatch) {
		t.Fatal("other runtime handle accepted", err)
	}
	for _, id := range []uuid.UUID{a, b} {
		if err := lease.StopIdle(context.Background(), id, handoffHandle(t, lease, id)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(h.e.dirs(id).root); !os.IsNotExist(err) {
			t.Fatal("absent runtime was prepared")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.e.Execute(ctx, executor.Request{AgentID: a, RunID: uuid.New()}, newSink())
		done <- err
	}()
	handoffWait(t, func() bool { h.e.mu.Lock(); defer h.e.mu.Unlock(); return h.e.pending[a] == 1 })
	select {
	case <-source.read:
		t.Fatal("snapshot read before gate released")
	default:
	}
	cancel() // active cancellation, after admission-wait barrier
	if err := handoffResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := h.e.AcquireRuntimeHandoff(context.Background(), []uuid.UUID{a}); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal("overlapping lease accepted", err)
	}
	go func() {
		_, err := h.e.Execute(context.Background(), executor.Request{AgentID: a, RunID: uuid.New(), Message: "new"}, newSink())
		done <- err
	}()
	handoffWait(t, func() bool { h.e.mu.Lock(); defer h.e.mu.Unlock(); return h.e.pending[a] == 1 })
	h.src.update(a, func(s *agentconfig.Snapshot) { s.CredentialIdentity = "new-binding" })
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handoffResult(t, done); err != nil {
		t.Fatal(err)
	}
	if version := <-source.read; version != 2 {
		t.Fatal("stale pre-handoff snapshot used", version)
	}
}

func TestRuntimeHandoffBusyAndQueuedRejectWholeGroup(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 1 }, "")
	idle, busy, queued := h.agent(nil), h.agent(nil), h.agent(nil)
	sink := newSink()
	busyDone := make(chan struct{})
	go func() {
		defer close(busyDone)
		_, _ = h.e.Execute(context.Background(), executor.Request{AgentID: busy, RunID: uuid.New(), Message: "!sleep=20s !text=ready_more"}, sink)
	}()
	select {
	case <-sink.gotText:
	case <-time.After(10 * time.Second):
		t.Fatal("fakePi busy barrier missing")
	}
	pid := h.pid(busy)
	queuedSink := newSink()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queueDone := make(chan struct{})
	go func() {
		defer close(queueDone)
		_, _ = h.e.Execute(ctx, executor.Request{AgentID: queued, RunID: uuid.New()}, queuedSink)
	}()
	handoffWait(t, func() bool {
		queuedSink.mu.Lock()
		defer queuedSink.mu.Unlock()
		return slices.Contains(queuedSink.phases, executor.PhaseWaitingSlot)
	})
	for _, group := range [][]uuid.UUID{{idle, busy}, {idle, queued}} {
		if _, err := h.e.AcquireRuntimeHandoff(context.Background(), group); !errors.Is(err, ErrRuntimeBusy) {
			t.Fatal("busy/queued group accepted", err)
		}
		h.e.mu.Lock()
		count := len(h.e.handoffs)
		h.e.mu.Unlock()
		if count != 0 || h.pid(busy) != pid || syscall.Kill(pid, 0) != nil {
			t.Fatal("partial gate or busy process stopped")
		}
	}
	cancel()
	<-queueDone
	_ = h.e.Shutdown(context.Background())
	<-busyDone
}

type handoffTokens struct {
	*fakeTokens
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
	fail    bool
}

func (f *handoffTokens) RevokeAgentToken(ctx context.Context, id uuid.UUID) error {
	f.once.Do(func() { close(f.entered); <-f.resume })
	if f.fail {
		return errors.New("TEST-secret-must-not-escape")
	}
	return f.fakeTokens.RevokeAgentToken(ctx, id)
}

func TestRuntimeHandoffStopCancellationRetainsGateUntilConfirmed(t *testing.T) {
	tokens := &handoffTokens{fakeTokens: &fakeTokens{issued: map[uuid.UUID]string{}, revoked: map[uuid.UUID]bool{}}, entered: make(chan struct{}), resume: make(chan struct{})}
	h := newHarness(t, func(o *Options) { o.Tokens = tokens }, "")
	a := h.agent(nil)
	h.exec(a, "session")
	lease := handoffLease(t, h.e, a)
	handle := handoffHandle(t, lease, a)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lease.StopIdle(ctx, a, handle) }()
	select {
	case <-tokens.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stop revocation barrier missing")
	}
	cancel()
	if err := handoffResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := lease.Close(); !errors.Is(err, ErrRuntimeStopUnconfirmed) {
		t.Fatal("unconfirmed stop released gate", err)
	}
	// Same known in-flight stop, independently bounded by a deadline as well.
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer deadlineCancel()
	if err := lease.StopIdle(deadlineCtx, a, handle); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline did not bound an in-flight stop", err)
	}
	if _, err := h.e.AcquireRuntimeHandoff(context.Background(), []uuid.UUID{a}); !errors.Is(err, ErrRuntimeBusy) {
		t.Fatal(err)
	}
	close(tokens.resume)
	if err := lease.StopIdle(context.Background(), a, handle); err != nil {
		t.Fatal(err)
	}
	if len(tokens.live()) != 0 {
		t.Fatal("old process token survived confirmed handoff")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal("Close not idempotent", err)
	}
}

func TestRuntimeHandoffShutdownUnblocksAdmission(t *testing.T) {
	h := newHarness(t, nil, "")
	a := h.agent(nil)
	lease := handoffLease(t, h.e, a)
	done := make(chan error, 1)
	go func() {
		_, err := h.e.Execute(context.Background(), executor.Request{AgentID: a, RunID: uuid.New()}, newSink())
		done <- err
	}()
	handoffWait(t, func() bool { h.e.mu.Lock(); defer h.e.mu.Unlock(); return h.e.pending[a] == 1 })
	if err := h.e.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handoffResult(t, done); !errors.Is(err, ErrRuntimeHandoffClosed) {
		t.Fatal("shutdown left admission waiting", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeHandoffMissingSessionAndRevocationFailure(t *testing.T) {
	t.Run("missing-session", func(t *testing.T) {
		h := newHarness(t, nil, "")
		a := h.agent(nil)
		h.exec(a, "session")
		pid := h.pid(a)
		h.src.mu.Lock()
		session := h.src.recorded[a]
		h.src.mu.Unlock()
		if err := os.Remove(session); err != nil {
			t.Fatal(err)
		}
		lease := handoffLease(t, h.e, a)
		if err := lease.StopIdle(context.Background(), a, handoffHandle(t, lease, a)); !errors.Is(err, ErrRuntimeSessionMissing) {
			t.Fatal(err)
		}
		if h.pid(a) != pid || syscall.Kill(pid, 0) != nil {
			t.Fatal("missing session should fail before stop")
		}
		if _, err := os.Stat(session); !os.IsNotExist(err) {
			t.Fatal("missing session recreated")
		}
	})
	t.Run("revoke-failure", func(t *testing.T) {
		tokens := &handoffTokens{fakeTokens: &fakeTokens{issued: map[uuid.UUID]string{}, revoked: map[uuid.UUID]bool{}}, entered: make(chan struct{}), resume: make(chan struct{}), fail: true}
		close(tokens.resume)
		h := newHarness(t, func(o *Options) { o.Tokens = tokens }, "")
		a := h.agent(nil)
		h.exec(a, "session")
		lease := handoffLease(t, h.e, a)
		if err := lease.StopIdle(context.Background(), a, handoffHandle(t, lease, a)); err != ErrRuntimeStopUnconfirmed {
			t.Fatal("must return only safe error", err)
		}
		if err := lease.Close(); !errors.Is(err, ErrRuntimeStopUnconfirmed) {
			t.Fatal("unsafe failure released gate", err)
		}
	})
}
