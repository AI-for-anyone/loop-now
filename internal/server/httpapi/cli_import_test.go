package httpapi_test

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/cliimport"
	"testing"
	"time"
)

type importContractController struct {
	calls int
	op    cliimport.Operation
	scan  cliimport.Scan
}

func (c *importContractController) Scan(context.Context, uuid.UUID) (cliimport.Scan, error) {
	c.calls++
	return c.scan, nil
}
func (c *importContractController) Start(context.Context, uuid.UUID, cliimport.StartInput) (cliimport.Operation, error) {
	c.calls++
	return c.op, nil
}
func (c *importContractController) Get(context.Context, uuid.UUID, uuid.UUID) (cliimport.Operation, error) {
	c.calls++
	return c.op, nil
}
func (c *importContractController) Cancel(context.Context, uuid.UUID, uuid.UUID) (cliimport.Operation, error) {
	c.calls++
	v := c.op
	v.Status = "cancelled"
	v.Phase = nil
	now := time.Now().UTC()
	v.CompletedAt = &now
	code := "cancelled"
	v.ErrorCode = &code
	v.Items[0].Status = "not_committed"
	return v, nil
}

// Formal route/permission/DTO verification only, not a source Reader or durable
// import worker acceptance. No host discovery or secret extraction occurs.
func TestCLIImportHTTPContractStage(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	id, scan, candidate := uuid.New(), uuid.New(), uuid.New()
	phase := "preparing"
	c := &importContractController{op: cliimport.Operation{ID: id, ProjectID: e.dc.Project.ID, ScanID: scan, Status: "pending", Phase: &phase, CreatedAt: now, DeadlineAt: now.Add(time.Minute), Items: []cliimport.Item{{CandidateID: candidate, CLI: "mysql", Label: "fixture", Status: "pending"}}}, scan: cliimport.Scan{ScanID: scan, ProjectID: e.dc.Project.ID, ExpiresAt: now.Add(time.Minute), CredentialsRevision: 1, Sources: []cliimport.SourceSummary{}, Candidates: []cliimport.Candidate{}}}
	for _, cli := range []string{"gh", "lark-cli", "gcloud", "psql", "mysql"} {
		c.scan.Sources = append(c.scan.Sources, cliimport.SourceSummary{CLI: cli, Status: "no_source"})
	}
	e.api.CLIImport = c
	base := "/projects/" + e.dc.Project.ID.String() + "/cli-credential-import"
	body := map[string]any{"operationId": id, "scanId": scan, "candidateIds": []uuid.UUID{candidate}, "expectedCredentialsRevision": 1}
	e.expect(201, "POST", base+"/scans", map[string]any{})
	e.expect(202, "POST", base+"/imports", body)
	e.expect(200, "GET", base+"/imports/"+id.String(), nil)
	before := c.calls
	e.expectError(422, "validation_failed", "GET", base+"/imports/"+uuid.Nil.String(), nil)
	e.expectError(422, "validation_failed", "POST", "/projects/"+uuid.Nil.String()+"/cli-credential-import/scans", map[string]any{})
	for _, bad := range []any{map[string]any{}, map[string]any{"operationId": nil, "scanId": scan, "candidateIds": []uuid.UUID{candidate}, "expectedCredentialsRevision": 1}, map[string]any{"operationId": id, "scanId": scan, "candidateIds": []uuid.UUID{uuid.Nil}, "expectedCredentialsRevision": 1}, map[string]any{"operationId": id, "scanId": scan, "candidateIds": []uuid.UUID{candidate}, "expectedCredentialsRevision": 1, "host": "forbidden-input"}} {
		e.expect(422, "POST", base+"/imports", bad, "X-Contract-Invalid-Request", "1")
	}
	_, token := e.identity("import-rejected-external")
	e.external(token).expectError(403, "forbidden", "POST", base+"/scans", map[string]any{})
	e.external("").expectError(401, "unauthenticated", "POST", base+"/scans", map[string]any{})
	a := e.platformAgent("ImportBoundary")
	agent := e.agentCaller(a.ID)
	e.console().expectError(403, "forbidden", "POST", base+"/scans", map[string]any{}, "Authorization", "Bearer "+agent.token)
	if c.calls != before {
		t.Fatal("invalid or unauthorized request reached source seam")
	}
	e.expect(200, "POST", base+"/imports/"+id.String()+"/cancel", map[string]any{})
	e.api.CLIImport = nil
	e.expect(503, "POST", base+"/scans", map[string]any{})
	e.expect(503, "POST", base+"/imports", body)
	e.expect(503, "GET", base+"/imports/"+id.String(), nil)
}
