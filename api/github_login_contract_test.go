package api_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"strings"
	"testing"
)

// Schema examples only: no handler, database, helper or provider is exercised.
func TestGitHubLoginFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, v any, valid bool) {
		t.Helper()
		err := doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (err == nil) != valid {
			t.Fatalf("%s valid=%t: %v", name, valid, err)
		}
	}
	clone := func(v map[string]any) map[string]any {
		out := map[string]any{}
		for k, x := range v {
			out[k] = x
		}
		return out
	}
	id, project, account, version := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	newGH := map[string]any{"operationId": id, "cli": "gh", "authType": "github_user"}
	relogin := clone(newGH)
	relogin["accountId"] = account
	relogin["expectedRevision"] = float64(2)
	t.Run("start_four_strict_branches", func(t *testing.T) {
		check("CLIAuthStart", newGH, true)
		check("CLIAuthStart", relogin, true)
		check("CLIAuthStart", map[string]any{"operationId": id, "cli": "gcloud", "authType": "google_user", "name": "Google"}, true)
		check("CLIAuthStart", map[string]any{"operationId": id, "cli": "gcloud", "authType": "google_user", "accountId": account, "expectedRevision": float64(1)}, true)
		for _, field := range []string{"name", "accountId", "expectedRevision", "host", "scopes", "token", "credential"} {
			bad := clone(newGH)
			bad[field] = nil
			check("CLIAuthStart", bad, false)
		}
		for _, field := range []string{"operationId", "cli", "authType"} {
			bad := clone(newGH)
			bad[field] = nil
			check("CLIAuthStart", bad, false)
			delete(bad, field)
			check("CLIAuthStart", bad, false)
		}
		for _, field := range []string{"accountId", "expectedRevision"} {
			bad := clone(relogin)
			bad[field] = nil
			check("CLIAuthStart", bad, false)
			delete(bad, field)
			check("CLIAuthStart", bad, false)
		}
		bad := clone(newGH)
		bad["operationId"] = uuid.Nil.String()
		check("CLIAuthStart", bad, false)
		bad = clone(newGH)
		bad["authType"] = "google_user"
		check("CLIAuthStart", bad, false)
		bad = clone(relogin)
		bad["name"] = "must not rename"
		check("CLIAuthStart", bad, false)
	})
	now := "2026-10-18T09:00:00Z"
	op := map[string]any{"id": id, "projectId": project, "cli": "gh", "authType": "github_user", "targetAccountId": nil, "targetRevision": nil, "name": nil, "status": "pending", "accountId": nil, "startedAt": now, "expiresAt": "2026-10-18T09:05:00Z", "completedAt": nil, "authorizationUrl": nil, "userCode": nil, "errorCode": nil, "errorMessage": nil}
	t.Run("operation_pair_and_terminal_nulls", func(t *testing.T) {
		check("CLIAuthState", map[string]any{"operation": op, "account": nil}, true)
		bad := clone(op)
		delete(bad, "userCode")
		check("CLIAuthOperation", bad, false)
		bad = clone(op)
		bad["userCode"] = "ABCD-1234"
		check("CLIAuthOperation", bad, false)
		awaiting := clone(op)
		awaiting["status"] = "awaiting_authorization"
		awaiting["authorizationUrl"] = "https://github.com/login/device"
		awaiting["userCode"] = "ABCD-1234"
		check("CLIAuthOperation", awaiting, true)
		for _, url := range []any{nil, "https://github.com/login/device?code=ABCD-1234", "https://github.com.evil.test/login/device", "http://github.com/login/device", "https://github.com/login/device#fragment"} {
			bad = clone(awaiting)
			bad["authorizationUrl"] = url
			check("CLIAuthOperation", bad, false)
		}
		for _, code := range []any{nil, "abcd-1234", "ABCD1234", "ABCD-1234-secret"} {
			bad = clone(awaiting)
			bad["userCode"] = code
			check("CLIAuthOperation", bad, false)
		}
		for _, status := range []string{"succeeded", "failed", "cancelled", "expired"} {
			end := clone(op)
			end["status"] = status
			end["completedAt"] = now
			if status == "succeeded" {
				end["accountId"] = account
			} else {
				end["errorCode"] = "oauth_failed"
				end["errorMessage"] = "固定安全错误"
			}
			check("CLIAuthOperation", end, true)
			bad = clone(end)
			bad["userCode"] = "ABCD-1234"
			check("CLIAuthOperation", bad, false)
			bad = clone(end)
			bad["authorizationUrl"] = "https://github.com/login/device"
			check("CLIAuthOperation", bad, false)
			bad = clone(end)
			bad["completedAt"] = nil
			check("CLIAuthOperation", bad, false)
		}
		bad = clone(op)
		bad["targetAccountId"] = account
		check("CLIAuthOperation", bad, false)
		bad["targetRevision"] = float64(2)
		check("CLIAuthOperation", bad, true)
		google := clone(op)
		delete(google, "userCode")
		google["cli"] = "gcloud"
		google["authType"] = "google_user"
		google["name"] = "Google"
		check("CLIAuthOperation", google, true)
		google["userCode"] = nil
		check("CLIAuthOperation", google, false)
	})
	identity := map[string]any{"host": "github.com", "userId": "12345678901234567890", "login": "fixture-user", "grantedScopes": nil, "verifiedAt": now}
	acct := map[string]any{"id": account, "projectId": project, "purpose": "cli", "provider": nil, "cli": "gh", "name": "备注", "accountLabel": "fixture-user", "revision": float64(2), "currentVersionId": version, "authMethod": "oauth", "status": "logged_in", "template": nil, "createdAt": now, "updatedAt": now, "deletedAt": nil, "githubIdentity": identity}
	t.Run("version_identity_and_pat_compatibility", func(t *testing.T) {
		for _, scopes := range []any{nil, []any{}, []any{"repo", "read:org"}} {
			v := clone(identity)
			v["grantedScopes"] = scopes
			check("GitHubIdentity", v, true)
		}
		for _, field := range []string{"host", "userId", "login", "grantedScopes", "verifiedAt"} {
			bad := clone(identity)
			delete(bad, field)
			check("GitHubIdentity", bad, false)
		}
		for _, uid := range []any{nil, float64(1), "0", "-1", "1.5"} {
			bad := clone(identity)
			bad["userId"] = uid
			check("GitHubIdentity", bad, false)
		}
		check("ProjectCredentialAccount", acct, true)
		// Contract examples only; transactional replacement is verified by Store tests later.
		replacementIdentity := clone(identity)
		replacementIdentity["userId"] = "987654321"
		replacementIdentity["login"] = "another-user"
		replacement := clone(acct)
		replacement["githubIdentity"] = replacementIdentity
		replacement["currentVersionId"] = uuid.NewString()
		replacement["accountLabel"] = "another-user"
		check("ProjectCredentialAccount", replacement, true)
		if replacement["name"] != acct["name"] {
			t.Fatal("replacement example changed remark")
		}
		pat := clone(acct)
		pat["authMethod"] = nil
		pat["status"] = "saved"
		pat["accountLabel"] = nil
		pat["githubIdentity"] = nil
		check("ProjectCredentialAccount", pat, true)
		pat["githubIdentity"] = identity
		check("ProjectCredentialAccount", pat, false)
		delete(pat, "githubIdentity")
		check("ProjectCredentialAccount", pat, true)
		bad := clone(acct)
		bad["cli"] = "gcloud"
		check("ProjectCredentialAccount", bad, false)
		bad = clone(acct)
		bad["currentVersionId"] = nil
		check("ProjectCredentialAccount", bad, false)
	})
	t.Run("errors_and_route_classification", func(t *testing.T) {
		check("Error", map[string]any{"error": map[string]any{"code": "cli_auth_in_progress", "message": "固定安全错误", "details": map[string]any{"projectId": project, "operationId": id, "cli": "gh"}}}, true)
		check("Error", map[string]any{"error": map[string]any{"code": "precondition_failed", "message": "版本已变化", "details": map[string]any{"resourceKind": "account", "resourceId": account, "expectedVersion": float64(1), "currentVersion": float64(2)}}}, true)
		found := 0
		for path, item := range doc.Paths.Map() {
			for _, operation := range item.Operations() {
				if !strings.Contains(operation.OperationID, "ProjectCLIAuthOperation") {
					continue
				}
				found++
				if operation.Extensions["x-access"] != "console-and-external" || agent.Paths.Value(path) != nil {
					t.Fatal("management route access", path)
				}
				for _, status := range []string{"401", "403", "404", "409", "422", "503"} {
					if operation.Responses.Value(status) == nil {
						t.Fatal("missing error", status)
					}
				}
				if operation.OperationID == "startProjectCLIAuthOperation" && operation.Responses.Value("412") == nil {
					t.Fatal("missing CAS response")
				}
			}
		}
		if found != 4 {
			t.Fatalf("management operations=%d", found)
		}
		list := doc.Paths.Value("/projects/{projectId}/cli-auth/operations").Get
		for _, rule := range []string{"POST前", "项目+CLI", "仅GET原ID", "已终态", "不自动新建", "不重试POST", "不换ID", "仅活动"} {
			if !strings.Contains(list.Description, rule) {
				t.Fatalf("missing unknown-start contract: %s", rule)
			}
		}
		cli := false
		for _, p := range list.Parameters {
			if p.Value.Name == "cli" {
				cli = true
				if p.Value.Schema.Value.Default != "gcloud" {
					t.Fatal("legacy list default")
				}
			}
		}
		if !cli {
			t.Fatal("missing cli filter")
		}
	})
}
