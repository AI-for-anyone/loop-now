package cliauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"log/slog"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("Google CLI授权不可用")

type SafeError struct{ Code string }

func (e *SafeError) Error() string { return "Google CLI认证失败" }

type LoginHelper interface {
	Login(context.Context, uuid.UUID, func(string) error) (clicredential.Google, error)
}
type Refresher interface {
	Refresh(context.Context, clicredential.Google) (clicredential.Google, error)
}
type RefreshStore interface {
	GetGoogleCLIGrant(context.Context, clicredential.Binding) (store.GoogleCLIGrant, error)
	ClaimGoogleCLIRefresh(context.Context, clicredential.Binding, int64, uuid.UUID, time.Time) (bool, error)
	CommitGoogleCLIRefresh(context.Context, clicredential.Binding, int64, uuid.UUID, []byte) error
	FailGoogleCLIRefresh(context.Context, clicredential.Binding, int64, uuid.UUID, string) error
}
type CoordinatorOptions struct {
	RefreshTimeout, RecordTimeout, WaitTimeout, PollInterval, RefreshSkew time.Duration
	Now                                                                   func() time.Time
}
type Coordinator struct {
	Store     RefreshStore
	Refresher Refresher
	Options   CoordinatorOptions
	Log       *slog.Logger
	mu        sync.Mutex
	closed    bool
	workers   sync.WaitGroup
}

