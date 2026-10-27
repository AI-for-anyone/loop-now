package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func githubStoreFixture(t *testing.T) projectMigrationEnv { return githubStoreFixtureAt(t, 0) }

// githubStoreFixtureAt stops at ceiling (0: latest); 026 cannot be rolled back.
func githubStoreFixtureAt(t *testing.T, ceiling int64) projectMigrationEnv {
	t.Helper()
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(pgtest.URL(t))
	if err != nil || config.ConnConfig.Database != "loop_now_test" {
		t.Fatal("GitHub fixture requires the dedicated TEST database")
	}
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig)
	if err != nil {
		t.Fatal("TEST connection unavailable")
	}
	schema := "github_login_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error("self-owned TEST schema cleanup failed")
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.RuntimeParams["application_name"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("TEST pool unavailable")
	}
	st := &store.Store{Pool: pool}
	t.Cleanup(st.Close)
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateFixture(ctx, st, ceiling); err != nil {
		t.Fatal(err)
	}
	defaults, err := st.EnsureDefaults(ctx, "GitHub TEST组织", "默认项目")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, defaults.Organization.ID, "GitHub TEST项目")
	if err != nil {
		t.Fatal(err)
	}
	return projectMigrationEnv{st, templates, p.ID}
}

func githubWrite(e projectMigrationEnv, user, login, value string, scopes []string) store.ProjectCredentialWrite {
	identity := &cm.GitHubIdentity{Host: "github.com", UserID: user, Login: login, GrantedScopes: scopes, VerifiedAt: time.Now().UTC()}
	plain, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{"GH_TOKEN": value}, Files: []clitemplate.PayloadFile{}})
	return store.ProjectCredentialWrite{AuthMethod: "oauth", AccountLabel: &login, GitHubIdentity: identity, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1", FieldNames: []string{"token"}, EnvNames: []string{"GH_TOKEN"}}, Secret: plain}
}

func githubOperation(t *testing.T, e projectMigrationEnv, target *cm.Account, expires time.Time) cm.CLIAuthOperation {
	t.Helper()
	in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gh", AuthType: "github_user"}
	if target != nil {
		in.AccountID, in.ExpectedRevision = &target.ID, &target.Revision
	}
	op, created, err := e.s.NewProjectCLIAuthOperation(context.Background(), e.project, in, expires)
	if err != nil || !created {
		t.Fatal("operation create failed", err)
	}
	return op
}

func TestGitHubStoreVersionIdentityReLoginAndPAT(t *testing.T) {
	e := githubStoreFixture(t)
	ctx := context.Background()
	op := githubOperation(t, e, nil, time.Now().Add(time.Minute))
	view, err := e.s.SetProjectGitHubAuthorization(ctx, e.project, op.ID, "https://github.com/login/device", "ABCD-1234", op.ExpiresAt)
	if err != nil || view.Status != "awaiting_authorization" || view.UserCode == nil {
		t.Fatal("authorization pair not published", err)
	}
	completed, first, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, githubWrite(e, "101", "alice", "fixture-A", nil))
	if err != nil || first == nil || completed.Status != "succeeded" || completed.UserCode != nil || completed.AuthorizationURL != nil || first.View.Name != "alice" || first.View.Status != "logged_in" || first.View.GitHubIdentity == nil || first.View.GitHubIdentity.GrantedScopes != nil {
		t.Fatal("new account/identity/terminal not committed together", err)
	}
	v1 := *first.View.CurrentVersionID
	before, _, err := e.s.GetProjectCLIVersion(ctx, first.View.ID, v1)
	if err != nil {
		t.Fatal(err)
	}
	remark := "保留人的备注"
	renamed, err := e.s.MutateProjectCredential(ctx, e.project, first.View.ID, first.View.Revision, &remark, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	relogin := githubOperation(t, e, &renamed.View, time.Now().Add(time.Minute))
	_, second, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, relogin.ID, githubWrite(e, "202", "bob", "fixture-B", []string{}))
	if err != nil || second == nil || second.View.ID != first.View.ID || second.View.Name != remark || second.View.GitHubIdentity.UserID != "202" || second.View.GitHubIdentity.GrantedScopes == nil || *second.View.CurrentVersionID == v1 {
		t.Fatal("different-user replacement or remark preservation failed", err)
	}
	old, _, err := e.s.GetProjectCLIVersion(ctx, first.View.ID, v1)
	if err != nil || !bytes.Equal(old.Secret, before.Secret) || !bytes.Contains(old.Secret, []byte("fixture-A")) {
		t.Fatal("replacement rewrote original version credential", err)
	}
	static := githubWrite(e, "303", "unused", "fixture-PAT", nil)
	static.AuthMethod, static.AccountLabel, static.GitHubIdentity = "static", nil, nil
	pat, err := e.s.MutateProjectCredential(ctx, e.project, second.View.ID, second.View.Revision, nil, &static, false)
	if err != nil || pat.View.Status != "saved" || pat.View.GitHubIdentity != nil || pat.View.AuthMethod != nil || pat.View.Name != remark {
		t.Fatal("PAT inherited OAuth identity or state", err)
	}
	patLogin := githubOperation(t, e, &pat.View, time.Now().Add(time.Minute))
	_, third, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, patLogin.ID, githubWrite(e, "404", "charlie", "fixture-C", nil))
	if err != nil || third == nil || third.View.Name != remark || third.View.GitHubIdentity.UserID != "404" {
		t.Fatal("old PAT could not become OAuth", err)
	}
	var identities, google int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM github_cli_identities WHERE account_id=$1`, first.View.ID).Scan(&identities); err != nil || identities != 3 {
		t.Fatal("immutable identity history lost", err)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM google_cli_grants WHERE account_id=$1`, first.View.ID).Scan(&google); err != nil || google != 0 {
		t.Fatal("GitHub incorrectly created Google grant", err)
	}
}

