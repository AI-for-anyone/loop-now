package api_test

import (
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/cliverify"
	"loop-now/internal/server/contracttest"
	"strings"
	"testing"
	"time"
)

func TestCLIVerificationFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, v any, valid bool) {
		t.Helper()
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		var data any
		json.Unmarshal(b, &data)
		e = doc.Components.Schemas[name].Value.VisitJSON(data, openapi3.EnableJSONSchema2020())
		if (e == nil) != valid {
			t.Fatalf("%s valid=%t: %v", name, valid, e)
		}
	}
	project, account, version, id := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	phase := "preparing_tool"
	op := cliverify.Operation{ID: id, ProjectID: project, AccountID: account, VersionID: version, CLI: "gh", AuthMethod: "static", TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: strings.Repeat("a", 64), AdapterRevision: "github-user-v1", ProbeKind: "github_user", Status: "pending", Phase: &phase, StartedAt: now, ExpiresAt: now.Add(time.Minute), IsCurrentVersion: true}
	check("CLIVerificationStart", cliverify.Start{OperationID: id, ExpectedVersionID: version}, true)
	check("CLIVerificationState", cliverify.State{AccountID: account}, true)
	check("CLIVerificationState", cliverify.State{AccountID: account, CurrentVersionID: &version, Operation: &op}, true)
	check("CLIVerificationOperation", op, true)
	for _, v := range []any{map[string]any{"operationId": nil, "expectedVersionId": version}, map[string]any{"operationId": id, "expectedVersionId": nil}, map[string]any{"operationId": uuid.Nil, "expectedVersionId": version}, map[string]any{"operationId": id, "expectedVersionId": version, "sql": "SELECT secret"}} {
		check("CLIVerificationStart", v, false)
	}
	for _, cli := range []string{"gh", "lark-cli", "gcloud", "mysql", "psql"} {
		success := op
		success.CLI = cli
		success.ProbeKind = map[string]string{"gh": "github_user", "lark-cli": "lark_bot", "gcloud": "google_crm_read", "mysql": "database_select1", "psql": "database_select1"}[cli]
		if cli == "gcloud" {
			success.AuthMethod = "service_account"
		}
		tool := map[string]string{"gh": "2.100.0", "lark-cli": "1.0.94", "gcloud": "568.0.0", "mysql": "8.4.9", "psql": "18.3"}[cli]
		success.ToolVersion = &tool
		success.Status = "succeeded"
		success.Phase = nil
		success.CompletedAt = &now
		success.Result = &cliverify.Result{Outcome: "passed", VerifiedAt: &now, DurationMS: 1}
		check("CLIVerificationOperation", success, true)
		success.ToolVersion = nil
		check("CLIVerificationOperation", success, false)
	}
	for status, outcome := range map[string]string{"failed": "failed", "expired": "expired", "cancelled": "cancelled"} {
		end := op
		end.Status = status
		end.Phase = nil
		end.CompletedAt = &now
		end.Result = &cliverify.Result{Outcome: outcome, DurationMS: 1}
		if status != "cancelled" {
			code := "timeout"
			end.Result.ErrorCode = &code
		}
		check("CLIVerificationOperation", end, true)
	}
	bad := op
	bad.CompletedAt = &now
	check("CLIVerificationOperation", bad, false)
	bad = op
	bad.TemplateFingerprint = "wrong"
	check("CLIVerificationOperation", bad, false)
	for code, details := range map[string]any{"precondition_failed": cliverify.VersionConflictDetails{ProjectID: project, AccountID: account, ExpectedVersionID: version}, "verification_in_progress": cliverify.ConflictDetails{ProjectID: project, AccountID: account, OperationID: id, VersionID: version}, "verification_operation_conflict": cliverify.ConflictDetails{ProjectID: project, AccountID: account, OperationID: id, VersionID: version}, "verification_unsupported": cliverify.UnsupportedDetails{CLI: "gh", TemplateSource: "builtin", Reason: "template_fingerprint_mismatch"}} {
		check("Error", map[string]any{"error": map[string]any{"code": code, "message": "固定安全错误", "details": details}}, true)
	}
	found := 0
	for path, item := range doc.Paths.Map() {
		for _, operation := range item.Operations() {
			if strings.Contains(operation.OperationID, "CLIVerification") {
				found++
				if operation.Extensions["x-access"] != "console-and-external" {
					t.Fatal("access")
				}
				if agent.Paths.Value(path) != nil {
					t.Fatal("Agent management route")
				}
				for _, status := range []string{"401", "403", "404", "409", "412", "422", "503"} {
					if operation.Responses.Value(status) == nil {
						t.Fatal("missing error response", status)
					}
				}
			}
		}
	}
	if found != 4 {
		t.Fatalf("operations=%d", found)
	}
}
