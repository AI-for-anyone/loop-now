package larkauth

import (
	"bytes"
	"context"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLarkLeaseBoundaryNoLateOrForeignCommit(t *testing.T) {
	for _, scenario := range []string{"expired_before_write", "expired_during_write", "foreign_agent", "stale_generation"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLarkFixture(t)
			ctx := context.Background()
			before, e := f.st.GetLarkCLIGrant(ctx, f.binding)
			if e != nil {
				t.Fatal(e)
			}
			until := time.Now().Add(time.Second)
			lease := uuid.New()
			ok, e := f.st.ClaimLarkCLIRefresh(ctx, f.binding, 1, lease, until)
			if e != nil || !ok {
				t.Fatal("initial claim failed")
			}
			waitExpired := func() {
				t.Helper()
				// Compare by wall clock (Round(0) strips the monotonic reading): clock_timestamp() in the database uses
				// the wall clock, which on Windows is coarser than the monotonic clock, so when the monotonic clock says
				// "expired" the database may not have reached that point yet.
				for !time.Now().Round(0).After(until.Round(0)) {
					time.Sleep(time.Millisecond)
				}
			}
			// Count credential writes with the statement hook (rolled-back ones count too). Expiring during the write:
			// inside the commit transaction, after the lease check passed, pause before the write statement until the
			// lease expires.
			var writes atomic.Int64
			f.st.Pool.SetQueryHook(func(_ context.Context, q string) {
				if strings.HasPrefix(strings.TrimSpace(q), "UPDATE project_cli_versions") {
					writes.Add(1)
					if scenario == "expired_during_write" {
						waitExpired()
					}
				}
			})
			defer f.st.Pool.SetQueryHook(nil)
			b := f.binding
			generation := int64(1)
			switch scenario {
			case "expired_before_write":
				waitExpired()
			case "foreign_agent":
				a, e := f.st.CreateAgent(ctx, store.ProjectScope(b.ProjectID), "foreign-finisher", "openai", "fake-model", "")
				if e != nil {
					t.Fatal(e)
				}
				b.AgentID = a.ID
			case "stale_generation":
				generation = 2
			}
			e = f.st.CommitLarkCLIRefresh(ctx, b, generation, lease, []byte("synthetic-uncommitted-rotation"))
			if e == nil {
				t.Fatal("late/foreign result committed")
			}
			want := int64(0)
			if scenario == "expired_during_write" {
				want = 1
			}
			if got := writes.Load(); got != want {
				t.Fatal("invalid result reached unexpected write stage", got)
			}
			after, e := f.st.GetLarkCLIGrant(ctx, f.binding)
			if e != nil || after.Generation != before.Generation || !bytes.Equal(after.Secret, before.Secret) {
				t.Fatal("failed commit partially changed credential/generation")
			}
			if scenario == "expired_before_write" || scenario == "expired_during_write" {
				var calls atomic.Int32
				c := NewCoordinator(f.st, larkRefreshFunc(func(context.Context, clicredential.Lark) (clicredential.Lark, error) {
					calls.Add(1)
					return clicredential.Lark{}, nil
				}), CoordinatorOptions{WaitTimeout: time.Second}, nil)
				_, e = c.Resolve(ctx, f.binding)
				c.Close(ctx)
				if e == nil || calls.Load() != 0 {
					t.Fatal("abandoned lease replayed old refresh")
				}
				after, e = f.st.GetLarkCLIGrant(ctx, f.binding)
				if e != nil || after.RefreshState != "unavailable" || after.Generation != 1 {
					t.Fatal("abandoned lease did not persist uncertainty")
				}
			}
		})
	}
}
