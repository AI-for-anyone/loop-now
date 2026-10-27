package larksidecar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRuntimeGatePreSealRollbackAndRepeatedStopIdle(t *testing.T) {
	// Real owned dual-listener broker, no native or provider calls. Exercise the
	// accepted Manager/ManagedScope/RuntimeGate production ABI directly.
	facade := NewNativeManager()
	s, calls := nativeABIScope(t, facade)
	m := facade.core
	ids := []uuid.UUID{uuid.MustParse(s.scope.id), uuid.New()}
	g, err := m.AcquireRuntimeGate(context.Background(), ids)
	if err != nil {
		t.Fatal("idle group acquisition")
	}
	g.Release()
	g.Release()
	if err := s.scope.begin(); err != nil {
		t.Fatal("pre-Seal rollback did not restore command admission")
	}
	s.scope.end()
	s.scope.broker.mu.Lock()
	closed := s.scope.broker.closed
	s.scope.broker.mu.Unlock()
	if closed || s.scope.closed {
		t.Fatal("pre-Seal rollback stopped the busy-capable scope")
	}
	g, err = m.AcquireRuntimeGate(context.Background(), ids)
	if err != nil {
		t.Fatal("pre-Seal rollback left runtime group gated")
	}
	if !errors.Is(s.scope.begin(), ErrNativeBusy) {
		t.Fatal("reacquired gate admitted command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := g.StopIdle(ctx); err != nil {
		t.Fatal("StopIdle did not close and drain scope")
	}
	if err := g.StopIdle(ctx); err != nil {
		t.Fatal("repeated StopIdle was not idempotent")
	}
	if err := s.scope.Close(); err != nil || s.scope.Wait(ctx) != nil {
		t.Fatal("scope Close/Wait did not remain idempotent")
	}
	if m.scopes[s.scope.id] != nil || calls.Load() != 0 {
		t.Fatal("sealed scope remained registered or resolved a credential")
	}
	g.Release()
	g.Release()
	other, err := m.AcquireRuntimeGate(ctx, ids)
	if err != nil {
		t.Fatal("sealed Release did not remove group gate")
	}
	if err := other.StopIdle(ctx); err != nil {
		t.Fatal("absent scopes did not seal")
	}
	other.Release()
}
