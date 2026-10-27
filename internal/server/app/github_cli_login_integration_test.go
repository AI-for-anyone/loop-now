package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/githubauth/githubtest"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

func githubAppWorld(t *testing.T) *world {
	t.Helper()
	db, err := url.Parse(pgtest.URL(t))
	if err != nil || strings.TrimPrefix(db.Path, "/") != "loop_now_test" {
		t.Fatal("GitHub App requires dedicated TEST database")
	}
	rd, err := redis.ParseURL(redistest.URL(t))
	if err != nil || rd.DB != 10 {
		t.Fatal("GitHub App requires TEST Redis DB10")
	}
	w := newWorld(t)
	w.cfg.AuthLoginTimeout = 10 * time.Second
	w.cfg.AuthRecordTimeout = time.Second
	return w
}

func githubAppWait(t *testing.T, s *server, base string, id uuid.UUID, status string) cm.CLIAuthState {
	t.Helper()
	for until := time.Now().Add(6 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		state := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
		if state.Operation.Status == status {
			return state
		}
		if state.Operation.Status == "failed" || state.Operation.Status == "expired" {
			t.Fatalf("unexpected safe status=%s code=%v", state.Operation.Status, state.Operation.ErrorCode)
		}
	}
	t.Fatal("GitHub App operation timeout")
	return cm.CLIAuthState{}
}

func githubAppStart(t *testing.T, s *server, base string, target *cm.Account) uuid.UUID {
	t.Helper()
	id := uuid.New()
	in := cm.CLIAuthStart{OperationID: id, CLI: "gh", AuthType: "github_user"}
	if target != nil {
		in.AccountID, in.ExpectedRevision = &target.ID, &target.Revision
	}
	s.must(202, "POST", base, in)
	return id
}

