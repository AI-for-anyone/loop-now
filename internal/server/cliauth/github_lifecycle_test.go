package cliauth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/githubauth/githubtest"
)

func githubPendingFixture(t *testing.T) (googleFixture, *Service, *githubtest.Attempt, uuid.UUID) {
	t.Helper()
	f := newGoogleFixture(t)
	provider := githubtest.New(t)
	attempt := provider.Add("101", "alice", nil, "")
	s := NewService(f.st, nil, nil, ServiceOptions{LoginTimeout: 5 * time.Second, RecordTimeout: time.Second}, nil)
	s.GitHub = provider.Client
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	id := uuid.New()
	if _, err := s.Start(context.Background(), f.project, cm.CLIAuthStart{OperationID: id, CLI: "gh", AuthType: "github_user"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempt.Waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("actual helper token request not reached")
	}
	return f, s, attempt, id
}

func TestGitHubShutdownPersistsBeforeCancellingHTTP(t *testing.T) {
	f, s, attempt, id := githubPendingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	op, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, id)
	if err != nil || op.Status != "failed" || op.ErrorCode == nil || *op.ErrorCode != "server_restarted" || op.UserCode != nil || op.AuthorizationURL != nil {
		t.Fatal("shutdown did not persist terminal state before late result", err)
	}
	select {
	case <-attempt.Cancelled:
	case <-time.After(time.Second):
		t.Fatal("actual helper HTTP resource did not end")
	}
	attempt.Release()
	var versions int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_cli_versions`).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("late shutdown result saved credential", err)
	}
}

func TestGitHubCancelRollbackDoesNotCancelHelper(t *testing.T) {
	f, s, attempt, id := githubPendingFixture(t)
	ctx := context.Background()
	if _, err := f.st.Pool.Exec(ctx, `CREATE FUNCTION github_cancel_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='cancelled' THEN RAISE EXCEPTION 'synthetic rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER github_cancel_fail BEFORE UPDATE OF status ON project_cli_auth_operations FOR EACH ROW EXECUTE FUNCTION github_cancel_fail()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(ctx, f.project, id); err == nil {
		t.Fatal("injected cancellation transaction did not fail")
	}
	select {
	case <-attempt.Cancelled:
		t.Fatal("rollback cancelled actual helper")
	default:
	}
	op, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, id)
	if err != nil || op.Status != "awaiting_authorization" || op.UserCode == nil {
		t.Fatal("rollback changed operation interaction state", err)
	}
	if _, err = f.st.Pool.Exec(ctx, `DROP TRIGGER github_cancel_fail ON project_cli_auth_operations`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Cancel(ctx, f.project, id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempt.Cancelled:
	case <-time.After(time.Second):
		t.Fatal("committed cancellation did not end HTTP resource")
	}
}

func TestGitHubCommitDeadlineUsesFreshTerminalContext(t *testing.T) {
	f := newGoogleFixture(t)
	provider := githubtest.New(t)
	attempt := provider.Add("101", "alice", nil, "")
	attempt.Release()
	ctx := context.Background()
	if _, err := f.st.Pool.Exec(ctx, `CREATE FUNCTION github_commit_delay() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.4); RETURN NEW; END $$; CREATE TRIGGER github_commit_delay BEFORE INSERT ON project_credential_accounts FOR EACH ROW EXECUTE FUNCTION github_commit_delay()`); err != nil {
		t.Fatal(err)
	}
	s := NewService(f.st, nil, nil, ServiceOptions{LoginTimeout: 5 * time.Second, RecordTimeout: 100 * time.Millisecond}, nil)
	s.GitHub = provider.Client
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	id := uuid.New()
	if _, err := s.Start(ctx, f.project, cm.CLIAuthStart{OperationID: id, CLI: "gh", AuthType: "github_user"}); err != nil {
		t.Fatal(err)
	}
	for until := time.Now().Add(4 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		op, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, id)
		if err != nil {
			t.Fatal(err)
		}
		if op.Status == "failed" {
			var accounts int
			if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_credential_accounts`).Scan(&accounts); err != nil || accounts != 0 || op.CompletedAt == nil || op.UserCode != nil || op.AuthorizationURL != nil {
				t.Fatal("failed commit retained partial data or interaction metadata", err)
			}
			return
		}
	}
	t.Fatal("commit context expiry left operation pending")
}
