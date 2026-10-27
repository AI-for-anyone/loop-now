package api_test

import (
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/contracttest"
	"testing"
)

func TestProjectCredentialFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agentDoc := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, value any) {
		t.Helper()
		if err := doc.Components.Schemas[name].Value.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	invalid := func(name string, value any) {
		t.Helper()
		if err := doc.Components.Schemas[name].Value.VisitJSON(value, openapi3.EnableJSONSchema2020()); err == nil {
			t.Fatalf("%s accepted invalid input", name)
		}
	}
	id, project, agent, version := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	account := map[string]any{"id": id, "projectId": project, "purpose": "model", "provider": "openai-codex", "cli": nil, "name": "共享账号", "accountLabel": nil, "revision": float64(1), "currentVersionId": version, "authMethod": "oauth", "status": "logged_in", "template": nil, "createdAt": "2026-10-15T12:00:00Z", "updatedAt": "2026-10-15T12:00:00Z", "deletedAt": nil}
	check("ProjectCredentialAccount", account)
	check("ProjectCredentialAccountList", map[string]any{"projectId": project, "credentialsRevision": float64(1), "accounts": []any{account}})
	check("ProjectCredentialCreate", map[string]any{"purpose": "model", "provider": "openai", "name": "key", "credential": map[string]any{"type": "api_key", "key": "fixture-not-real"}})
	check("ProjectCredentialCreate", map[string]any{"purpose": "cli", "cli": "gh", "name": "cli", "credential": map[string]any{"type": "cli", "fields": map[string]any{"token": "fixture-not-real"}}})
	for _, mode := range []string{"automatic", "explicit", "legacy_cleared"} {
		selection := map[string]any{"purpose": "model", "provider": "openai-codex", "cli": nil, "mode": mode, "selectedAccountId": nil, "resolvedAccountId": nil, "resolvedVersionId": nil, "status": "not_configured", "candidates": []any{account}}
		if mode == "explicit" {
			selection["selectedAccountId"] = id
			selection["resolvedAccountId"] = id
			selection["resolvedVersionId"] = version
			selection["status"] = "resolved"
		}
		if mode == "automatic" {
			selection["resolvedAccountId"] = id
			selection["resolvedVersionId"] = version
			selection["status"] = "resolved"
		}
		check("CredentialSelections", map[string]any{"agentId": agent, "projectId": project, "configVersion": float64(2), "credentialsRevision": float64(1), "selections": []any{selection}})
	}
	for _, mode := range []string{"explicit", "legacy_cleared"} {
		bad := map[string]any{"purpose": "model", "provider": "openai-codex", "cli": nil, "mode": mode, "selectedAccountId": nil, "resolvedAccountId": nil, "resolvedVersionId": nil, "status": "not_configured", "candidates": []any{account}}
		if mode == "legacy_cleared" {
			bad["resolvedAccountId"] = id
			bad["resolvedVersionId"] = version
			bad["status"] = "resolved"
		}
		invalid("CredentialSelection", bad)
	}
	check("CredentialSelectionPut", map[string]any{"purpose": "model", "provider": "openai-codex", "accountId": nil, "expectedConfigVersion": float64(2)})
	invalid("CredentialSelectionPut", map[string]any{"purpose": "model", "provider": "openai-codex", "expectedConfigVersion": float64(2)})
	invalid("CredentialSelectionPut", map[string]any{"purpose": "model", "provider": nil, "accountId": nil, "expectedConfigVersion": float64(2)})
	for _, bad := range []map[string]any{{"type": "api_key", "key": "fixture", "expectedRevision": float64(2)}, {"type": "api_key", "key": "fixture", "metadata": map[string]any{}}, {"type": "api_key", "key": nil}, {"credential": map[string]any{"type": "api_key", "key": "fixture"}}} {
		invalid("ProjectCredentialInput", bad)
	}
	operation := map[string]any{"id": uuid.NewString(), "projectId": project, "provider": "openai-codex", "targetAccountId": nil, "targetRevision": nil, "name": "new", "status": "awaiting_authorization", "accountId": nil, "startedAt": "2026-10-15T12:00:00Z", "expiresAt": "2026-10-15T12:15:00Z", "completedAt": nil, "authorizationUrl": "https://auth.openai.com/oauth/authorize?state=fixture", "errorCode": nil, "errorMessage": nil}
	check("ProjectModelAuthState", map[string]any{"operation": operation, "account": nil})
	operation["status"] = "succeeded"
	operation["completedAt"] = "2026-10-15T12:01:00Z"
	operation["authorizationUrl"] = nil
	operation["accountId"] = id
	check("ProjectModelAuthState", map[string]any{"operation": operation, "account": account})
	for code, details := range map[string]any{
		"credential_selection_required": map[string]any{"agentId": agent, "projectId": project, "purpose": "model", "provider": "openai", "cli": nil, "accountId": nil, "candidateAccountIds": []any{id}},
		"credential_account_deleted":    map[string]any{"projectId": project, "accountId": id},
		"credential_scope_mismatch":     map[string]any{"agentId": agent, "projectId": project, "purpose": "cli", "provider": nil, "cli": "gh", "accountId": id, "candidateAccountIds": []any{}},
		"project_credential_required":   map[string]any{"agentId": agent, "projectId": project},
		"precondition_failed":           map[string]any{"resourceKind": "account", "resourceId": id, "expectedVersion": float64(1), "currentVersion": float64(2)},
		"model_auth_in_progress":        map[string]any{"projectId": project, "operationId": id},
		"task_not_idle":                 map[string]any{"projectId": project, "tasks": []any{map[string]any{"id": id, "status": "running"}}},
	} {
		check("Error", map[string]any{"error": map[string]any{"code": code, "message": "固定安全提示", "details": details}})
	}
	for path, item := range doc.Paths.Map() {
		for _, op := range item.Operations() {
			switch op.OperationID {
			case "listProjectCredentialAccounts", "createProjectCredentialAccount", "getProjectCredentialAccount", "renameProjectCredentialAccount", "deleteProjectCredentialAccount", "replaceProjectCredentialAccount", "getProjectCredentialAccountImpact", "getAgentCredentialSelections", "putAgentCredentialSelection", "listProjectModelAuthOperations", "startProjectModelAuthLogin", "getProjectModelAuthLogin", "cancelProjectModelAuthLogin":
				if op.Extensions["x-access"] != "console-and-external" {
					t.Fatal(path, "access")
				}
				if agentDoc.Paths.Value(path) != nil {
					t.Fatal(path, "management path exposed to Agent")
				}
				for status, code := range map[string]string{"401": "unauthenticated", "403": "forbidden", "404": "not_found", "409": "project_credential_required", "412": "precondition_failed", "422": "validation_failed"} {
					r := op.Responses.Value(status)
					if r == nil {
						t.Fatal(path, status)
					}
					if err := r.Value.Content["application/json"].Schema.Value.VisitJSON(map[string]any{"error": map[string]any{"code": code, "message": "safe"}}, openapi3.EnableJSONSchema2020()); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
}
