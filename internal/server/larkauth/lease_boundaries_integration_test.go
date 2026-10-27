package larkauth

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
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
				for {
					var expired bool
					if e := f.st.Pool.QueryRow(ctx, `SELECT clock_timestamp()>$1::timestamptz`, until).Scan(&expired); e != nil {
						t.Fatal(e)
					}
					if expired {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
			// Count credential writes with a nontransactional sequence. During-write
			// expiry is held inside the commit transaction after the lease check.
			wait := ""
			if scenario == "expired_during_write" {
				wait = fmt.Sprintf("WHILE clock_timestamp()<='%s'::timestamptz LOOP PERFORM pg_sleep(0.005); END LOOP;", until.UTC().Format(time.RFC3339Nano))
			}
			if _, e = f.st.Pool.Exec(ctx, `CREATE SEQUENCE lark_version_writes;
 CREATE FUNCTION lark_count_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('lark_version_writes'); `+wait+` RETURN NEW; END $$;
 CREATE TRIGGER lark_count_write BEFORE UPDATE ON project_cli_versions FOR EACH ROW EXECUTE FUNCTION lark_count_write()`); e != nil {
				t.Fatal(e)
			}
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
			var writes int64
			if e = f.st.Pool.QueryRow(ctx, `SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM lark_version_writes`).Scan(&writes); e != nil || writes != want {
				t.Fatal("invalid result reached unexpected write stage", e)
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
