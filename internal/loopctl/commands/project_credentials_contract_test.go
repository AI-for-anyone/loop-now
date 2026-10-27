package commands_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"loop-now/internal/loopctl/clitest"
)

const credentialProjectID = "0192f1a2-0000-7000-8000-0000000000aa"
const credentialAccountID = "0192f1a2-0000-7000-8000-0000000000ab"

func projectAccount(purpose string) map[string]any {
	a := map[string]any{"id": credentialAccountID, "projectId": credentialProjectID, "purpose": purpose, "provider": "anthropic", "cli": nil, "name": "fixture label", "accountLabel": nil, "revision": 1, "currentVersionId": actionUUID, "authMethod": "api_key", "status": "saved", "template": nil, "createdAt": "2026-10-15T00:00:00Z", "updatedAt": "2026-10-15T00:00:00Z", "deletedAt": nil}
	if purpose == "cli" {
		a["provider"] = nil
		a["cli"] = "gh"
		a["authMethod"] = nil
		a["template"] = map[string]any{"source": "builtin", "credentialFormat": "1", "fieldNames": []string{"token"}, "warnings": []any{}, "status": "available"}
	}
	return a
}
func projectSelections() map[string]any {
	return map[string]any{"agentId": agentUUID, "projectId": credentialProjectID, "configVersion": 1, "credentialsRevision": 1, "selections": []any{
		map[string]any{"purpose": "model", "provider": "anthropic", "cli": nil, "mode": "automatic", "selectedAccountId": nil, "resolvedAccountId": credentialAccountID, "resolvedVersionId": actionUUID, "status": "resolved", "candidates": []any{projectAccount("model")}},
		map[string]any{"purpose": "model", "provider": "openai-codex", "cli": nil, "mode": "legacy_cleared", "selectedAccountId": nil, "resolvedAccountId": nil, "resolvedVersionId": nil, "status": "not_configured", "candidates": []any{}},
	}}
}
func projectAuthState(status string) map[string]any {
	op := map[string]any{"id": actionUUID, "projectId": credentialProjectID, "provider": "openai-codex", "targetAccountId": nil, "targetRevision": nil, "name": "Codex fixture", "status": status, "accountId": nil, "startedAt": "2026-10-15T00:00:00Z", "expiresAt": "2026-10-15T00:10:00Z", "completedAt": nil, "authorizationUrl": nil, "errorCode": nil, "errorMessage": nil}
	if status == "awaiting_authorization" {
		op["authorizationUrl"] = "https://auth.openai.com/oauth/authorize?client_id=offline-fixture&state=fixture"
	}
	if status == "cancelled" {
		op["completedAt"] = "2026-10-15T00:01:00Z"
	}
	return map[string]any{"operation": op, "account": nil}
}
func projectCredentialSamples() map[string]clitest.Sample {
	ok := func(b any) clitest.Sample { return clitest.Sample{Status: 200, Body: b} }
	acct := projectAccount("model")
	return map[string]clitest.Sample{
		"GET /projects/{projectId}/credential-accounts":                         ok(map[string]any{"projectId": credentialProjectID, "credentialsRevision": 1, "accounts": []any{acct}}),
		"POST /projects/{projectId}/credential-accounts":                        {Status: 201, Body: acct},
		"GET /projects/{projectId}/credential-accounts/{accountId}":             ok(acct),
		"PATCH /projects/{projectId}/credential-accounts/{accountId}":           ok(acct),
		"DELETE /projects/{projectId}/credential-accounts/{accountId}":          ok(acct),
		"PUT /projects/{projectId}/credential-accounts/{accountId}/credential":  ok(acct),
		"GET /projects/{projectId}/credential-account-impact":                   ok(map[string]any{"projectId": credentialProjectID, "credentialsRevision": 1, "accountId": nil, "agents": []any{}}),
		"GET /agents/{agentId}/credential-selections":                           ok(projectSelections()),
		"PUT /agents/{agentId}/credential-selections":                           ok(projectSelections()),
		"POST /projects/{projectId}/model-auth/operations":                      {Status: 202, Body: projectAuthState("pending")},
		"GET /projects/{projectId}/model-auth/operations":                       ok(map[string]any{"operations": []any{projectAuthState("awaiting_authorization")["operation"]}}),
		"GET /projects/{projectId}/model-auth/operations/{operationId}":         ok(projectAuthState("awaiting_authorization")),
		"POST /projects/{projectId}/model-auth/operations/{operationId}/cancel": ok(projectAuthState("cancelled")),
	}
}
func credentialStub(t *testing.T) (*clitest.Stub, *env) {
	s, e := stub(t)
	for op, sample := range projectCredentialSamples() {
		s.On(op, sample)
	}
	return s, e
}