func TestGitHubAppManagementCaptureAndRecovery(t *testing.T) {
	w := githubAppWorld(t)
	provider := githubtest.New(t)
	s := w.start(nil)
	defer func() { s.stop() }()
	s.a.CLIAuth.GitHub = provider.Client
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	base := "/projects/" + project.String() + "/cli-auth/operations"
	accounts := "/projects/" + project.String() + "/credential-accounts"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic model", "credential": map[string]string{"type": "api_key", "key": "synthetic-github-model"}})
	path, allowed := s.createAgent("GitHub allowed")
	_, denied := s.createAgent("GitHub not allowed")
	s.must(200, "PUT", path+"/clis/gh", nil)
	a := provider.Add("101", "alice", nil, "")
	a.Release()
	id := githubAppStart(t, s, base, nil)
	first := githubAppWait(t, s, base, id, "succeeded")
	if first.Account == nil || first.Account.Status != "logged_in" || first.Account.GitHubIdentity.UserID != "101" {
		t.Fatal("actual App did not save verified GitHub OAuth")
	}
	account := *first.Account
	impact := decode[cm.Impact](t, s.must(200, "GET", "/projects/"+project.String()+"/credential-account-impact?purpose=cli&cli=gh&accountId="+account.ID.String(), nil))
	if len(impact.Agents) != 1 || impact.Agents[0].AgentID != allowed.ID || impact.Agents[0].Mode != "automatic" || impact.Agents[0].ResolvedAccountID == nil || *impact.Agents[0].ResolvedAccountID != account.ID {
		t.Fatal("actual App impact did not reflect allowed automatic Agent")
	}
	before, err := s.a.Config.ExecutionSnapshot(ctx, allowed.ID)
	if err != nil || before.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-access-1" || before.GoogleBinding != nil {
		t.Fatal("GitHub execution capture used wrong credential path", err)
	}
	unallowed, err := s.a.Config.ExecutionSnapshot(ctx, denied.ID)
	if err != nil || len(unallowed.CLIPayloads["gh"].Env) != 0 {
		t.Fatal("unallowed Agent captured GitHub identity", err)
	}
	old, _, err := s.a.Store.GetProjectCLIVersion(ctx, account.ID, *account.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(old.Secret)
	account = decode[cm.Account](t, s.must(200, "PATCH", accounts+"/"+account.ID.String(), map[string]any{"name": "human remark", "expectedRevision": account.Revision}))
	b := provider.Add("202", "bob", nil, "")
	b.Release()
	secondID := githubAppStart(t, s, base, &account)
	second := githubAppWait(t, s, base, secondID, "succeeded")
	if second.Account == nil || second.Account.Name != "human remark" || second.Account.GitHubIdentity.UserID != "202" {
		t.Fatal("actual App blocked changing GitHub identity or overwrote remark")
	}
	after, err := s.a.Config.ExecutionSnapshot(ctx, allowed.ID)
	if err != nil || after.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-access-2" || before.CLIPayloads["gh"].Env["GH_TOKEN"] != "synthetic-access-1" || before.CredentialIdentity == after.CredentialIdentity {
		t.Fatal("next dispatch/current capture boundary failed", err)
	}
	old, _, err = s.a.Store.GetProjectCLIVersion(ctx, account.ID, *first.Account.CurrentVersionID)
	if err != nil || sha256.Sum256(old.Secret) != hash {
		t.Fatal("actual App rewrote old OAuth credential", err)
	}
	account = *second.Account
	failed := provider.Add("303", "not saved", nil, "access_denied")
	failed.Release()
	failedID := githubAppStart(t, s, base, &account)
	githubAppWait(t, s, base, failedID, "failed")
	current := decode[cm.Account](t, s.must(200, "GET", accounts+"/"+account.ID.String(), nil))
	if *current.CurrentVersionID != *account.CurrentVersionID || current.GitHubIdentity.UserID != "202" {
		t.Fatal("App denied authorization did not preserve saved version")
	}
	pat := decode[cm.Account](t, s.must(200, "PUT", accounts+"/"+account.ID.String()+"/credential", map[string]any{"expectedRevision": account.Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-pat"}}}))
	if pat.Status != "saved" || pat.GitHubIdentity != nil || pat.AccountLabel != nil {
		t.Fatal("PAT replacement retained current OAuth identity")
	}
	// Simulate a durable operation whose source worker did not survive. No
	// provider call is started for this row, and restart must not resume it.
	pendingID := uuid.New()
	if _, _, err = s.a.Store.NewProjectCLIAuthOperation(ctx, project, cm.CLIAuthStart{OperationID: pendingID, CLI: "gh", AuthType: "github_user"}, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Store.SetProjectGitHubAuthorization(ctx, project, pendingID, "https://github.com/login/device", "ABCD-1234", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	deviceCalls := provider.DeviceCalls.Load()
	s.stop()
	s = w.start(nil)
	recovered := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+pendingID.String(), nil))
	if recovered.Operation.Status != "failed" || recovered.Operation.ErrorCode == nil || *recovered.Operation.ErrorCode != "server_restarted" || recovered.Operation.UserCode != nil || recovered.Operation.AuthorizationURL != nil || provider.DeviceCalls.Load() != deviceCalls {
		t.Fatal("restart resumed device secret or retained interaction metadata")
	}
	stillPAT := decode[cm.Account](t, s.must(200, "GET", accounts+"/"+account.ID.String(), nil))
	if stillPAT.Status != "saved" || stillPAT.GitHubIdentity != nil || *stillPAT.CurrentVersionID != *pat.CurrentVersionID {
		t.Fatal("restart changed existing PAT")
	}
	var runs, turns, grants int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM google_cli_grants)`).Scan(&runs, &turns, &grants); err != nil || runs+turns+grants != 0 || provider.DeniedDials.Load() != 0 {
		t.Fatal("management flow triggered model/task/Google or attempted unapproved endpoint", err)
	}
	list := decode[cm.AccountList](t, s.must(200, "GET", accounts+"?purpose=cli&cli=gh", nil))
	if len(list.Accounts) != 1 || list.Accounts[0].Status != "saved" {
		t.Fatal("App safe account list failed")
	}
	// The response is safe metadata, not the captured secret payload.
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "synthetic-access-") || strings.Contains(string(raw), "synthetic-pat") {
		t.Fatal("App account response leaked credential")
	}
}

func TestGitHubAppDeletionEndsActualPendingRequest(t *testing.T) {
	w := githubAppWorld(t)
	provider := githubtest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.GitHub = provider.Client
	ctx := context.Background()
	for _, kind := range []string{"account", "project"} {
		t.Run(kind, func(t *testing.T) {
			project := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "GitHub delete " + kind}))
			projectPath := "/projects/" + project.ID.String()
			base := projectPath + "/cli-auth/operations"
			seed := provider.Add("101", "alice", nil, "")
			seed.Release()
			firstID := githubAppStart(t, s, base, nil)
			first := githubAppWait(t, s, base, firstID, "succeeded")
			if first.Account == nil {
				t.Fatal("saved account missing")
			}
			pending := provider.Add("202", "bob", nil, "")
			pendingID := githubAppStart(t, s, base, first.Account)
			select {
			case <-pending.Waiting:
			case <-time.After(3 * time.Second):
				t.Fatal("actual request not waiting")
			}
			if kind == "account" {
				s.must(200, "DELETE", projectPath+"/credential-accounts/"+first.Account.ID.String()+"?expectedRevision="+fmt.Sprint(first.Account.Revision), nil)
			} else {
				s.must(204, "DELETE", projectPath, nil)
			}
			select {
			case <-pending.Cancelled:
			case <-time.After(time.Second):
				t.Fatal("committed deletion did not terminate actual HTTP")
			}
			pending.Release()
			var status string
			var code, uri *string
			var versions, identities int
			if err := s.a.Store.Pool.QueryRow(ctx, `SELECT status,user_code,authorization_url FROM project_cli_auth_operations WHERE id=$1`, pendingID).Scan(&status, &code, &uri); err != nil || status != "cancelled" || code != nil || uri != nil {
				t.Fatal("deleted operation not terminal/cleared", err)
			}
			if err := s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_cli_versions WHERE account_id=$1),(SELECT count(*) FROM github_cli_identities WHERE account_id=$1)`, first.Account.ID).Scan(&versions, &identities); err != nil {
				t.Fatal(err)
			}
			if kind == "account" && (versions != 1 || identities != 1) {
				t.Fatal("account deletion lost old task-readable version/history")
			}
			if kind == "project" && (versions != 0 || identities != 0) {
				t.Fatal("project deletion retained credential/history")
			}
		})
	}
	if provider.DeniedDials.Load() != 0 {
		t.Fatal("unapproved endpoint attempted")
	}
}

