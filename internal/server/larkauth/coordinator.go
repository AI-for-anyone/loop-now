package larkauth

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

var ErrUnavailable = errors.New("Lark CLI授权不可用")

type SafeError struct {
	Code       string
	HTTPStatus int
}

func (e *SafeError) Error() string { return "Lark CLI认证失败" }

type Refresher interface {
	Refresh(context.Context, clicredential.Lark) (clicredential.Lark, error)
}
type RefreshStore interface {
	GetLarkCLIGrant(context.Context, clicredential.LarkBinding) (store.LarkCLIGrant, error)
	ClaimLarkCLIRefresh(context.Context, clicredential.LarkBinding, int64, uuid.UUID, time.Time) (bool, error)
	CommitLarkCLIRefresh(context.Context, clicredential.LarkBinding, int64, uuid.UUID, []byte) error
	FailLarkCLIRefresh(context.Context, clicredential.LarkBinding, int64, uuid.UUID, string) error
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

type refreshAccess struct {
	version uuid.UUID
	binding clicredential.LarkBinding
	get     func(context.Context) (store.LarkCLIGrant, error)
	claim   func(context.Context, int64, uuid.UUID, time.Time) (bool, error)
	commit  func(context.Context, int64, uuid.UUID, []byte) error
	fail    func(context.Context, int64, uuid.UUID, string) error
}

func (c *Coordinator) Resolve(ctx context.Context, b clicredential.LarkBinding) (clicredential.Lark, error) {
	if !b.Valid() || c.Store == nil {
		return clicredential.Lark{}, ErrUnavailable
	}
	return c.resolve(ctx, refreshAccess{version: b.VersionID, binding: b,
		get: func(ctx context.Context) (store.LarkCLIGrant, error) { return c.Store.GetLarkCLIGrant(ctx, b) },
		claim: func(ctx context.Context, g int64, l uuid.UUID, u time.Time) (bool, error) {
			return c.Store.ClaimLarkCLIRefresh(ctx, b, g, l, u)
		},
		commit: func(ctx context.Context, g int64, l uuid.UUID, secret []byte) error {
			return c.Store.CommitLarkCLIRefresh(ctx, b, g, l, secret)
		},
		fail: func(ctx context.Context, g int64, l uuid.UUID, code string) error {
			return c.Store.FailLarkCLIRefresh(ctx, b, g, l, code)
		}})
}
func (c *Coordinator) resolve(ctx context.Context, a refreshAccess) (clicredential.Lark, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Options.WaitTimeout)
	defer cancel()
	for {
		if e := ctx.Err(); e != nil {
			return clicredential.Lark{}, e
		}
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return clicredential.Lark{}, ErrUnavailable
		}
		row, e := a.get(ctx)
		if e != nil {
			return clicredential.Lark{}, ErrUnavailable
		}
		if row.RefreshState == "unavailable" {
			if row.ErrorCode != nil && *row.ErrorCode == "requires_login" {
				return clicredential.Lark{}, &SafeError{Code: "requires_login"}
			}
			return clicredential.Lark{}, ErrUnavailable
		}

		if row.RefreshState == "ready" {
			credential, e := clicredential.DecodeLark(row.Secret)
			if e != nil || !credential.Matches(a.binding) {
				return clicredential.Lark{}, ErrUnavailable
			}
			if credential.ExpiresAt > c.Options.Now().Add(c.Options.RefreshSkew).UnixMilli() {
				return credential.Clone(), nil
			}
			if c.Refresher == nil {
				return clicredential.Lark{}, ErrUnavailable
			}
			lease := uuid.New()
			until := time.Now().Add(c.Options.RefreshTimeout + 2*c.Options.RecordTimeout + 5*time.Second)
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return clicredential.Lark{}, ErrUnavailable
			}
			// Register admission before leaving the mutex; Close can now wait
			// on its own deadline while a database lock blocks the claim. The
			// admitted claim owns its slot through any independent rotation.
			c.workers.Add(1)
			c.mu.Unlock()
			claimed, e := a.claim(ctx, row.Generation, lease, until)
			if e != nil {
				c.workers.Done()
				return clicredential.Lark{}, ErrUnavailable
			}
			if claimed {
				go c.refresh(a, row.Generation, lease, credential.Clone())
			} else {
				c.workers.Done()
			}
		} else if row.RefreshState == "refreshing" && row.LeaseUntil != nil && !row.LeaseUntil.After(time.Now()) {
			// An abandoned lease never causes reuse of the uncertain refresh token.
			// Its conditional database cleanup is admitted work too: Close must
			// not report drained while this claim is still waiting on a row lock.
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				return clicredential.Lark{}, ErrUnavailable
			}
			c.workers.Add(1)
			c.mu.Unlock()
			_, e = a.claim(ctx, row.Generation, uuid.New(), time.Now().Add(time.Second))
			c.workers.Done()
			if e != nil {
				return clicredential.Lark{}, ErrUnavailable
			}
		}
		timer := time.NewTimer(c.Options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clicredential.Lark{}, ctx.Err()
		case <-timer.C:
		}
	}
}
func (c *Coordinator) refresh(a refreshAccess, generation int64, lease uuid.UUID, old clicredential.Lark) {
	defer c.workers.Done()
	// Supplier rotation owns a bounded lifecycle independent of any waiter.
	ctx, stop := context.WithTimeout(context.Background(), c.Options.RefreshTimeout)
	next, e := c.Refresher.Refresh(ctx, old)
	stop()
	code := "refresh_uncertain"
	if e == nil && (!next.Valid() || !next.Matches(a.binding) || next.AppSecret != old.AppSecret) {
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
			c.Log.Warn("Lark CLI刷新状态回写失败", "stage", "refresh_failure_record", "versionId", a.version.String())
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
