package larkauth

import (
	"context"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type larkFixture struct {
	st         *store.Store
	binding    clicredential.LarkBinding
	credential clicredential.Lark
}

type larkRefreshFunc func(context.Context, clicredential.Lark) (clicredential.Lark, error)

func (f larkRefreshFunc) Refresh(ctx context.Context, c clicredential.Lark) (clicredential.Lark, error) {
	return f(ctx, c)
}

func newLarkFixture(t *testing.T) larkFixture {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	d, e := st.EnsureDefaults(ctx, "Lark TEST组织", "默认项目")
	if e != nil {
		t.Fatal(e)
	}
	p, e := st.CreateProject(ctx, d.Organization.ID, "Lark隔离项目")
	if e != nil {
		t.Fatal(e)
	}
	a, e := st.CreateAgent(ctx, store.ProjectScope(p.ID), "Lark测试", "openai", "fake-model", "")
	if e != nil {
		t.Fatal(e)
	}
	c := clicredential.Lark{Type: "lark_user", AppSecret: "fixture-app-secret", AccessToken: "fixture-access-1", RefreshToken: "fixture-refresh-1", ExpiresAt: time.Now().Add(time.Minute).UnixMilli(), RefreshExpiresAt: time.Now().Add(24 * time.Hour).UnixMilli(), Identity: cm.LarkIdentity{Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", DisplayName: "隔离测试用户", VerifiedAt: time.Now()}, RuntimeVariantVersion: clicredential.LarkUserVariant}
	plain, e := c.Encode()
	if e != nil {
		t.Fatal(e)
	}
	defer clear(plain)
	label := c.Identity.OpenID
	r, e := st.CreateProjectCredential(ctx, p.ID, "cli", "lark-cli", "离线用户", store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &label, LarkIdentity: &c.Identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
	if e != nil {
		t.Fatal(e)
	}
	b := clicredential.LarkBinding{ProjectID: p.ID, AccountID: r.View.ID, VersionID: *r.View.CurrentVersionID, AgentID: a.ID, Brand: c.Identity.Brand, AppID: c.Identity.AppID, OpenID: c.Identity.OpenID, RuntimeVariantVersion: clicredential.LarkUserVariant}
	return larkFixture{st, b, c}
}

func TestLarkPGTwoCoordinatorsCancelWaiterAndFreshProcess(t *testing.T) {
	f := newLarkFixture(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	rf := larkRefreshFunc(func(ctx context.Context, old clicredential.Lark) (clicredential.Lark, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return old, ctx.Err()
		}
		old.AccessToken = "fixture-access-2"
		old.RefreshToken = "fixture-refresh-2"
		old.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return old, nil
	})
	c1 := NewCoordinator(f.st, rf, CoordinatorOptions{}, nil)
	c2 := NewCoordinator(f.st, rf, CoordinatorOptions{}, nil)
	t.Cleanup(func() { c1.Close(ctx); c2.Close(ctx) })
	wait, cancel := context.WithCancel(ctx)
	first := make(chan error, 1)
	go func() { _, e := c1.Resolve(wait, f.binding); first <- e }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh not started")
	}
	cancel()
	if e := <-first; e == nil {
		t.Fatal("cancelled waiter succeeded")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := c2.Resolve(ctx, f.binding)
			if e == nil && c.AccessToken != "fixture-access-2" {
				t.Error("returned uncommitted or old token")
			}
			errs <- e
		}()
	}
	close(release)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("refresh token consumed more than once")
	}
	r, e := f.st.GetLarkCLIGrant(ctx, f.binding)
	if e != nil || r.Generation != 2 || r.RefreshState != "ready" {
		t.Fatal("rotation not durably committed")
	}
	fresh := NewCoordinator(f.st, nil, CoordinatorOptions{}, nil)
	defer fresh.Close(ctx)
	if _, e = fresh.Resolve(ctx, f.binding); e != nil {
		t.Fatal("fresh coordinator could not use committed grant")
	}
	bad := f.binding
	bad.OpenID = "ou-other"
	if _, e = fresh.Resolve(ctx, bad); e == nil {
		t.Fatal("wrong identity resolved")
	}
}

func TestLarkClaimWaitingAcrossLeaseDeadlineDoesNotClaim(t *testing.T) {
	f := newLarkFixture(t)
	ctx := context.Background()
	// 测试占住写连接，领取停在等写连接上，直到租约期限已过才放开。
	release := sqlitetest.HoldWriter(t, f.st.Pool)
	until := time.Now().Add(100 * time.Millisecond)
	result := make(chan bool, 1)
	errs := make(chan error, 1)
	go func() {
		ok, e := f.st.ClaimLarkCLIRefresh(ctx, f.binding, 1, uuid.New(), until)
		result <- ok
		errs <- e
	}()
	sqlitetest.WaitWriteWaiters(t, ctx, f.st.Pool, 1)
	// 按墙上时钟比较，理由见 lease_boundaries_integration_test.go 的 waitExpired。
	for !time.Now().Round(0).After(until.Round(0)) {
		time.Sleep(time.Millisecond)
	}
	release()
	if <-result {
		t.Fatal("late claim crossed its lease deadline")
	}
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	r, e := f.st.GetLarkCLIGrant(ctx, f.binding)
	if e != nil || r.RefreshState != "ready" || r.Generation != 1 {
		t.Fatal("late claim changed grant")
	}
}