func TestGitHubStoreStableIDCLIIsolationAndRollback(t *testing.T) {
	e := githubStoreFixture(t)
	ctx := context.Background()
	expires := time.Now().Add(time.Minute)
	op := githubOperation(t, e, nil, expires)
	in := cm.CLIAuthStart{OperationID: op.ID, CLI: "gh", AuthType: "github_user"}
	if same, created, err := e.s.NewProjectCLIAuthOperation(ctx, e.project, in, expires); err != nil || created || same.ID != op.ID {
		t.Fatal("same start was not idempotent", err)
	}
	name := "Google"
	if _, created, err := e.s.NewProjectCLIAuthOperation(ctx, e.project, cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gcloud", AuthType: "google_user", Name: &name}, expires); err != nil || !created {
		t.Fatal("Google and GitHub activities interfered", err)
	}
	for _, changed := range []cm.CLIAuthStart{{OperationID: op.ID, CLI: "gcloud", AuthType: "google_user", Name: &name}, {OperationID: uuid.New(), CLI: "gh", AuthType: "github_user"}} {
		_, _, err := e.s.NewProjectCLIAuthOperation(ctx, e.project, changed, expires)
		var app *apperr.Error
		if !errors.As(err, &app) || app.Code != "cli_auth_in_progress" {
			t.Fatal("missing operation conflict")
		}
		b, _ := json.Marshal(app.Details)
		if !bytes.Contains(b, []byte(`"cli":"gh"`)) {
			t.Fatal("conflict reported an incorrect CLI")
		}
	}
	google, err := e.s.ListProjectCLIAuthOperations(ctx, e.project, nil)
	if err != nil || len(google) != 1 || google[0].CLI != "gcloud" {
		t.Fatal("legacy list default changed", err)
	}
	gh, err := e.s.ListProjectCLIAuthOperations(ctx, e.project, nil, "gh")
	if err != nil || len(gh) != 1 || gh[0].ID != op.ID {
		t.Fatal("GitHub list not isolated", err)
	}
	w := githubWrite(e, "1", "alice", "fixture", nil)
	w.Secret = nil
	if _, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, w); err == nil || saved != nil {
		t.Fatal("failed write was reported committed")
	}
	for _, table := range []string{"project_credential_accounts", "project_cli_versions", "github_cli_identities"} {
		var n int
		if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("rollback left partial account/version/identity", table)
		}
	}
	if _, err = e.s.EndProjectCLIAuthOperation(ctx, e.project, op.ID, "cancelled", "auth_cancelled"); err != nil {
		t.Fatal(err)
	}
	w = githubWrite(e, "1", "alice", "fixture", nil)
	end, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, w)
	if err != nil || saved != nil || end.Status != "cancelled" {
		t.Fatal("late result saved or revived cancelled operation", err)
	}
}