func TestProjectCredentialRequests(t *testing.T) {
	for _, purpose := range []string{"model", "cli"} {
		t.Run(purpose, func(t *testing.T) {
			group, locator, value, input := purpose+"-account", "provider", "anthropic", `{"type":"api_key","key":"synthetic-secret"}`
			if purpose == "cli" {
				locator, value, input = "cli", "gh", `{"type":"cli","fields":{"token":"synthetic-secret"}}`
			}
			s, e := credentialStub(t)
			e.stdin = input
			e.run("project", group, "create", credentialProjectID, "--name", "fixture label", "--"+locator, value, "--from-file", "-").ok(t)
			b := lastBody(t, s)
			if len(b) != 4 || b["purpose"] != purpose || b[locator] != value || b["name"] != "fixture label" {
				t.Fatalf("create body %v", b)
			}
			if _, ok := b["expectedRevision"]; ok {
				t.Fatal("create must not carry revision")
			}
			e.run("project", group, "replace", credentialProjectID, credentialAccountID, "--expected-revision", "7", "--from-file", writeFile(t, "cred.json", input), "--yes").ok(t)
			b = lastBody(t, s)
			if len(b) != 2 || b["expectedRevision"] != float64(7) {
				t.Fatalf("replace %v", b)
			}
			e.run("project", group, "rename", credentialProjectID, credentialAccountID, "--name", "new name", "--expected-revision", "8").ok(t)
			b = lastBody(t, s)
			if len(b) != 2 || b["name"] != "new name" {
				t.Fatalf("rename %v", b)
			}
			e.run("project", group, "delete", credentialProjectID, credentialAccountID, "--expected-revision", "9", "--yes").ok(t)
			reqs := s.Requests()
			req := reqs[len(reqs)-1]
			if req.RawQuery != "expectedRevision=9" || len(req.Body) != 0 {
				t.Fatalf("delete transport %s %s", req.RawQuery, req.Body)
			}
			e.run("project", group, "list", credentialProjectID, "--"+locator, value).ok(t)
			req = s.Requests()[len(s.Requests())-1]
			if !strings.Contains(req.RawQuery, "purpose="+purpose) || !strings.Contains(req.RawQuery, locator+"="+value) {
				t.Fatalf("filter %s", req.RawQuery)
			}
			e.run("project", group, "impact", credentialProjectID, "--"+locator, value, "--account-id", credentialAccountID).ok(t)
			req = s.Requests()[len(s.Requests())-1]
			if !strings.Contains(req.RawQuery, "accountId="+credentialAccountID) {
				t.Fatal("impact account missing")
			}
		})
	}
}
func TestProjectCredentialSelectionSingleItem(t *testing.T) {
	for _, locator := range []string{"provider", "cli"} {
		for _, action := range []string{"set", "clear"} {
			t.Run(locator+"/"+action, func(t *testing.T) {
				s, e := credentialStub(t)
				value, purpose := "anthropic", "model"
				if locator == "cli" {
					value, purpose = "gh", "cli"
				}
				args := []string{"agent", "credential-selection", action, agentUUID, "--" + locator, value, "--expected-config-version", "3"}
				var account any
				if action == "set" {
					args = append(args, "--account-id", credentialAccountID)
					account = credentialAccountID
				} else {
					args = append(args, "--yes")
				}
				data := e.run(args...).ok(t)
				b := lastBody(t, s)
				if !reflect.DeepEqual(b, map[string]any{"purpose": purpose, locator: value, "accountId": account, "expectedConfigVersion": float64(3)}) {
					t.Fatalf("single item %v", b)
				}
				if len(s.Requests()) != 1 {
					t.Fatal("selection must not GET revision or replace collection")
				}
				if data["selections"].([]any)[1].(map[string]any)["mode"] != "legacy_cleared" {
					t.Fatal("mode incorrectly normalized")
				}
			})
		}
	}
}
func TestProjectCredentialOAuthRequests(t *testing.T) {
	s, e := credentialStub(t)
	e.run("project", "model-account", "login", credentialProjectID, "--provider", "openai-codex", "--name", "new account").ok(t)
	if b := lastBody(t, s); len(b) != 2 || b["name"] != "new account" {
		t.Fatal(b)
	}
	e.run("project", "model-account", "login", credentialProjectID, "--provider", "openai-codex", "--account-id", credentialAccountID, "--expected-revision", "4", "--yes").ok(t)
	if b := lastBody(t, s); len(b) != 3 || b["accountId"] != credentialAccountID || b["expectedRevision"] != float64(4) {
		t.Fatal(b)
	}
	e.run("project", "model-account", "login-list", credentialProjectID, "--provider", "openai-codex").ok(t)
	e.run("project", "model-account", "login-status", credentialProjectID, actionUUID).ok(t)
	e.run("project", "model-account", "login-cancel", credentialProjectID, actionUUID, "--yes").ok(t)
	req := s.Requests()[len(s.Requests())-1]
	if len(req.Body) != 0 {
		t.Fatal("cancel must have no body")
	}
	if len(s.Requests()) != 5 {
		t.Fatal("must not automatically poll/login/authorize")
	}
}
func TestProjectCredentialErrorDetails(t *testing.T) {
	for _, tc := range []struct {
		status  int
		code    string
		exit    int
		details any
	}{
		{412, "precondition_failed", 8, map[string]any{"resourceKind": "agent", "resourceId": agentUUID, "expectedVersion": 1, "currentVersion": 2}},
		{409, "project_credential_required", 8, map[string]any{"agentId": agentUUID, "projectId": credentialProjectID}},
		{409, "credential_account_deleted", 8, map[string]any{"projectId": credentialProjectID, "accountId": credentialAccountID}},
		{409, "credential_selection_required", 8, map[string]any{"agentId": agentUUID, "projectId": credentialProjectID, "purpose": "model", "provider": "anthropic", "cli": nil, "accountId": nil, "candidateAccountIds": []string{credentialAccountID}}},
		{422, "credential_scope_mismatch", 9, map[string]any{"agentId": agentUUID, "projectId": credentialProjectID, "purpose": "cli", "provider": nil, "cli": "gh", "accountId": credentialAccountID, "candidateAccountIds": []string{}}},
		{409, "model_auth_in_progress", 8, map[string]any{"projectId": credentialProjectID, "operationId": actionUUID}},
		{403, "forbidden", 6, nil},
		{401, "unauthenticated", 5, nil},
		{404, "not_found", 7, nil},
		{422, "validation_failed", 9, nil},
	} {
		t.Run(tc.code, func(t *testing.T) {
			s, e := credentialStub(t)
			body := map[string]any{"code": tc.code, "message": "安全错误"}
			if tc.details != nil {
				body["details"] = tc.details
			}
			s.On("PUT /agents/{agentId}/credential-selections", clitest.Sample{Status: tc.status, Body: map[string]any{"error": body}})
			r := e.run("agent", "credential-selection", "set", agentUUID, "--provider", "anthropic", "--account-id", credentialAccountID, "--expected-config-version", "1")
			r.fails(t, tc.code, tc.exit)
			if len(s.Requests()) != 1 {
				t.Fatal("failure must not reread or retry")
			}
			var got any
			if len(r.Env.Error.Details) > 0 {
				_ = json.Unmarshal(r.Env.Error.Details, &got)
			}
			want, _ := json.Marshal(tc.details)
			actual, _ := json.Marshal(got)
			if string(want) != string(actual) {
				t.Fatalf("details %s != %s", actual, want)
			}
		})
	}
}

