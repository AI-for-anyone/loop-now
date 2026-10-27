package larksidecar

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

var ErrNativeBusy = errors.New("lark native busy")
var ErrNativeClosed = errors.New("lark native closed")

// Manager is the shared, server-held authority for ALL normal/task scope
// creation, command preparation and native lifetime. There is no public setter
// for captured paths, hashes, environment or credentials.
type Manager struct {
	epoch    uuid.UUID
	mu       sync.Mutex
	scopes   map[string]*ManagedScope
	creating map[string]bool
	active   map[string]int
	idle     map[string]chan struct{}
	gates    map[string]*RuntimeGate
	// Captured at construction by trusted Go composition, never from a request,
	// environment variable, scope descriptor or runtime setter.
	httpTransport http.RoundTripper
}

// ManagerOptions provides an explicit dependency for isolated Go integration
// tests. A nil HTTPTransport retains the fixed HTTPS, Proxy=nil production
// transport. Supplying one does not change the allowed host/path/identity rules,
// native Command, OS admission, or the scope/consumer registry.
type ManagerOptions struct {
	HTTPTransport http.RoundTripper
}

func NewManager() *Manager {
	return NewManagerWithOptions(ManagerOptions{})
}

func NewManagerWithOptions(options ManagerOptions) *Manager {
	return &Manager{epoch: uuid.New(), scopes: make(map[string]*ManagedScope), creating: make(map[string]bool), active: make(map[string]int), idle: make(map[string]chan struct{}), gates: make(map[string]*RuntimeGate), httpTransport: options.HTTPTransport}
}

type ManagedScope struct {
	epoch                        uuid.UUID
	manager                      *Manager
	broker                       *Broker
	id                           string
	cfg                          LaunchConfig
	execKey                      string
	cancel                       context.CancelFunc
	closed                       bool                          // manager.mu
	idle                         chan struct{}                 // manager.mu; specific scope generation
	active                       int                           // manager.mu
	accepted, started, confirmed uint64                        // manager.mu; original scope watermarks
	lastNative                   *NativeProcessIdentity        // manager.mu; last confirmed group
	identityUnknown              bool                          // manager.mu; capture cannot invent a missing birth identity
	processes                    map[int]NativeProcessIdentity // manager.mu; only unconfirmed native groups
}

// OpenScope's context bounds registration only. A published scope is owned by
// Close/Wait and survives the caller cancelling a completed registration. The
// creation reservation is visible to group freeze throughout preparation.
func (m *Manager) OpenScope(ctx context.Context, binding Binding, source TokenSource, cfg LaunchConfig, limits Limits) (*ManagedScope, error) {
	if m == nil {
		return nil, ErrNativeClosed
	}
	return m.openScope(ctx, binding, source, cfg, limits, m.httpTransport)
}

// Private fixture injection changes only the trusted upstream TLS dialer; it
// does not change native Command, sandbox or admission.
func (m *Manager) openScope(ctx context.Context, binding Binding, source TokenSource, cfg LaunchConfig, limits Limits, transport http.RoundTripper) (*ManagedScope, error) {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return nil, ErrNativeClosed
	}
	id, e := uuid.Parse(binding.RuntimeID)
	if e != nil || id == uuid.Nil || id.String() != binding.RuntimeID {
		return nil, errors.New("invalid lark runtime")
	}
	m.mu.Lock()
	if m.gates[binding.RuntimeID] != nil || m.creating[binding.RuntimeID] || m.scopes[binding.RuntimeID] != nil {
		m.mu.Unlock()
		return nil, ErrNativeBusy
	}
	m.creating[binding.RuntimeID] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.creating, binding.RuntimeID); m.mu.Unlock() }()
	if filepath.Base(cfg.LauncherBinary) != "lark-cli" || validateExecutable(cfg.LauncherBinary, cfg.LauncherSHA256) != nil {
		return nil, errors.New("lark launcher unavailable")
	}
	cfg.InputRoots = append([]string(nil), cfg.InputRoots...)
	if cfg.HostHome == "" {
		cfg.HostHome, e = os.UserHomeDir()
		if e != nil {
			return nil, errors.New("lark launcher unavailable")
		}
	}
	child, cancel := context.WithCancel(context.Background())
	forward := context.AfterFunc(ctx, cancel)
	published := false
	defer func() {
		forward()
		if !published {
			cancel()
		}
	}()
	b, e := start(child, binding, source, limits, transport)
	if e != nil {
		cancel()
		return nil, e
	}
	fail := func() {
		cancel()
		_ = b.Close()
		cleanup, done := context.WithTimeout(context.Background(), execCleanupTimeout)
		defer done()
		_ = b.Wait(cleanup)
	}
	// No native process is started: prove the exact profile and both owned
	// listeners before publishing an execution capability or PATH entry.
	if _, e = b.Command(child, cfg, "api", "GET", "/open-apis/authen/v1/user_info"); e != nil || child.Err() != nil {
		fail()
		return nil, errors.New("lark native admission unavailable")
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		fail()
		return nil, errors.New("lark execution unavailable")
	}
	s := &ManagedScope{epoch: uuid.New(), manager: m, broker: b, id: binding.RuntimeID, cfg: cfg, execKey: hex.EncodeToString(key), cancel: cancel}
	b.mu.Lock()
	b.execHandler = http.HandlerFunc(s.execute)
	b.mu.Unlock()
	m.mu.Lock()
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	detached := forward()
	if child.Err() != nil || ctx.Err() != nil || !detached || closed {
		m.mu.Unlock()
		fail()
		return nil, ErrNativeClosed
	}
	m.scopes[s.id] = s
	zero := make(chan struct{})
	close(zero)
	m.idle[s.id] = zero
	s.idle = zero
	published = true
	m.mu.Unlock()
	return s, nil
}

