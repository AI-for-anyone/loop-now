package larkauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"io"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type failCommitStore struct {
	*store.Store
	after bool
}

func TestLarkFreshSourceRequiresLoginAndLostSupplierResponse(t *testing.T) {
	for _, mode := range []string{"requires_login", "response_lost"} {
		t.Run(mode, func(t *testing.T) {
			f := newLarkFixture(t)
			ctx := context.Background()
			var calls atomic.Int32
			var rf Refresher
			if mode == "requires_login" {
				rf = larkRefreshFunc(func(context.Context, clicredential.Lark) (clicredential.Lark, error) {
					calls.Add(1)
					return clicredential.Lark{}, &SafeError{Code: "reauth_required"}
				})
			} else {
				rf = localProtocolClient(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					io.Copy(io.Discard, r.Body)
					conn, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						conn.Close()
					}
				})
			}
			c := NewCoordinator(f.st, rf, CoordinatorOptions{WaitTimeout: time.Second}, nil)
			_, e := c.Resolve(ctx, f.binding)
			c.Close(ctx)
			if e == nil {
				t.Fatal("failed supplier refresh returned a token")
			}
			row, e := f.st.GetLarkCLIGrant(ctx, f.binding)
			if e != nil || row.RefreshState != "unavailable" || row.Generation != 1 {
				t.Fatal("failed supplier result not persistently failclosed")
			}
			fresh := NewCoordinator(f.st, rf, CoordinatorOptions{WaitTimeout: time.Second}, nil)
			source, e := fresh.TokenSource(f.binding)
			if e != nil {
				t.Fatal(e)
			}
			_, e = source.AccessToken(ctx)
			fresh.Close(ctx)
			if e == nil || calls.Load() != 1 {
				t.Fatal("fresh source retried consumed/uncertain refresh")
			}
			if mode == "requires_login" {
				var safe *SafeError
				if !errors.As(e, &safe) || safe.Code != "requires_login" {
					t.Fatal("persistent typed requires_login lost")
				}
			}
			var operations int
			if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_auth_operations`).Scan(&operations); e != nil || operations != 0 {
				t.Fatal("refresh failure automatically initiated login")
			}
		})
	}
}

type blockedClaimStore struct {
	*store.Store
	entered, release chan struct{}
}

func (s blockedClaimStore) ClaimLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, g int64, l uuid.UUID, until time.Time) (bool, error) {
	close(s.entered)
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-s.release:
	}
	return s.Store.ClaimLarkCLIRefresh(ctx, b, g, l, until)
}
func TestLarkCloseDeadlineWhileAdmittedClaimBlocked(t *testing.T) {
	f := newLarkFixture(t)
	ctx := context.Background()
	st := blockedClaimStore{f.st, make(chan struct{}), make(chan struct{})}
	var calls atomic.Int32
	rf := larkRefreshFunc(func(_ context.Context, c clicredential.Lark) (clicredential.Lark, error) {
		calls.Add(1)
		c.AccessToken = "after-close-access"
		c.RefreshToken = "after-close-refresh"
		c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return c, nil
	})
	c := NewCoordinator(st, rf, CoordinatorOptions{}, nil)
	resolved := make(chan struct{})
	go func() { defer close(resolved); c.Resolve(ctx, f.binding) }()
	<-st.entered
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	start := time.Now()
	e := c.Close(short)
	cancel()
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		close(st.release)
		t.Fatal("Close ignored its own deadline while claim blocked")
	}
	close(st.release)
	drain, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if e = c.Close(drain); e != nil {
		t.Fatal("admitted rotation did not drain")
	}
	<-resolved
	r, e := f.st.GetLarkCLIGrant(ctx, f.binding)
	if e != nil || r.Generation != 2 || r.RefreshState != "ready" || calls.Load() != 1 {
		t.Fatal("admitted claimed refresh was lost at Close")
	}
	if _, e = c.Resolve(ctx, f.binding); e == nil {
		t.Fatal("closed coordinator accepted new work")
	}
}

func (s failCommitStore) CommitLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, g int64, l uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.Store.CommitLarkCLIRefresh(ctx, b, g, l, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement loss")
}
func TestLarkRotationCommitFailureAndAcknowledgementLoss(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "not_committed", true: "committed_ack_lost"}[after], func(t *testing.T) {
			f := newLarkFixture(t)
			ctx := context.Background()
			var calls atomic.Int32
			rf := larkRefreshFunc(func(_ context.Context, c clicredential.Lark) (clicredential.Lark, error) {
				calls.Add(1)
				c.AccessToken = "rotated-access"
				c.RefreshToken = "rotated-refresh"
				c.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
				return c, nil
			})
			c := NewCoordinator(failCommitStore{f.st, after}, rf, CoordinatorOptions{WaitTimeout: time.Second}, nil)
			_, e := c.Resolve(ctx, f.binding)
			c.Close(ctx)
			row, readErr := f.st.GetLarkCLIGrant(ctx, f.binding)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if after {
				if e != nil || row.Generation != 2 || row.RefreshState != "ready" {
					t.Fatal("authoritative committed generation not recovered")
				}
			} else if e == nil || row.Generation != 1 || row.RefreshState != "unavailable" {
				t.Fatal("uncertain old refresh was not failclosed")
			}
			fresh := NewCoordinator(f.st, rf, CoordinatorOptions{WaitTimeout: time.Second}, nil)
			_, e = fresh.Resolve(ctx, f.binding)
			fresh.Close(ctx)
			if (e == nil) != after || calls.Load() != 1 {
				t.Fatal("fresh coordinator replayed uncertain old refresh or lost committed grant")
			}
		})
	}
}
func TestLarkStartedLeaseAgentDeleteAndProjectPurge(t *testing.T) {
	for _, scope := range []string{"agent", "account", "project", "wrong_lease"} {
		t.Run(scope, func(t *testing.T) {
			f := newLarkFixture(t)
			ctx := context.Background()
			lease := uuid.New()
			ok, e := f.st.ClaimLarkCLIRefresh(ctx, f.binding, 1, lease, time.Now().Add(time.Minute))
			if e != nil || !ok {
				t.Fatal("lease not acquired")
			}
			switch scope {
			case "agent":
				if e = f.st.SoftDeleteAgent(ctx, f.binding.AgentID); e != nil {
					t.Fatal(e)
				}
				if _, e = f.st.GetLarkCLIGrant(ctx, f.binding); e == nil {
					t.Fatal("deleted Agent may not get/start new token work")
				}
			case "account":
				r, e := f.st.GetProjectCredential(ctx, f.binding.ProjectID, f.binding.AccountID)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.st.MutateProjectCredential(ctx, f.binding.ProjectID, f.binding.AccountID, r.View.Revision, nil, nil, true); e != nil {
					t.Fatal(e)
				}
			case "project":
				if _, e = f.st.DeleteProject(ctx, f.binding.ProjectID); e != nil {
					t.Fatal(e)
				}
			case "wrong_lease":
				lease = uuid.New()
			}
			plain, _ := f.credential.Encode()
			defer clear(plain)
			e = f.st.CommitLarkCLIRefresh(ctx, f.binding, 1, lease, plain)
			// 删除账号或项目会清空没有进行中任务使用的版本及其授权（迁移 030），已开始的刷新不能再写回。
			if scope == "account" || scope == "project" || scope == "wrong_lease" {
				if e == nil {
					t.Fatal("invalid late owner/lease committed")
				}
			} else if e != nil {
				t.Fatal("retained started lease failed to persist")
			}
			if scope == "account" {
				var grants int
				if e = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants WHERE version_id=$1`, f.binding.VersionID).Scan(&grants); e != nil || grants != 0 {
					t.Fatal("account delete retained grant rows", grants, e)
				}
			}
			if scope == "project" {
				var versions, grants int
				if e = f.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions),(SELECT count(*) FROM lark_cli_grants)`).Scan(&versions, &grants); e != nil || versions != 0 || grants != 0 {
					t.Fatal("project purge retained grant or credential rows")
				}
			}
		})
	}
}
