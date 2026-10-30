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
	// 提交写账号时挡在测试持有的行锁上，提交的期限必然先到；序列不随回滚撤销，记下提交确实走到了这里。
	// 被取消的提交事务要等取消请求送达才放开项目行锁，终态回写用新的期限等它，期限不能小到在繁忙的 CI 上跟取消请求抢时间。
	if _, err := f.st.Pool.Exec(ctx, `CREATE TABLE github_commit_gate(id int); INSERT INTO github_commit_gate VALUES (1); CREATE SEQUENCE github_commit_reached;
		CREATE FUNCTION github_commit_wait() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM nextval('github_commit_reached'); PERFORM 1 FROM github_commit_gate FOR UPDATE; RETURN NEW; END $$;
		CREATE TRIGGER github_commit_wait BEFORE INSERT ON project_credential_accounts FOR EACH ROW EXECUTE FUNCTION github_commit_wait()`); err != nil {
		t.Fatal(err)
	}
	gate, err := f.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(ctx, `SELECT 1 FROM github_commit_gate FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	s := NewService(f.st, nil, nil, ServiceOptions{LoginTimeout: time.Minute, RecordTimeout: 2 * time.Second}, nil)
	s.GitHub = provider.Client
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	id := uuid.New()
	if _, err = s.Start(ctx, f.project, cm.CLIAuthStart{OperationID: id, CLI: "gh", AuthType: "github_user"}); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w, running := s.workers[id]
	s.mu.Unlock()
	if running {
		select {
		case <-w.done:
		case <-time.After(30 * time.Second):
			t.Fatal("login worker did not finish")
		}
	}
	var reached bool
	if err = f.st.Pool.QueryRow(ctx, `SELECT is_called FROM github_commit_reached`).Scan(&reached); err != nil || !reached {
		t.Fatal("login ended before the commit", err)
	}
	op, err := f.st.GetProjectCLIAuthOperation(ctx, f.project, id)
	if err != nil || op.Status != "failed" {
		t.Fatal("commit context expiry left operation pending", op.Status, err)
	}
	var accounts int
	if err = f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM project_credential_accounts`).Scan(&accounts); err != nil || accounts != 0 || op.CompletedAt == nil || op.UserCode != nil || op.AuthorizationURL != nil {
		t.Fatal("failed commit retained partial data or interaction metadata", err)
	}
}