func TestGitHubStoreLockWaitPastExpiryAndFinalFence(t *testing.T) {
	t.Run("lock_wait", func(t *testing.T) {
		e := githubStoreFixture(t)
		ctx := context.Background()
		op := githubOperation(t, e, nil, time.Now().Add(time.Second))
		tx, err := e.s.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `SELECT id FROM projects WHERE id=$1 FOR UPDATE`, e.project); err != nil {
			t.Fatal(err)
		}
		w := githubWrite(e, "1", "alice", "fixture", nil)
		done := make(chan error, 1)
		go func() {
			end, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, w)
			if err == nil && (saved != nil || end.Status != "expired") {
				err = errors.New("late lock claim was accepted")
			}
			done <- err
		}()
		application := e.s.Pool.Config().ConnConfig.RuntimeParams["application_name"]
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE 'SELECT deleted_at FROM projects%'`, application).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("deterministic PG lock barrier not reached")
			}
			time.Sleep(time.Millisecond)
		}
		if pause := time.Until(op.ExpiresAt) + 20*time.Millisecond; pause > 0 {
			time.Sleep(pause)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			if err != nil {
				t.Fatal("expired lock waiter saved credentials", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("lock waiter did not finish")
		}
	})
	t.Run("expiry_during_write_rolls_everything_back", func(t *testing.T) {
		e := githubStoreFixture(t)
		ctx := context.Background()
		op := githubOperation(t, e, nil, time.Now().Add(time.Second))
		w := githubWrite(e, "1", "alice", "fixture", nil)
		// SHARE blocks only the identity INSERT (not earlier reads), so the write
		// stalls after the first deadline check and before the final fence.
		tx, err := e.s.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, `LOCK TABLE github_cli_identities IN SHARE MODE`); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, w)
			if saved != nil {
				err = errors.New("expired write reported saved")
			}
			done <- err
		}()
		application := e.s.Pool.Config().ConnConfig.RuntimeParams["application_name"]
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.application_name=$1 AND l.locktype='relation' AND l.relation='github_cli_identities'::regclass AND l.mode='RowExclusiveLock' AND NOT l.granted`, application).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("deterministic PG lock barrier not reached")
			}
			time.Sleep(time.Millisecond)
		}
		if pause := time.Until(op.ExpiresAt) + 20*time.Millisecond; pause > 0 {
			time.Sleep(pause)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			if !errors.Is(err, store.ErrCLIAuthExpired) {
				t.Fatal("final expiry fence failed", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("write did not finish")
		}
		var accounts, versions, identities int
		if err := e.s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_credential_accounts),(SELECT count(*) FROM project_cli_versions),(SELECT count(*) FROM github_cli_identities)`).Scan(&accounts, &versions, &identities); err != nil || accounts+versions+identities != 0 {
			t.Fatal("final fence retained partial writes", err)
		}
	})
}

func TestGitHubMigrationUpDownAndAtomicDDL(t *testing.T) {
	e := githubStoreFixtureAt(t, 25)
	ctx := context.Background()
	if err := e.s.MigrateDownTo(ctx, 17); err != nil {
		t.Fatal("empty 018 Down failed", err)
	}
	mustExec(t, e.s, `CREATE TABLE github_cli_identities(fixture int)`)
	if err := e.s.MigrateUpTo(ctx, 18); err == nil {
		t.Fatal("DDL collision did not reject migration")
	}
	var ledger, columns int
	if err := e.s.Pool.QueryRow(ctx, `SELECT max(version_id),(SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='project_cli_auth_operations' AND column_name='user_code') FROM goose_db_version WHERE is_applied`).Scan(&ledger, &columns); err != nil || ledger != 17 || columns != 0 {
		t.Fatal("failed migration retained DDL/ledger", err)
	}
	var definition string
	if err := e.s.Pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='cli_auth_kind' AND conrelid='project_cli_versions'::regclass`).Scan(&definition); err != nil || strings.Contains(definition, "'gh'") {
		t.Fatal("preceding auth constraint alteration did not roll back", err)
	}
	mustExec(t, e.s, `DROP TABLE github_cli_identities`)
	if err := e.s.MigrateUpTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	op := githubOperation(t, e, nil, time.Now().Add(time.Minute))
	if _, err := e.s.EndProjectCLIAuthOperation(ctx, e.project, op.ID, "failed", "oauth_failed"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateDownTo(ctx, 17); err == nil {
		t.Fatal("Down erased GitHub failure history")
	}
	read, err := e.s.GetProjectCLIAuthOperation(ctx, e.project, op.ID)
	if err != nil || read.Status != "failed" {
		t.Fatal("rejected Down changed history", err)
	}
}

