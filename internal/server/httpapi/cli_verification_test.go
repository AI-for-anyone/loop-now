package httpapi_test

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliverify"
	"net/http"
	"strings"
	"testing"
	"time"
)

type verificationContractController struct {
	op    cliverify.Operation
	calls int
	err   error
}

func (c *verificationContractController) Start(_ context.Context, p, a uuid.UUID, in cliverify.Start) (cliverify.Operation, error) {
	c.calls++
	c.op.ProjectID = p
	c.op.AccountID = a
	c.op.ID = in.OperationID
	c.op.VersionID = in.ExpectedVersionID
	return c.op, c.err
}
func (c *verificationContractController) State(_ context.Context, _, a uuid.UUID) (cliverify.State, error) {
	c.calls++
	return cliverify.State{AccountID: a, CurrentVersionID: &c.op.VersionID}, c.err
}
func (c *verificationContractController) Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (cliverify.Operation, error) {
	c.calls++
	return c.op, c.err
}
func (c *verificationContractController) Cancel(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (cliverify.Operation, error) {
	c.calls++
	now := time.Now().UTC()
	c.op.Status = "cancelled"
	c.op.Phase = nil
	c.op.CompletedAt = &now
	c.op.Result = &cliverify.Result{Outcome: "cancelled"}
	return c.op, c.err
}

// This verifies real routing/authorization/JSON and the DTO seam, not a persistent
// probe worker. Unassembled App remains 503 until the implementation is wired.
func TestCLIVerificationHTTPContractStage(t *testing.T) {
	e := newEnv(t)
	now := time.Now().UTC()
	phase := "preparing_tool"
	id, version, account := uuid.New(), uuid.New(), uuid.New()
	c := &verificationContractController{op: cliverify.Operation{ID: id, ProjectID: e.dc.Project.ID, AccountID: account, VersionID: version, CLI: "gh", AuthMethod: "static", TemplateSource: "builtin", CredentialFormat: "1", TemplateFingerprint: strings.Repeat("a", 64), AdapterRevision: "github-user-v1", ProbeKind: "github_user", Status: "pending", Phase: &phase, StartedAt: now, ExpiresAt: now.Add(time.Minute), IsCurrentVersion: true}}
	e.api.CLIVerification = c
	base := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts/" + account.String() + "/verification-operations"
	body := map[string]any{"operationId": id, "expectedVersionId": version}
	_, token := e.identity("verification-admin")
	external := e.external(token)
	e.expect(202, "POST", base, body)
	external.expect(200, "GET", base, nil)
	e.expect(200, "GET", base+"/"+id.String(), nil)
	before := c.calls
	for _, bad := range []any{map[string]any{}, map[string]any{"operationId": nil, "expectedVersionId": version}, map[string]any{"operationId": id, "expectedVersionId": nil}, map[string]any{"operationId": uuid.Nil, "expectedVersionId": version}, map[string]any{"operationId": id, "expectedVersionId": version, "credential": "secret-not-input"}} {
		e.expect(422, "POST", base, bad, "X-Contract-Invalid-Request", "1")
	}
	if c.calls != before {
		t.Fatal("invalid input reached controller")
	}
	e.external("").expectError(401, "unauthenticated", "POST", base, body)
	a := e.platformAgent("VerificationBoundary")
	agent := e.agentCaller(a.ID)
	agent.client = &http.Client{}
	agent.expectError(404, "not_found", "POST", base, body)
	e.console().expectError(403, "forbidden", "POST", base, body, "Authorization", "Bearer "+agent.token)
	if c.calls != before {
		t.Fatal("unauthorized input reached controller")
	}
	for _, failure := range []*apperr.Error{
		{Status: 409, Code: apperr.CodeVerificationInProgress, Message: "已有验证", Details: cliverify.ConflictDetails{ProjectID: e.dc.Project.ID, AccountID: account, OperationID: id, VersionID: version}},
		{Status: 412, Code: apperr.CodePreconditionFailed, Message: "版本已变", Details: cliverify.VersionConflictDetails{ProjectID: e.dc.Project.ID, AccountID: account, ExpectedVersionID: version}},
		{Status: 422, Code: apperr.CodeVerificationUnsupported, Message: "模板不支持", Details: cliverify.UnsupportedDetails{CLI: "gh", TemplateSource: "builtin", Reason: "template_fingerprint_mismatch"}},
	} {
		c.err = failure
		external.expectError(failure.Status, failure.Code, "POST", base, body)
	}
	c.err = nil
	external.expect(200, "POST", base+"/"+id.String()+"/cancel", nil)
	e.api.CLIVerification = nil
	e.expect(503, "GET", base, nil)
	e.expect(503, "POST", base, body)
}