func TestProjectCredentialSelectionStatesTransparent(t *testing.T) {
	for _, tc := range []struct {
		mode, status string
		count        int
	}{
		{"automatic", "not_configured", 0}, {"automatic", "resolved", 1}, {"automatic", "selection_required", 2},
		{"explicit", "account_deleted", 0}, {"explicit", "unavailable", 1}, {"explicit", "requires_login", 1},
		{"legacy_cleared", "not_configured", 1},
	} {
		t.Run(tc.mode+"/"+tc.status, func(t *testing.T) {
			s, e := credentialStub(t)
			view := projectSelections()
			selection := view["selections"].([]any)[0].(map[string]any)
			selection["mode"], selection["status"] = tc.mode, tc.status
			selection["selectedAccountId"], selection["resolvedAccountId"], selection["resolvedVersionId"] = nil, nil, nil
			if tc.mode == "explicit" {
				selection["selectedAccountId"] = credentialAccountID
			}
			if tc.status == "resolved" {
				selection["resolvedAccountId"], selection["resolvedVersionId"] = credentialAccountID, actionUUID
			}
			candidates := []any{}
			for i := 0; i < tc.count; i++ {
				a := projectAccount("model")
				if i > 0 {
					a["id"] = actionUUID
					a["status"] = "unavailable"
				}
				candidates = append(candidates, a)
			}
			selection["candidates"] = candidates
			s.On("GET /agents/{agentId}/credential-selections", clitest.Sample{Status: 200, Body: view})
			got := e.run("agent", "credential-selection", "get", agentUUID).ok(t)
			b, _ := json.Marshal(view)
			actual, _ := json.Marshal(got)
			if string(b) != string(actual) {
				t.Fatal("CLI filtered candidates, changed null/mode, or inferred fallback")
			}
		})
	}
}