func NewCoordinator(st RefreshStore, refresher Refresher, o CoordinatorOptions, log *slog.Logger) *Coordinator {
	if o.RefreshTimeout <= 0 {
		o.RefreshTimeout = 30 * time.Second
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
	return &Coordinator{Store: st, Refresher: refresher, Options: o, Log: log}
}

// ManagementRefreshStore adds a distinct operation-authorized principal without
// widening the runtime Binding or forcing legacy/fake stores to implement it.
type ManagementRefreshStore interface {
	GetGoogleCLIVerificationGrant(context.Context, clicredential.VerificationGrantBinding) (store.GoogleCLIGrant, error)
	ClaimGoogleCLIVerificationRefresh(context.Context, clicredential.VerificationGrantBinding, int64, uuid.UUID, time.Time) (bool, error)
	CommitGoogleCLIVerificationRefresh(context.Context, clicredential.VerificationGrantBinding, int64, uuid.UUID, []byte) error
	FailGoogleCLIVerificationRefresh(context.Context, clicredential.VerificationGrantBinding, int64, uuid.UUID, string) error
}
type refreshAccess struct {
	version uuid.UUID
	get     func(context.Context) (store.GoogleCLIGrant, error)
	claim   func(context.Context, int64, uuid.UUID, time.Time) (bool, error)
	commit  func(context.Context, int64, uuid.UUID, []byte) error
	fail    func(context.Context, int64, uuid.UUID, string) error
}

func (c *Coordinator) Resolve(ctx context.Context, b clicredential.Binding) (clicredential.Google, error) {
	if !b.Valid() || c.Store == nil {
		return clicredential.Google{}, ErrUnavailable
	}
	return c.resolve(ctx, refreshAccess{version: b.VersionID,
		get: func(ctx context.Context) (store.GoogleCLIGrant, error) { return c.Store.GetGoogleCLIGrant(ctx, b) },
		claim: func(ctx context.Context, g int64, l uuid.UUID, u time.Time) (bool, error) {
			return c.Store.ClaimGoogleCLIRefresh(ctx, b, g, l, u)
		},
		commit: func(ctx context.Context, g int64, l uuid.UUID, secret []byte) error {
			return c.Store.CommitGoogleCLIRefresh(ctx, b, g, l, secret)
		},
		fail: func(ctx context.Context, g int64, l uuid.UUID, code string) error {
			return c.Store.FailGoogleCLIRefresh(ctx, b, g, l, code)
		}})
}
func (c *Coordinator) ResolveVerification(ctx context.Context, b clicredential.VerificationGrantBinding) (clicredential.Google, error) {
	st, ok := c.Store.(ManagementRefreshStore)
	if !ok || !b.Valid() {
		return clicredential.Google{}, ErrUnavailable
	}
	return c.resolve(ctx, refreshAccess{version: b.VersionID,
		get: func(ctx context.Context) (store.GoogleCLIGrant, error) {
			return st.GetGoogleCLIVerificationGrant(ctx, b)
		},
		claim: func(ctx context.Context, g int64, l uuid.UUID, u time.Time) (bool, error) {
			return st.ClaimGoogleCLIVerificationRefresh(ctx, b, g, l, u)
		},
		commit: func(ctx context.Context, g int64, l uuid.UUID, secret []byte) error {
			return st.CommitGoogleCLIVerificationRefresh(ctx, b, g, l, secret)
		},
		fail: func(ctx context.Context, g int64, l uuid.UUID, code string) error {
			return st.FailGoogleCLIVerificationRefresh(ctx, b, g, l, code)
		}})
}
func (c *Coordinator) resolve(ctx context.Context, a refreshAccess) (clicredential.Google, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Options.WaitTimeout)
	defer cancel()
	for {
		if e := ctx.Err(); e != nil {
			return clicredential.Google{}, e
		}
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return clicredential.Google{}, ErrUnavailable
		}
		row, e := a.get(ctx)
		if e != nil {
			return clicredential.Google{}, ErrUnavailable
		}
		if row.RefreshState == "unavailable" {
			if row.ErrorCode != nil && *row.ErrorCode == "requires_login" {
				return clicredential.Google{}, &SafeError{Code: "requires_login"}
			}
			return clicredential.Google{}, ErrUnavailable
		}

		if row.RefreshState == "ready" {
			credential, e := clicredential.DecodeGoogle(row.Secret)
			if e != nil || credential.Account != row.AccountLabel {
				return clicredential.Google{}, ErrUnavailable
			}
			if credential.ExpiresAt > c.Options.Now().Add(c.Options.RefreshSkew).UnixMilli() {
				return credential.Clone(), nil
			}
			if c.Refresher == nil {
				return clicredential.Google{}, ErrUnavailable
			}
			lease := uuid.New()
			until := time.Now().Add(c.Options.RefreshTimeout + 2*c.Options.RecordTimeout + 5*time.Second)
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return clicredential.Google{}, ErrUnavailable
			}
			// Claim and worker registration cannot race Close's Wait.
			claimed, e := a.claim(ctx, row.Generation, lease, until)
			if e != nil {
				c.mu.Unlock()
				return clicredential.Google{}, ErrUnavailable
			}
			if claimed {
				c.workers.Add(1)
				go c.refresh(a, row.Generation, lease, credential.Clone())
			}
			c.mu.Unlock()
		} else if row.RefreshState == "refreshing" && row.LeaseUntil != nil && !row.LeaseUntil.After(time.Now()) {
			// An abandoned lease never causes reuse of the uncertain refresh token.
			_, e = a.claim(ctx, row.Generation, uuid.New(), time.Now().Add(time.Second))
			if e != nil {
				return clicredential.Google{}, ErrUnavailable
			}
		}
		timer := time.NewTimer(c.Options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clicredential.Google{}, ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Coordinator) refresh(a refreshAccess, generation int64, lease uuid.UUID, old clicredential.Google) {
	defer c.workers.Done()
	// Supplier rotation owns a bounded lifecycle independent of any waiter.
	ctx, stop := context.WithTimeout(context.Background(), c.Options.RefreshTimeout)
	next, e := c.Refresher.Refresh(ctx, old)
	stop()
	code := "refresh_uncertain"
	if e == nil && (!next.Valid() || next.Account != old.Account || next.ClientID != old.ClientID || next.ClientSecret != old.ClientSecret) {
		e = ErrUnavailable
		code = "invalid_response"
	}
	if e == nil {
		plain, err := next.Encode()
		if err != nil {
			e = err
		} else {
			record, cancel := context.WithTimeout(context.Background(), c.Options.RecordTimeout)
			e = a.commit(record, generation, lease, plain)
			cancel()
			clear(plain)
		}
	}
	if e != nil {
		var safe *SafeError
		if errors.As(e, &safe) && safe.Code == "reauth_required" {
			code = "requires_login"
		}
		record, cancel := context.WithTimeout(context.Background(), c.Options.RecordTimeout)
		err := a.fail(record, generation, lease, code)
		cancel()
		if err != nil && c.Log != nil {
			c.Log.Warn("Google CLI刷新状态回写失败", "stage", "refresh_failure_record", "versionId", a.version.String())
		}
	}
}
func (c *Coordinator) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
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
