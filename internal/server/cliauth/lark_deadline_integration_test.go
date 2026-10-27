package cliauth

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
)

func TestLarkLoginCommitSourceLockCrossesDeadlineWithoutSeal(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	if e := f.st.MigrateThrough(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), 20); e != nil {
		t.Fatal(e)
	}
	source := larkSource(t, f)
	in := larkStart(source)
	deadline := time.Now().Add(time.Second)
	op, _, err := f.st.NewProjectCLIAuthOperation(ctx, f.project, in, deadline)
	if err != nil {
		t.Fatal(err)
	}
	block, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer block.Rollback(ctx)
	var pid int
	if err = block.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = block.Exec(ctx, `SELECT id FROM project_credential_accounts WHERE id=$1 FOR UPDATE`, source.View.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	identity := cm.LarkIdentity{Brand: "feishu", AppID: "app-fixture", OpenID: "ou-fixture", DisplayName: "test", VerifiedAt: time.Now()}
	go func() {
		_, _, e := f.st.CompleteProjectCLIAuthOperation(ctx, f.project, op.ID, store.ProjectCredentialWrite{AuthMethod: "oauth", LarkIdentity: &identity, Secret: []byte("must-not-write")})
		done <- e
	}()
	// Observe a real PostgreSQL lock waiter blocked by this transaction, not
	// merely a goroutine scheduled after the operation already expired.
	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		var blocked bool
		if err = block.QueryRow(waitCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatal("commit did not reach the held source row lock")
		case <-time.After(time.Millisecond):
		}
	}
	for {
		var expired bool
		if err = block.QueryRow(waitCtx, `SELECT clock_timestamp()>$1::timestamptz`, deadline).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err = block.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	read, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, op.ID)
	if err != nil || read.Status != "expired" || read.AuthorizationURL != nil || read.UserCode != nil || read.SourceVersionID == nil {
		t.Fatal("late login result lost terminal source audit")
	}
	var written int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions WHERE secret=$1`, []byte("must-not-write")).Scan(&written); err != nil || written != 0 {
		t.Fatal("late login result wrote credential", err)
	}
	var grants int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants`).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("expired source-lock waiter created user grant")
	}
}