func TestProjectCredentialOAuthStatesTransparent(t *testing.T) {
	for _, status := range []string{"pending", "awaiting_authorization", "succeeded", "failed", "cancelled", "expired"} {
		t.Run(status, func(t *testing.T) {
			s, e := credentialStub(t)
			state := projectAuthState(status)
			op := state["operation"].(map[string]any)
			if status != "pending" && status != "awaiting_authorization" {
				op["completedAt"] = "2026-10-15T00:01:00Z"
				op["authorizationUrl"] = nil
			}
			if status == "succeeded" || status == "failed" {
				state["account"] = projectAccount("model")
				op["targetAccountId"] = credentialAccountID
				op["targetRevision"] = 1
				op["name"] = nil
			}
			if status == "succeeded" {
				op["accountId"] = credentialAccountID
			}
			if status == "failed" {
				op["errorCode"] = "auth_failed"
				op["errorMessage"] = "授权失败"
			}
			s.On("GET /projects/{projectId}/model-auth/operations/{operationId}", clitest.Sample{Status: 200, Body: state})
			got := e.run("project", "model-account", "login-status", credentialProjectID, actionUUID).ok(t)
			b, _ := json.Marshal(state)
			actual, _ := json.Marshal(got)
			if string(b) != string(actual) {
				t.Fatal("CLI rewrote operation/account state")
			}
			if len(s.Requests()) != 1 {
				t.Fatal("status must not automatically poll or start login")
			}
		})
	}
}
