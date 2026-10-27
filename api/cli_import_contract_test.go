package api_test

import (
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/contracttest"
	"testing"
	"time"
)

func TestCLIImportFormalContract(t *testing.T) {
	doc := contracttest.LoadFile(t, "openapi.yaml")
	agent := contracttest.LoadFile(t, "agent-openapi.yaml")
	check := func(name string, value any, valid bool) {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("invalid fixture")
		}
		e = doc.Components.Schemas[name].Value.VisitJSON(v, openapi3.EnableJSONSchema2020())
		if (e == nil) != valid {
			t.Fatalf("%s valid=%v: %v", name, valid, e)
		}
	}
	project, scan, id, candidate := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	phase := "preparing"
	in := cliimport.StartInput{OperationID: id, ScanID: scan, CandidateIDs: []uuid.UUID{candidate}, ExpectedCredentialsRevision: 1}
	check("CLIImportStart", in, true)
	check("CLIImportStart", map[string]any{"operationId": uuid.Nil, "scanId": scan, "candidateIds": []uuid.UUID{candidate}, "expectedCredentialsRevision": 1}, false)
	for _, bad := range []any{map[string]any{"operationId": id, "scanId": scan, "candidateIds": []uuid.UUID{candidate, candidate}, "expectedCredentialsRevision": 1}, map[string]any{"operationId": id, "scanId": scan, "candidateIds": []uuid.UUID{candidate}, "expectedCredentialsRevision": 1, "password": "must-not-input"}} {
		check("CLIImportStart", bad, false)
	}
	operation := cliimport.Operation{ID: id, ProjectID: project, ScanID: scan, Status: "pending", Phase: &phase, CreatedAt: now, DeadlineAt: now.Add(time.Minute), Items: []cliimport.Item{{CandidateID: candidate, CLI: "mysql", Label: "fixture", Status: "pending"}}}
	check("CLIImportState", cliimport.State{Operation: operation}, true)
	for _, status := range []string{"succeeded", "failed", "cancelled", "expired"} {
		v := operation
		v.Status = status
		v.Phase = nil
		v.CompletedAt = &now
		if status == "succeeded" {
			rev := int64(2)
			a, b := uuid.New(), uuid.New()
			v.CredentialsRevision = &rev
			v.Items = []cliimport.Item{{CandidateID: candidate, CLI: "mysql", Label: "fixture", Status: "imported", AccountID: &a, VersionID: &b}}
		} else {
			code := "timeout"
			v.ErrorCode = &code
			v.Items = []cliimport.Item{{CandidateID: candidate, CLI: "mysql", Label: "fixture", Status: "not_committed"}}
		}
		check("CLIImportState", cliimport.State{Operation: v}, true)
	}
	sources := []cliimport.SourceSummary{}
	for _, cli := range []string{"gh", "lark-cli", "gcloud", "psql", "mysql"} {
		sources = append(sources, cliimport.SourceSummary{CLI: cli, Status: "no_source"})
	}
	check("CLIImportScan", cliimport.Scan{ScanID: scan, ProjectID: project, ExpiresAt: now.Add(time.Minute), CredentialsRevision: 1, Sources: sources, Candidates: []cliimport.Candidate{}}, true)
	for _, name := range []string{"CLIImportUUID", "CLIImportCLI", "CLIImportSafeCode", "CLIImportScan", "CLIImportCandidate", "CLIImportSupplement", "CLIImportStart", "CLIImportState", "CLIImportOperation", "CLIImportItem"} {
		if agent.Components.Schemas[name] == nil {
			t.Fatal("missing shared schema", name)
		}
	}
	ids := map[string]bool{"scanProjectCLICredentialImports": true, "startProjectCLICredentialImport": true, "getProjectCLICredentialImport": true, "cancelProjectCLICredentialImport": true}
	for _, path := range agent.Paths.Map() {
		for _, op := range path.Operations() {
			if ids[op.OperationID] {
				t.Fatal("Agent API gained import management")
			}
		}
	}
	count := 0
	for _, path := range doc.Paths.Map() {
		for _, op := range path.Operations() {
			if ids[op.OperationID] {
				count++
				if op.Extensions["x-access"] != "console" {
					t.Fatal("import was exposed to external/Agent")
				}
			}
		}
	}
	if count != 4 {
		t.Fatal("missing formal routes")
	}
}