func TestGitHubNativeOperationConstraints(t *testing.T) {
	e := githubStoreFixture(t)
	ctx := context.Background()
	a, err := e.s.CreateProjectCredential(ctx, e.project, "cli", "gh", "fixture", githubWrite(e, "1", "alice", "fixture", nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, cli, auth, status                           string
		nameValue, target, revision, url, code, completed any
	}{
		{name: "wrong_pair", cli: "gh", auth: "google_user", status: "pending"},
		{name: "gh_name", cli: "gh", auth: "github_user", status: "pending", nameValue: "not accepted"},
		{name: "google_missing_name", cli: "gcloud", auth: "google_user", status: "pending"},
		{name: "target_missing_revision", cli: "gh", auth: "github_user", status: "pending", target: a.View.ID},
		{name: "pending_code", cli: "gh", auth: "github_user", status: "pending", code: "ABCD-1234"},
		{name: "awaiting_missing_code", cli: "gh", auth: "github_user", status: "awaiting_authorization", url: "https://github.com/login/device"},
		{name: "awaiting_missing_url", cli: "gh", auth: "github_user", status: "awaiting_authorization", code: "ABCD-1234"},
		{name: "awaiting_bad_code", cli: "gh", auth: "github_user", status: "awaiting_authorization", url: "https://github.com/login/device", code: "invalid"},
		{name: "awaiting_bad_url", cli: "gh", auth: "github_user", status: "awaiting_authorization", url: "https://foreign.invalid/", code: "ABCD-1234"},
		{name: "google_code", cli: "gcloud", auth: "google_user", status: "pending", nameValue: "Google", code: "ABCD-1234"},
		{name: "terminal_code", cli: "gh", auth: "github_user", status: "failed", code: "ABCD-1234", completed: time.Now()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.s.Pool.Exec(ctx, `INSERT INTO project_cli_auth_operations(id,project_id,cli_name,auth_type,request_hash,name,target_account_id,target_revision,status,authorization_url,user_code,completed_at,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, uuid.New(), e.project, tc.cli, tc.auth, strings.Repeat("a", 64), tc.nameValue, tc.target, tc.revision, tc.status, tc.url, tc.code, tc.completed, time.Now().Add(time.Minute))
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || pg.Code != "23514" {
				t.Fatal("native operation CHECK not enforced")
			}
		})
	}
	if err = e.s.MigrateDownTo(ctx, 17); err == nil {
		t.Fatal("Down accepted OAuth version/identity without operations")
	}
	// Deliberately alter only a synthetic fixture to isolate the composite FK.
	mustExec(t, e.s, `DELETE FROM github_cli_identities WHERE version_id=$1`, *a.View.CurrentVersionID)
	if _, err = e.s.Pool.Exec(ctx, `INSERT INTO github_cli_identities(version_id,account_id,host,user_id,login,verified_at)VALUES($1,$2,'github.com','1','alice',clock_timestamp())`, *a.View.CurrentVersionID, uuid.New()); err == nil {
		t.Fatal("identity could be rebound to another account")
	}
	if err = e.s.MigrateDownTo(ctx, 17); err == nil {
		t.Fatal("Down accepted OAuth version with no identity")
	}
}

func TestGitHubProjectDeletionRollbackPurgeAndLateResult(t *testing.T) {
	e := githubStoreFixture(t)
	ctx := context.Background()
	account, err := e.s.CreateProjectCredential(ctx, e.project, "cli", "gh", "remark", githubWrite(e, "1", "alice", "fixture-A", nil))
	if err != nil {
		t.Fatal(err)
	}
	write := githubWrite(e, "2", "bob", "fixture-B", []string{})
	account, err = e.s.MutateProjectCredential(ctx, e.project, account.View.ID, account.View.Revision, nil, &write, false)
	if err != nil {
		t.Fatal(err)
	}
	op := githubOperation(t, e, &account.View, time.Now().Add(time.Minute))
	if _, err = e.s.SetProjectGitHubAuthorization(ctx, e.project, op.ID, "https://github.com/login/device", "ABCD-1234", op.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	mustExec(t, e.s, `CREATE FUNCTION github_delete_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic rollback'; END $$`)
	mustExec(t, e.s, `CREATE TRIGGER github_delete_fail BEFORE UPDATE OF deleted_at ON projects FOR EACH ROW EXECUTE FUNCTION github_delete_fail()`)
	if _, err = e.s.DeleteProject(ctx, e.project); err == nil {
		t.Fatal("injected project deletion did not roll back")
	}
	read, err := e.s.GetProjectCLIAuthOperation(ctx, e.project, op.ID)
	if err != nil || read.Status != "awaiting_authorization" || read.UserCode == nil || read.AuthorizationURL == nil {
		t.Fatal("rollback cleared live authorization", err)
	}
	var versions, identities int
	if err = e.s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions),(SELECT count(*) FROM github_cli_identities)`).Scan(&versions, &identities); err != nil || versions != 2 || identities != 2 {
		t.Fatal("rollback lost current/history", err)
	}
	mustExec(t, e.s, `DROP TRIGGER github_delete_fail ON projects`)
	if _, err = e.s.DeleteProject(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	if err = e.s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions),(SELECT count(*) FROM github_cli_identities)`).Scan(&versions, &identities); err != nil || versions != 0 || identities != 0 {
		t.Fatal("legal project deletion retained secrets/identity", err)
	}
	var status string
	var code, uri *string
	if err = e.s.Pool.QueryRow(ctx, `SELECT status,user_code,authorization_url FROM project_cli_auth_operations WHERE id=$1`, op.ID).Scan(&status, &code, &uri); err != nil || status != "cancelled" || code != nil || uri != nil {
		t.Fatal("project deletion did not clear operation interaction", err)
	}
	if _, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, write); err != nil || saved != nil {
		t.Fatal("deleted project was revived by late result", err)
	}
}

func TestGitHubConcurrentStartAndCompletionCAS(t *testing.T) {
	e := githubStoreFixture(t)
	ctx := context.Background()
	in := cm.CLIAuthStart{OperationID: uuid.New(), CLI: "gh", AuthType: "github_user"}
	start := make(chan struct{})
	var created atomic.Int32
	var group sync.WaitGroup
	errorsCh := make(chan error, 2)
	for range 2 {
		group.Go(func() {
			<-start
			_, fresh, err := e.s.NewProjectCLIAuthOperation(ctx, e.project, in, time.Now().Add(time.Minute))
			if fresh {
				created.Add(1)
			}
			errorsCh <- err
		})
	}
	close(start)
	group.Wait()
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	if created.Load() != 1 {
		t.Fatal("concurrent same-ID start created more than once")
	}
	_, first, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, in.OperationID, githubWrite(e, "1", "alice", "fixture-A", nil))
	if err != nil || first == nil {
		t.Fatal(err)
	}
	op := githubOperation(t, e, &first.View, time.Now().Add(time.Minute))
	remark := "new revision while authorizing"
	latest, err := e.s.MutateProjectCredential(ctx, e.project, first.View.ID, first.View.Revision, &remark, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	w := githubWrite(e, "2", "bob", "fixture-B", nil)
	end, saved, err := e.s.CompleteProjectCLIAuthOperation(ctx, e.project, op.ID, w)
	if err != nil || saved != nil || end.Status != "failed" || end.ErrorCode == nil || *end.ErrorCode != "version_conflict" {
		t.Fatal("completion CAS did not fence before write", err)
	}
	current, err := e.s.GetProjectCredential(ctx, e.project, first.View.ID)
	if err != nil || current.View.Revision != latest.View.Revision || *current.View.CurrentVersionID != *first.View.CurrentVersionID || current.View.GitHubIdentity.UserID != "1" {
		t.Fatal("CAS failure replaced saved account", err)
	}
}