func (s *ManagedScope) BinDir() string         { return filepath.Dir(s.cfg.LauncherBinary) }
func (s *ManagedScope) ExecURL() string        { return s.broker.URL() + execPath }
func (s *ManagedScope) ExecCapability() string { return s.execKey }
func (s *ManagedScope) Environment() map[string]string {
	return map[string]string{ExecURLEnv: s.ExecURL(), ExecKeyEnv: s.ExecCapability()}
}

// begin is called before Command/admission/preparation, not only after Start.
func (s *ManagedScope) begin() error {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.closed || m.scopes[s.id] != s {
		return ErrNativeClosed
	}
	if m.gates[s.id] != nil {
		return ErrNativeBusy
	}
	if s.accepted == ^uint64(0) {
		return ErrNativeClosed
	}
	s.accepted++
	if m.active[s.id] == 0 {
		m.idle[s.id] = make(chan struct{})
		s.idle = m.idle[s.id]
	}
	m.active[s.id]++
	s.active++
	return nil
}

// end must run only after no child was started OR its actual Wait completed.
func (s *ManagedScope) end() {
	m := s.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active[s.id]--
	s.active--
	if m.active[s.id] == 0 {
		close(m.idle[s.id])
	}
}

// Close revokes admission and triggers child cancellation. Wait owns the drain
// proof; a timeout/unknown child completion cannot be converted into idle.
func (s *ManagedScope) Close() error {
	s.manager.mu.Lock()
	s.closed = true
	s.manager.mu.Unlock()
	s.cancel()
	return s.broker.Close()
}
func (s *ManagedScope) Wait(ctx context.Context) error {
	if e := s.broker.Wait(ctx); e != nil {
		return e
	}
	m := s.manager
	m.mu.Lock()
	closed, idle := s.closed, s.idle
	m.mu.Unlock()
	if !closed {
		return ErrNativeClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
	}
	m.mu.Lock()
	// A cancelled Close/Wait must not release the exact registry entry while
	// returning failure. Once removed, completion is proven and returns success.
	if e := ctx.Err(); e != nil {
		m.mu.Unlock()
		return e
	}
	if m.scopes[s.id] == s {
		delete(m.scopes, s.id)
	}
	m.mu.Unlock()
	return nil
}

type RuntimeGate struct {
	m               *Manager
	ids             []string
	scopes          []*ManagedScope
	unfreeze        []func()
	sealToken       chan struct{}
	attempted       bool                  // m.mu
	sealed          bool                  // m.mu
	released        bool                  // m.mu
	captured        *NativeRecoveryRecord // m.mu; immutable original capture
	recoveryAction  uuid.UUID             // m.mu
	recoveryUnknown bool                  // m.mu
}

func (m *Manager) AcquireRuntimeGate(ctx context.Context, ids []uuid.UUID) (*RuntimeGate, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNativeClosed
	}
	keys := make([]string, 0, len(ids))
	seen := make(map[uuid.UUID]bool)
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return nil, errors.New("invalid runtime group")
		}
		seen[id] = true
		keys = append(keys, id.String())
	}
	if len(keys) == 0 {
		return nil, errors.New("empty runtime group")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	for _, id := range keys {
		if m.creating[id] || m.active[id] != 0 || m.gates[id] != nil {
			return nil, ErrNativeBusy
		}
	}
	g := &RuntimeGate{m: m, ids: keys, sealToken: make(chan struct{}, 1)}
	for _, id := range keys {
		if s := m.scopes[id]; s != nil {
			release, e := s.broker.FreezeIdle()
			if e != nil {
				for _, undo := range g.unfreeze {
					undo()
				}
				return nil, ErrNativeBusy
			}
			g.scopes = append(g.scopes, s)
			g.unfreeze = append(g.unfreeze, release)
		}
	}
	for _, id := range keys {
		m.gates[id] = g
	}
	return g, nil
}

// Seal completes every fallible stop/revoke/drain before transaction C. Failure
// retains the entire gate; callers may retry Seal but cannot release uncertainty.
func (g *RuntimeGate) Seal(ctx context.Context) error {
	if ctx == nil {
		return ErrNativeClosed
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	select {
	case g.sealToken <- struct{}{}:
		defer func() { <-g.sealToken }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	g.m.mu.Lock()
	if g.recoveryUnknown {
		g.m.mu.Unlock()
		return ErrNativeRecoveryUnknown
	}
	if g.released {
		g.m.mu.Unlock()
		return ErrNativeClosed
	}
	if g.sealed {
		g.m.mu.Unlock()
		return nil
	}
	g.attempted = true
	g.m.mu.Unlock()
	for _, s := range g.scopes {
		if e := s.Close(); e != nil {
			return e
		}
		if e := s.Wait(ctx); e != nil {
			return e
		}
	}
	g.m.mu.Lock()
	g.sealed = true
	g.m.mu.Unlock()
	return nil
}
func (g *RuntimeGate) StopIdle(ctx context.Context) error { return g.Seal(ctx) }

// Release performs only idempotent memory operations. Unknown/failed Seal keeps
// the gate. Before attempting Seal an abandoned acquisition can be rolled back.
func (g *RuntimeGate) Release() {
	g.m.mu.Lock()
	defer g.m.mu.Unlock()
	if g.released || g.attempted && !g.sealed {
		return
	}
	for _, undo := range g.unfreeze {
		undo()
	}
	for _, id := range g.ids {
		if g.m.gates[id] == g {
			delete(g.m.gates, id)
		}
	}
	g.released = true
}
