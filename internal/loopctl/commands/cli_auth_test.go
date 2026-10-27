package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

func cliAuthState(status string) map[string]any {
	op := map[string]any{"id": actionUUID, "projectId": credentialProjectID, "cli": "gcloud", "authType": "google_user", "targetAccountId": nil, "targetRevision": nil, "name": "Google fixture", "status": status, "accountId": nil, "startedAt": "2026-10-15T00:00:00Z", "expiresAt": "2026-10-15T00:10:00Z", "completedAt": nil, "authorizationUrl": nil, "errorCode": nil, "errorMessage": nil}
	var account any
	if status == "awaiting_authorization" {
		op["authorizationUrl"] = "https://accounts.google.com/o/oauth2/auth?client_id=offline-fixture&state=fixture"
	} else if status != "pending" {
		op["completedAt"] = "2026-10-15T00:01:00Z"
		if status == "succeeded" {
			op["accountId"] = credentialAccountID
			a := projectAccount("cli")
			a["cli"], a["authMethod"] = "gcloud", "oauth"
			account = a
		} else if status == "failed" {
			op["errorCode"], op["errorMessage"] = "oauth_failed", "安全失败"
		}
	}
	return map[string]any{"operation": op, "account": account}
}

func cliAuthSamples() map[string]clitest.Sample {
	return map[string]clitest.Sample{
		"POST /projects/{projectId}/cli-auth/operations":                      {Status: 202, Body: cliAuthState("pending")},
		"GET /projects/{projectId}/cli-auth/operations":                       {Status: 200, Body: map[string]any{"operations": []any{cliAuthState("awaiting_authorization")["operation"]}}},
		"GET /projects/{projectId}/cli-auth/operations/{operationId}":         {Status: 200, Body: cliAuthState("awaiting_authorization")},
		"POST /projects/{projectId}/cli-auth/operations/{operationId}/cancel": {Status: 200, Body: cliAuthState("cancelled")},
	}
}

func cliAuthStub(t *testing.T) (*clitest.Stub, *env) {
	s, e := stub(t)
	for op, smp := range cliAuthSamples() {
		s.On(op, smp)
	}
	return s, e
}

func TestCLIAuthRequests(t *testing.T) {
	s, e := cliAuthStub(t)
	e.run("project", "cli-account", "login", credentialProjectID, "--cli", "gcloud", "--operation-id", actionUUID, "--name", "Google fixture").ok(t)
	b := lastBody(t, s)
	if len(b) != 4 || b["operationId"] != actionUUID || b["cli"] != "gcloud" || b["authType"] != "google_user" || b["name"] != "Google fixture" {
		t.Fatalf("new login body: %v", b)
	}
	e.run("project", "cli-account", "login", credentialProjectID, "--cli", "gcloud", "--operation-id", actionUUID, "--account-id", credentialAccountID, "--expected-revision", "7", "--yes").ok(t)
	b = lastBody(t, s)
	if len(b) != 5 || b["expectedRevision"] != float64(7) || b["accountId"] != credentialAccountID {
		t.Fatalf("relogin body: %v", b)
	}
	e.run("project", "cli-account", "login-list", credentialProjectID).ok(t)
	e.run("project", "cli-account", "login-list", credentialProjectID, "--account-id", credentialAccountID).ok(t)
	e.run("project", "cli-account", "login-status", credentialProjectID, actionUUID).ok(t)
	e.run("project", "cli-account", "login-cancel", credentialProjectID, actionUUID, "--yes").ok(t)
	reqs := s.Requests()
	if len(reqs) != 6 || reqs[2].RawQuery != "" || reqs[3].RawQuery != "accountId="+credentialAccountID || len(reqs[5].Body) != 0 {
		t.Fatalf("no implicit GET/poll/extra query/cancel body: %+v", reqs)
	}
}