func TestGitHubAppUnknownStartResponseAndCommittedRestart(t *testing.T) {
	w := githubAppWorld(t)
	provider := githubtest.New(t)
	s := w.start(nil)
	defer func() { s.stop() }()
	s.a.CLIAuth.GitHub = provider.Client
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	base := "/projects/" + project.String() + "/cli-auth/operations"
	attempt := provider.Add("101", "alice", nil, "")
	id := uuid.New()
	body, _ := json.Marshal(cm.CLIAuthStart{OperationID: id, CLI: "gh", AuthType: "github_user"})
	req, err := http.NewRequest("POST", s.url+base, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately discard the entire receipt payload. The caller cannot infer
	// whether authorization has succeeded; it must GET its original operation.
	if response.StatusCode != 202 {
		response.Body.Close()
		t.Fatal("start not accepted")
	}
	response.Body.Close()
	select {
	case <-attempt.Waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("original start did not reach owned provider")
	}
	pending := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
	if pending.Operation.ID != id || pending.Operation.Status != "awaiting_authorization" {
		t.Fatal("unknown response recovery changed original operation")
	}
	attempt.Release()
	saved := githubAppWait(t, s, base, id, "succeeded")
	if saved.Account == nil {
		t.Fatal("credential not committed")
	}
	originalVersion := *saved.Account.CurrentVersionID
	version, _, err := s.a.Store.GetProjectCLIVersion(ctx, saved.Account.ID, originalVersion)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(version.Secret)
	s.stop()
	s = w.start(nil)
	recovered := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
	if recovered.Operation.Status != "succeeded" || recovered.Operation.UserCode != nil || recovered.Operation.AuthorizationURL != nil || recovered.Account == nil || *recovered.Account.CurrentVersionID != originalVersion || recovered.Account.GitHubIdentity.UserID != "101" {
		t.Fatal("postcommit restart lost durable original result")
	}
	current, _, err := s.a.Store.GetProjectCLIVersion(ctx, saved.Account.ID, originalVersion)
	if err != nil || sha256.Sum256(current.Secret) != hash || provider.DeviceCalls.Load() != 1 || provider.UserCalls.Load() != 1 || provider.DeniedDials.Load() != 0 {
		t.Fatal("unknown/restart reissued authorization or rewrote saved credential", err)
	}
}
