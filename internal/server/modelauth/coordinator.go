package modelauth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

var ErrCredentialUnavailable = errors.New("绑定的模型账号凭证不可用")

type Refresher interface {
	Refresh(context.Context, modelcredential.Credential) (modelcredential.Credential, error)
}

// RefreshStore permits failure injection at the PG acknowledgement boundary.
// Production uses Store, including its durable conditional recovery update.
type RefreshStore interface {
	GetModelAuthVersion(context.Context, uuid.UUID) (store.ModelAuthVersion, error)
	ClaimModelAuthRefresh(context.Context, uuid.UUID, int64, uuid.UUID) (bool, error)
	CommitModelAuthRefresh(context.Context, uuid.UUID, uuid.UUID, []byte) (bool, error)
	FailModelAuthRefresh(context.Context, uuid.UUID, uuid.UUID, string) error
	RecoverStaleModelAuthRefresh(context.Context, time.Time) (int64, error)
}
type CoordinatorOptions struct {
	RecoveryInterval, RefreshDrainTimeout                                 time.Duration
	RefreshTimeout, RecordTimeout, WaitTimeout, PollInterval, RefreshSkew time.Duration
	Now                                                                   func() time.Time
}
type Coordinator struct {
	Store          RefreshStore
	Refresher      Refresher
	Options        CoordinatorOptions
	Log            *slog.Logger
	mu             sync.Mutex
	closed         bool
	workers        sync.WaitGroup
	recoveryCancel context.CancelFunc
}

func NewCoordinator(st RefreshStore, refresher Refresher, o CoordinatorOptions, log *slog.Logger) *Coordinator {
	if o.RefreshTimeout <= 0 {
		o.RefreshTimeout = 15 * time.Second
	}
	if o.RecordTimeout <= 0 {
		o.RecordTimeout = 5 * time.Second
	}
	if o.WaitTimeout <= 0 {
		o.WaitTimeout = 30 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 25 * time.Millisecond
	}
	if o.RefreshSkew <= 0 {
		o.RefreshSkew = 5 * time.Minute
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RecoveryInterval <= 0 {
		o.RecoveryInterval = time.Second
	}
	if o.RefreshDrainTimeout <= 0 {
		o.RefreshDrainTimeout = 7 * time.Second
	}
	c := &Coordinator{Store: st, Refresher: refresher, Options: o, Log: log}
	recoveryCtx, cancel := context.WithCancel(context.Background())
	c.recoveryCancel = cancel
	c.workers.Add(1)
	go c.recoverAbandoned(recoveryCtx)
	return c
}
func (c *Coordinator) Resolve(ctx context.Context, b modelcredential.Binding) (modelcredential.Credential, error) {
	if !b.Valid() {
		return modelcredential.Credential{}, ErrCredentialUnavailable
	}
	ctx, stop := context.WithTimeout(ctx, c.Options.WaitTimeout)
	defer stop()
	for {
		if ctx.Err() != nil {
			return modelcredential.Credential{}, ctx.Err()
		}
		v, e := c.Store.GetModelAuthVersion(ctx, b.VersionID)
		if e != nil {
			return modelcredential.Credential{}, ErrCredentialUnavailable
		}
		authorized := false
		if owner, ok := c.Store.(interface {
			ValidateModelAuthBinding(context.Context, modelcredential.Binding) bool
		}); ok {
			authorized = owner.ValidateModelAuthBinding(ctx, b)
		} else {
			authorized = b.OwnerKind != "project" && v.ProjectAccountID == nil && v.AgentID == b.AgentID
		}
		if !authorized || v.Provider != b.Provider || v.RefreshState == "unavailable" {
			return modelcredential.Credential{}, ErrCredentialUnavailable
		}
		if v.RefreshState == "ready" {
			value, e := modelcredential.Decode(v.Secret)
			if e != nil || value.Kind() != v.AuthMethod {
				return modelcredential.Credential{}, ErrCredentialUnavailable
			}
			if value.Kind() == "api_key" || value.ExpiresAt().After(c.Options.Now().Add(c.Options.RefreshSkew)) {
				return value, nil
			}
			if c.Refresher == nil {
				return modelcredential.Credential{}, ErrCredentialUnavailable
			}
			attempt := uuid.New()
			owned, e := c.Store.ClaimModelAuthRefresh(ctx, v.ID, v.Generation, attempt)
			if e != nil {
				return modelcredential.Credential{}, ErrCredentialUnavailable
			}
			if owned {
				c.mu.Lock()
				if c.closed {
					c.mu.Unlock()
					c.fail(v, attempt, "refresh_uncertain")
					return modelcredential.Credential{}, ErrCredentialUnavailable
				}
				c.workers.Add(1)
				c.mu.Unlock()
				go c.refresh(v, attempt, value)
			}
		}
		timer := time.NewTimer(c.Options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return modelcredential.Credential{}, ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Coordinator) refresh(v store.ModelAuthVersion, attempt uuid.UUID, old modelcredential.Credential) {
	defer c.workers.Done()
	// A rotating grant is not a cancellable read. Never inherit the waiter context.
	refreshCtx, cancel := context.WithTimeout(context.Background(), c.Options.RefreshTimeout)
	next, e := c.Refresher.Refresh(refreshCtx, old)
	cancel()
	if e != nil || next.Kind() != "oauth" || !old.SameAccount(next) {
		c.fail(v, attempt, "refresh_uncertain")
		return
	}
	ctx, stop := context.WithTimeout(context.Background(), c.Options.RecordTimeout)
	defer stop()
	plain, e := next.Encode()
	if e != nil {
		c.fail(v, attempt, "refresh_uncertain")
		return
	}
	defer clear(plain)
	committed, e := c.Store.CommitModelAuthRefresh(ctx, v.ID, attempt, plain)
	if e != nil || !committed {
		c.fail(v, attempt, "refresh_write_failed")
	}
}
func (c *Coordinator) fail(v store.ModelAuthVersion, attempt uuid.UUID, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Options.RecordTimeout)
	defer cancel()
	e := c.Store.FailModelAuthRefresh(ctx, v.ID, attempt, code)
	if c.Log != nil {
		c.Log.Warn("模型授权刷新不可用", "agentId", v.AgentID, "versionId", v.ID, "provider", v.Provider, "attemptId", attempt, "code", code, "recorded", e == nil)
	}
}
func (c *Coordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.recoveryCancel()
	c.mu.Unlock()
	done := make(chan struct{})
	go func() { c.workers.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Coordinator) recoverAbandoned(ctx context.Context) {
	defer c.workers.Done()
	ticker := time.NewTicker(c.Options.RecoveryInterval)
	defer ticker.Stop()
	// Include the provider budget, helper abort write/drain and two separate
	// persistence contexts. Credential-expiry test clocks never govern leases.
	horizon := c.Options.RefreshTimeout + c.Options.RefreshDrainTimeout + 2*c.Options.RecordTimeout + 2*time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			record, stop := context.WithTimeout(ctx, c.Options.RecordTimeout)
			count, err := c.Store.RecoverStaleModelAuthRefresh(record, time.Now().Add(-horizon))
			stop()
			if c.Log != nil && ctx.Err() == nil && (err != nil || count > 0) {
				c.Log.Warn("模型授权刷新恢复检查", "recorded", err == nil, "unavailableCount", count)
			}
		}
	}
}
