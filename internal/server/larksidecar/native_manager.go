package larksidecar

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
)

// NativeManager is the App-owned manager shared by execution and task handoff.
// The older Manager API remains a compatibility surface over the same engine;
// NativeManager does not create a second registration or consumer registry.
type NativeManager struct{ core *Manager }

func NewNativeManager() *NativeManager { return &NativeManager{core: NewManager()} }

// NativeScope exposes only the PATH shim's independent execution capability.
// The immutable binding, launch configuration and native HMAC key stay private.
type NativeScope struct{ scope *ManagedScope }

func (m *NativeManager) OpenScope(ctx context.Context, binding Binding, source TokenSource, cfg LaunchConfig, limits Limits) (*NativeScope, error) {
	if m == nil || m.core == nil {
		return nil, ErrNativeClosed
	}
	scope, err := m.core.OpenScope(ctx, binding, source, cfg, limits)
	if err != nil {
		return nil, err
	}
	return &NativeScope{scope: scope}, nil
}

func (s *NativeScope) BinDir() string { return s.scope.BinDir() }

func (s *NativeScope) Environment() map[string]string { return s.scope.Environment() }

// Close revokes admission immediately, then proves both broker I/O and native
// Start/Wait consumers have drained. Failure retains the exact registration,
// preventing replacement until a subsequent Close proves completion. A caller
// cancelling its wait does not undo revocation or revive the old capability.
func (s *NativeScope) Close(ctx context.Context) error {
	if s == nil || s.scope == nil || ctx == nil {
		return ErrNativeClosed
	}
	if err := s.scope.Close(); err != nil {
		return err
	}
	return s.scope.Wait(ctx)
}

type NativeLease struct{ gate *RuntimeGate }

// Acquire atomically gates the exact two runtime IDs, including absent scopes.
// It neither stops a scope nor cancels an admitted consumer. Creation and
// command preparation use this same engine's mutex. Sorting gives callers a
// deterministic group order without mutating their input slice.
func (m *NativeManager) Acquire(ctx context.Context, ids []uuid.UUID) (*NativeLease, error) {
	if m == nil || m.core == nil {
		return nil, ErrNativeClosed
	}
	if len(ids) != 2 || ids[0] == uuid.Nil || ids[1] == uuid.Nil || ids[0] == ids[1] {
		return nil, errors.New("invalid runtime pair")
	}
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	gate, err := m.core.AcquireRuntimeGate(ctx, ordered)
	if err != nil {
		return nil, err
	}
	return &NativeLease{gate: gate}, nil
}

// Seal completes all fallible revocation, Close and Wait before transaction C,
// retaining the group gate on either success or failure.
func (l *NativeLease) Seal(ctx context.Context) error {
	if l == nil || l.gate == nil {
		return ErrNativeClosed
	}
	return l.gate.Seal(ctx)
}

// Release is idempotent and memory-only. An attempted but unconfirmed Seal
// cannot release the gate; after confirmed C callers release a sealed lease.
func (l *NativeLease) Release() {
	if l != nil && l.gate != nil {
		l.gate.Release()
	}
}
