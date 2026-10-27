package larksidecar

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This lifecycle fixture owns real dual loopback listeners, but deliberately
// does not launch native or claim sandbox/PG coverage. Those matrices are kept
// separately; this slice verifies the new ABI's join and registry semantics.
func nativeABIScope(t *testing.T, m *NativeManager) (*NativeScope, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	b := lifecycleBroker(t, TokenSourceFunc(func(context.Context) (string, error) {
		calls.Add(1)
		return "", errors.New("synthetic unavailable")
	}))
	zero := make(chan struct{})
	close(zero)
	s := &ManagedScope{manager: m.core, broker: b, id: uuid.NewString(), cancel: func() {}, idle: zero, execKey: "synthetic-exec-only"}
	m.core.scopes[s.id] = s
	m.core.idle[s.id] = zero
	return &NativeScope{scope: s}, &calls
}

func TestNativeManagerAcquireExactPair(t *testing.T) {
	m := NewNativeManager()
	lo := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	hi := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	for _, ids := range [][]uuid.UUID{nil, {lo}, {lo, lo}, {lo, uuid.Nil}, {lo, hi, uuid.New()}} {
		if l, err := m.Acquire(context.Background(), ids); err == nil || l != nil {
			t.Fatal("invalid runtime pair accepted")
		}
	}
	ids := []uuid.UUID{hi, lo}
	l, err := m.Acquire(context.Background(), ids)
	if err != nil {
		t.Fatal("absent runtime pair did not gate")
	}
	defer l.Release()
	if ids[0] != hi || l.gate.ids[0] != lo.String() || l.gate.ids[1] != hi.String() {
		t.Fatal("pair not sorted independently of caller slice")
	}
	for _, id := range ids {
		b := testBinding()
		b.RuntimeID = id.String()
		if scope, err := m.OpenScope(context.Background(), b, nil, LaunchConfig{}, Limits{}); !errors.Is(err, ErrNativeBusy) || scope != nil {
			t.Fatal("absent runtime creation escaped acquired pair")
		}
	}
	if err := l.Seal(context.Background()); err != nil {
		t.Fatal("empty pair did not seal")
	}
	l.Release()
	l.Release()
	// Preparation reservations and unknown native completion are both busy,
	// even without a current HTTP handler or a published scope.
	for _, preparation := range []bool{true, false} {
		if preparation {
			m.core.creating[lo.String()] = true
		} else {
			m.core.active[lo.String()] = 1
		}
		if l, err := m.Acquire(context.Background(), ids); !errors.Is(err, ErrNativeBusy) || l != nil {
			t.Fatal("preparation or unconfirmed native completion escaped gate")
		}
		delete(m.core.creating, lo.String())
		delete(m.core.active, lo.String())
	}
}

func TestNativeManagerCloseJoinsBeforeRegistryRelease(t *testing.T) {
	m := NewNativeManager()
	s, calls := nativeABIScope(t, m)
	id := uuid.MustParse(s.scope.id)
	env := s.Environment()
	if len(env) != 2 || env[ExecURLEnv] == "" || env[ExecKeyEnv] == "" || env[ExecKeyEnv] == s.scope.broker.Capability() {
		t.Fatal("scope leaked native authority or omitted exec capability")
	}
	delete(env, ExecKeyEnv)
	if s.Environment()[ExecKeyEnv] == "" {
		t.Fatal("caller mutated captured environment")
	}
	if err := s.scope.begin(); err != nil {
		t.Fatal("owned preparation admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := s.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Close reported success before consumer completion")
	}
	if m.core.scopes[s.scope.id] != s.scope {
		t.Fatal("failed Close discarded exact scope registration")
	}
	b := testBinding()
	b.RuntimeID = s.scope.id
	if other, err := m.OpenScope(context.Background(), b, nil, LaunchConfig{}, Limits{}); !errors.Is(err, ErrNativeBusy) || other != nil {
		t.Fatal("failed Close permitted replacement registration")
	}
	if l, err := m.Acquire(context.Background(), []uuid.UUID{id, uuid.New()}); !errors.Is(err, ErrNativeBusy) || l != nil {
		t.Fatal("unconfirmed consumer mistaken for idle")
	}
	s.scope.end() // Release the owned preparation-completion barrier.
	cleanup, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if s.Close(cleanup) != nil || s.Close(cleanup) != nil {
		t.Fatal("completed Close did not join idempotently")
	}
	if m.core.scopes[s.scope.id] != nil || !errors.Is(s.scope.begin(), ErrNativeClosed) || calls.Load() != 0 {
		t.Fatal("closed scope revived or source was resolved")
	}
}

func TestNativeManagerAcquireOnlyGatesSealCloses(t *testing.T) {
	m := NewNativeManager()
	s, calls := nativeABIScope(t, m)
	ids := []uuid.UUID{uuid.MustParse(s.scope.id), uuid.New()}
	l, err := m.Acquire(context.Background(), ids)
	if err != nil {
		t.Fatal("idle pair acquisition")
	}
	defer l.Release()
	s.scope.broker.mu.Lock()
	closed := s.scope.broker.closed
	s.scope.broker.mu.Unlock()
	if closed || s.scope.closed || !errors.Is(s.scope.begin(), ErrNativeBusy) {
		t.Fatal("Acquire stopped scope or did not gate admission")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Seal(ctx); err != nil {
		t.Fatal("Seal did not close and drain owned scope")
	}
	if m.core.scopes[s.scope.id] != nil || !s.scope.closed || calls.Load() != 0 {
		t.Fatal("Seal did not complete local cleanup before ready")
	}
	if other, err := m.Acquire(ctx, ids); !errors.Is(err, ErrNativeBusy) || other != nil {
		t.Fatal("Seal released group before confirmed C")
	}
	l.Release()
	l.Release()
	other, err := m.Acquire(ctx, ids)
	if err != nil {
		t.Fatal("memory-only release did not reopen absent pair")
	}
	if err := other.Seal(ctx); err != nil {
		t.Fatal("empty reacquired pair did not seal")
	}
	other.Release()
}
