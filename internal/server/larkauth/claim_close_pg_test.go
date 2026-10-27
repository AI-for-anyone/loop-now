package larkauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
)

// Hold the actual grant row only after Resolve has read its ready generation.
// The observed blocked PID proves the admitted claim, not the initial read,
// reached PostgreSQL while Close and another Resolve need the Go mutex.
func TestLarkCloseDeadlineDuringActualPGClaimLock(t *testing.T) {
	testLarkClaimClose(t, false)
}
func TestLarkCloseDeadlineDuringExpiredLeaseCleanup(t *testing.T) {
	testLarkClaimClose(t, true)
}
func testLarkClaimClose(t *testing.T, expired bool) {
	f := newLarkFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if expired {
		if ok, err := f.st.ClaimLarkCLIRefresh(ctx, f.binding, 1, uuid.New(), time.Now().Add(time.Minute)); err != nil || !ok {
			t.Fatal("seed lease failed")
		}
		if _, err := f.st.Pool.Exec(ctx, `UPDATE lark_cli_grants SET lease_until=clock_timestamp()-interval '1 second' WHERE version_id=$1`, f.binding.VersionID); err != nil {
			t.Fatal(err)
		}
	}
	adapter := blockedClaimStore{f.st, make(chan struct{}), make(chan struct{})}
	refreshStarted, refreshRelease := make(chan struct{}), make(chan struct{})
	var releaseClaimOnce, releaseRefreshOnce sync.Once
	releaseClaim := func() { releaseClaimOnce.Do(func() { close(adapter.release) }) }
	releaseRefresh := func() { releaseRefreshOnce.Do(func() { close(refreshRelease) }) }
	defer releaseClaim()
	defer releaseRefresh()
	var calls atomic.Int32
	c := NewCoordinator(adapter, larkRefreshFunc(func(refreshCtx context.Context, old clicredential.Lark) (clicredential.Lark, error) {
		calls.Add(1)
		close(refreshStarted)
		select {
		case <-refreshRelease:
		case <-refreshCtx.Done():
			return clicredential.Lark{}, refreshCtx.Err()
		}
		next := old.Clone()
		next.AccessToken = "synthetic-after-locked-claim"
		next.RefreshToken = "synthetic-after-locked-refresh"
		next.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return next, nil
	}), CoordinatorOptions{}, nil)
	t.Cleanup(func() {
		releaseClaim()
		releaseRefresh()
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := c.Close(cleanup); err != nil {
			t.Error("coordinator fixture did not drain")
		}
	})
	caller, stopCaller := context.WithCancel(ctx)
	defer stopCaller()
	resolved := make(chan struct{})
	go func() { defer close(resolved); _, _ = c.Resolve(caller, f.binding) }()
	select {
	case <-adapter.entered:
	case <-ctx.Done():
		t.Fatal("ready claim not entered")
	}
	tx, e := f.st.Pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT version_id FROM lark_cli_grants WHERE version_id=$1 FOR UPDATE`, f.binding.VersionID); e != nil {
		t.Fatal(e)
	}
	var blocker uint32
	if e = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); e != nil {
		t.Fatal(e)
	}
	releaseClaim()
	for {
		var waiting bool
		if e = f.st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::integer=ANY(pg_blocking_pids(pid)))`, blocker).Scan(&waiting); e != nil {
			t.Fatal(e)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real PG claim never blocked")
		case <-time.After(time.Millisecond):
		}
	}
	// This second Resolve takes the same c.mu but has no need to read the held
	// grant. A slow claim must not serialize unrelated resolution at Go level.
	other := make(chan error, 1)
	go func() {
		_, err := c.resolve(ctx, refreshAccess{get: func(context.Context) (store.LarkCLIGrant, error) { return store.LarkCLIGrant{}, ErrUnavailable }})
		other <- err
	}()
	select {
	case err := <-other:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal("unexpected independent resolution result")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("claim holds resolver mutex")
	}
	short, stopShort := context.WithTimeout(ctx, 30*time.Millisecond)
	closeResult := make(chan error, 1)
	start := time.Now()
	go func() { closeResult <- c.Close(short) }()
	select {
	case err := <-closeResult:
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
			t.Fatal("Close ignored own deadline")
		}
	case <-time.After(500 * time.Millisecond):
		stopShort()
		t.Fatal("Close blocked on Go mutex")
	}
	stopShort()
	if calls.Load() != 0 {
		t.Fatal("refresh ran before PostgreSQL claim succeeded")
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if !expired {
		select {
		case <-refreshStarted:
		case <-ctx.Done():
			t.Fatal("registered claim was discarded by Close")
		}
	}
	if !expired {
		stopCaller()
	} // supplier rotation must survive the original caller ending.
	releaseRefresh()
	if e = c.Close(ctx); e != nil {
		t.Fatal("registered refresh did not drain")
	}
	select {
	case <-resolved:
	case <-ctx.Done():
		t.Fatal("resolver did not finish")
	}
	row, e := f.st.GetLarkCLIGrant(ctx, f.binding)
	wantGeneration, wantState, wantCalls := int64(2), "ready", int32(1)
	if expired {
		wantGeneration, wantState, wantCalls = 1, "unavailable", 0
	}
	if e != nil || row.Generation != wantGeneration || row.RefreshState != wantState || calls.Load() != wantCalls {
		t.Fatal("admitted rotation was not persisted exactly once")
	}
	if _, e = c.Resolve(ctx, f.binding); !errors.Is(e, ErrUnavailable) {
		t.Fatal("closed coordinator admitted new work")
	}
}
