package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/cliauth"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/githubauth/githubtest"
)

func TestGitHubLoginHTTPActualWorkerAndContract(t *testing.T) {
	e := newEnv(t)
	provider := githubtest.New(t)
	var logs bytes.Buffer
	service := cliauth.NewService(e.st, nil, nil, cliauth.ServiceOptions{LoginTimeout: 10 * time.Second, RecordTimeout: time.Second}, slog.New(slog.NewTextHandler(&logs, nil)))
	service.GitHub = provider.Client
	service.Account = e.api.ProjectCredentials.Get
	e.api.CLIAuth = service
	e.api.ProjectCredentials.CancelCLILogins = service.CancelLogins
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	base := "/projects/" + e.dc.Project.ID.String() + "/cli-auth/operations"
	accounts := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts"
	_, externalToken := e.identity("github-admin")
	external := e.external(externalToken)
	start := func(id uuid.UUID) map[string]any {
		return map[string]any{"operationId": id, "cli": "gh", "authType": "github_user"}
	}
	waitState := func(id uuid.UUID, status string) cm.CLIAuthState {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
			out := decode[cm.CLIAuthState](t, e.expect(200, "GET", base+"/"+id.String(), nil))
			if out.Operation.Status == status {
				return out
			}
			if out.Operation.Status == "failed" || out.Operation.Status == "expired" {
				t.Fatalf("unexpected safe operation status=%s code=%v", out.Operation.Status, out.Operation.ErrorCode)
			}
		}
		t.Fatal("operation did not reach expected state")
		return cm.CLIAuthState{}
	}
	first := provider.Add("1001", "alice", nil, "")
	id := uuid.New()
	e.expect(202, "POST", base, start(id))
	awaiting := waitState(id, "awaiting_authorization")
	if awaiting.Operation.UserCode == nil || awaiting.Operation.AuthorizationURL == nil {
		t.Fatal("real worker did not publish required authorization pair")
	}
	external.expect(202, "POST", base, start(id))
	e.console().expectError(409, "cli_auth_in_progress", "POST", base, start(uuid.New()))
	for _, key := range []string{"name", "accountId", "expectedRevision", "host", "clientId", "scopes", "token"} {
		bad := start(uuid.New())
		bad[key] = nil
		e.console().expectError(422, "validation_failed", "POST", base, bad, "X-Contract-Invalid-Request", "1")
	}
	e.console().expectError(422, "validation_failed", "GET", base+"?cli=", nil, "X-Contract-Invalid-Request", "1")
	e.console().expectError(422, "validation_failed", "GET", base+"?cli=gh&cli=gh", nil, "X-Contract-Invalid-Request", "1")
	gh := decode[cm.CLIAuthOperations](t, external.expect(200, "GET", base+"?cli=gh", nil))
	google := decode[cm.CLIAuthOperations](t, e.expect(200, "GET", base, nil))
	if len(gh.Operations) != 1 || gh.Operations[0].ID != id || len(google.Operations) != 0 || provider.DeviceCalls.Load() != 1 {
		t.Fatal("CLI isolation, invalid-input fence or stable-ID behavior failed")
	}
	e.external("").expectError(401, "unauthenticated", "POST", base, start(uuid.New()))
	agent := e.platformAgent("GitHub boundary")
	caller := e.agentCaller(agent.ID)
	e.console().expectError(403, "forbidden", "POST", base, start(uuid.New()), "Authorization", "Bearer "+caller.token)
	caller.client = &http.Client{}
	caller.expectError(404, "not_found", "POST", base, start(uuid.New()))
	e.console().expectError(403, "forbidden_origin", "POST", base, start(uuid.New()), "Origin", "https://foreign.invalid")
	requestBody, _ := json.Marshal(start(uuid.New()))
	hostRequest, _ := http.NewRequest("POST", e.srv.URL+"/api/v1"+base, bytes.NewReader(requestBody))
	hostRequest.Host = "foreign.invalid"
	hostRequest.Header.Set("Content-Type", "application/json")
	hostResponse, err := e.client.Do(hostRequest)
	if err != nil {
		t.Fatal(err)
	}
	hostBody, _ := io.ReadAll(hostResponse.Body)
	hostResponse.Body.Close()
	if hostResponse.StatusCode != 403 || decode[apitypes.ErrorBody](t, hostBody).Error.Code != "invalid_host" {
		t.Fatal("Host guard did not reject before login")
	}
	first.Release()
	success := waitState(id, "succeeded")
	if success.Account == nil || success.Account.Status != "logged_in" || success.Account.Name != "alice" || success.Account.GitHubIdentity == nil || success.Account.GitHubIdentity.UserID != "1001" || success.Operation.UserCode != nil || success.Operation.AuthorizationURL != nil {
		t.Fatal("successful operation projected stale/incorrect account")
	}
	account := *success.Account
	view := decode[cm.Account](t, external.expect(200, "GET", accounts+"/"+account.ID.String(), nil))
	if view.Status != "logged_in" || view.GitHubIdentity == nil {
		t.Fatal("GitHub OAuth was decoded as a Google credential")
	}
	remark := "keep human remark"
	account = decode[cm.Account](t, e.expect(200, "PATCH", accounts+"/"+account.ID.String(), map[string]any{"name": remark, "expectedRevision": account.Revision}))
	relogin := func(id uuid.UUID, revision int64) map[string]any {
		b := start(id)
		b["accountId"], b["expectedRevision"] = account.ID, revision
		return b
	}
	e.console().expectError(412, "precondition_failed", "POST", base, relogin(uuid.New(), account.Revision-1))
	second := provider.Add("2002", "bob", nil, "")
	secondID := uuid.New()
	e.expect(202, "POST", base, relogin(secondID, account.Revision))
	waitState(secondID, "awaiting_authorization")
	second.Release()
	replaced := waitState(secondID, "succeeded")
	if replaced.Account == nil || replaced.Account.ID != account.ID || replaced.Account.Name != remark || replaced.Account.GitHubIdentity.UserID != "2002" || *replaced.Account.CurrentVersionID == *account.CurrentVersionID {
		t.Fatal("different-user re-login failed or overwrote remark")
	}
	account = *replaced.Account
	denied := provider.Add("3003", "uncommitted", nil, "access_denied")
	deniedID := uuid.New()
	e.expect(202, "POST", base, relogin(deniedID, account.Revision))
	waitState(deniedID, "awaiting_authorization")
	denied.Release()
	failure := waitState(deniedID, "failed")
	if failure.Operation.UserCode != nil || failure.Operation.AuthorizationURL != nil || failure.Operation.ErrorCode == nil || *failure.Operation.ErrorCode != "oauth_failed" {
		t.Fatal("denied operation did not safely terminate")
	}
	current := decode[cm.Account](t, e.expect(200, "GET", accounts+"/"+account.ID.String(), nil))
	if *current.CurrentVersionID != *account.CurrentVersionID || current.GitHubIdentity.UserID != "2002" {
		t.Fatal("failed re-login replaced old account")
	}
	cancelAttempt := provider.Add("4004", "cancelled", nil, "")
	cancelID := uuid.New()
	e.expect(202, "POST", base, relogin(cancelID, account.Revision))
	waitState(cancelID, "awaiting_authorization")
	select {
	case <-cancelAttempt.Waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("actual pending HTTP request did not start")
	}
	cancelled := decode[cm.CLIAuthState](t, e.expect(200, "POST", base+"/"+cancelID.String()+"/cancel", nil))
	if cancelled.Operation.Status != "cancelled" || cancelled.Operation.UserCode != nil || cancelled.Operation.AuthorizationURL != nil {
		t.Fatal("cancel did not clear authorization metadata")
	}
	e.expect(200, "POST", base+"/"+cancelID.String()+"/cancel", nil)
	cancelAttempt.Release()
	active := decode[cm.CLIAuthOperations](t, e.expect(200, "GET", base+"?cli=gh", nil))
	if len(active.Operations) != 0 || provider.DeniedDials.Load() != 0 {
		t.Fatal("terminal activity remained or provider endpoint escaped fixture")
	}
	// Wait for all workers before sampling the non-concurrent log buffer.
	if err := service.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ABCD-1234", "synthetic-access-", "synthetic-device-", "synthetic-provider-message"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("provider secret or raw message reached ordinary logs")
		}
	}
}