func TestCLIAuthInvalidZeroDial(t *testing.T) {
	base := []string{"project", "cli-account", "login", "named-project", "--cli", "gcloud"}
	for _, flags := range [][]string{
		{"--name", "label"},
		{"--operation-id", "bad", "--name", "label"},
		{"--operation-id", "00000000-0000-0000-0000-000000000000", "--name", "label"},
		{"--operation-id", actionUUID, "--name", " "},
		{"--operation-id", actionUUID, "--name", strings.Repeat("字", 201)},
		{"--operation-id", actionUUID, "--name", "label", "--expected-revision", "1"},
		{"--operation-id", actionUUID, "--name", "label", "--account-id", credentialAccountID, "--expected-revision", "1"},
		{"--operation-id", actionUUID, "--account-id", credentialAccountID},
		{"--operation-id", actionUUID, "--account-id", credentialAccountID, "--expected-revision", "1"},
		{"--operation-id", actionUUID, "--account-id", "label", "--expected-revision", "1", "--yes"},
		{"--operation-id", actionUUID, "--account-id", credentialAccountID, "--expected-revision", "0", "--yes"},
		{"--operation-id", actionUUID, "--name", "label", "--code", "synthetic-secret"},
		{"--operation-id", actionUUID, "--name", "label", "--from-file", "-"},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			s, e := cliAuthStub(t)
			e.block = true
			r := e.run(append(append([]string{}, base...), flags...)...)
			if r.Code != 2 || r.Env.Error == nil || e.dialCount() != 0 || len(s.Requests()) != 0 || strings.Contains(r.Stdout, "synthetic-secret") {
				t.Fatalf("invalid input must fail locally without secrets: %s", r.Stdout)
			}
		})
	}
	for _, args := range [][]string{
		{"login-status", "named-project", "display-name"},
		{"login-cancel", "named-project", "00000000-0000-0000-0000-000000000000", "--yes"},
		{"login-list", "named-project", "--account-id", "label"},
		{"login-list", "named-project", "--provider", "openai-codex"},
		{"login", "named-project", "--cli", "psql", "--operation-id", actionUUID, "--name", "label"},
	} {
		s, e := cliAuthStub(t)
		e.block = true
		e.run(append([]string{"project", "cli-account"}, args...)...).fails(t, output.CodeInvalidArgument, 2)
		if e.dialCount() != 0 || len(s.Requests()) != 0 {
			t.Fatal("invalid operation resolved project")
		}
	}
}

func TestCLIAuthStatesAndDetails(t *testing.T) {
	for _, status := range []string{"pending", "awaiting_authorization", "succeeded", "failed", "cancelled", "expired"} {
		s, e := cliAuthStub(t)
		want := cliAuthState(status)
		s.On("GET /projects/{projectId}/cli-auth/operations/{operationId}", clitest.Sample{Status: 200, Body: want})
		r := e.run("project", "cli-account", "login-status", credentialProjectID, actionUUID)
		r.ok(t)
		var got any
		_ = json.Unmarshal(r.Env.Data, &got)
		b, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(b) != string(w) || len(s.Requests()) != 1 {
			t.Fatalf("state changed or automatically polled: %s", r.Stdout)
		}
		if status == "succeeded" || status == "failed" || status == "cancelled" || status == "expired" {
			if got.(map[string]any)["operation"].(map[string]any)["authorizationUrl"] != nil {
				t.Fatal("terminal URL must be null")
			}
		}
	}
	for _, tc := range []struct {
		status  int
		code    string
		details map[string]any
	}{
		{409, "cli_auth_in_progress", map[string]any{"projectId": credentialProjectID, "operationId": actionUUID, "cli": "gcloud"}},
		{412, "precondition_failed", map[string]any{"resourceKind": "account", "resourceId": credentialAccountID, "expectedVersion": 1, "currentVersion": 2}},
	} {
		s, e := cliAuthStub(t)
		s.On("POST /projects/{projectId}/cli-auth/operations", clitest.Sample{Status: tc.status, Body: map[string]any{"error": map[string]any{"code": tc.code, "message": "安全错误", "details": tc.details}}})
		r := e.run("project", "cli-account", "login", credentialProjectID, "--cli", "gcloud", "--operation-id", actionUUID, "--account-id", credentialAccountID, "--expected-revision", "1", "--yes")
		r.fails(t, tc.code, 8)
		var got map[string]any
		_ = json.Unmarshal(r.Env.Error.Details, &got)
		w, _ := json.Marshal(tc.details)
		var want map[string]any
		_ = json.Unmarshal(w, &want)
		if !reflect.DeepEqual(got, want) || len(s.Requests()) != 1 {
			t.Fatal("CAS/conflict modified details or read/retried")
		}
	}
}

func TestCLIAuthUnknownWriteNotRetried(t *testing.T) {
	for _, action := range []string{"login", "login-cancel"} {
		t.Run(action, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				_, _ = io.ReadAll(r.Body)
				c, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = c.Close()
			}))
			defer srv.Close()
			e := newEnv(t)
			e.vars[conn.EnvURL], e.vars[conn.EnvToken] = srv.URL, extToken
			e.vars[conn.EnvCAFile], e.vars[conn.EnvRetryMax] = writeFile(t, "ca.pem", string(pemCert(srv))), "3"
			args := []string{"project", "cli-account", action, credentialProjectID}
			if action == "login" {
				args = append(args, "--cli", "gcloud", "--operation-id", actionUUID, "--name", "label")
			} else {
				args = append(args, actionUUID, "--yes")
			}
			r := e.run(args...)
			if r.Code == 0 || count.Load() != 1 || !strings.Contains(r.Stdout, "写入结果未知") || !strings.Contains(r.Stdout, "login-status") {
				t.Fatalf("unknown POST replayed: calls=%d %s", count.Load(), r.Stdout)
			}
		})
	}
}
