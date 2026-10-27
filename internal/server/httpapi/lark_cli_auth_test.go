package httpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/cliauth"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
)

// Actual router, validators, worker, cipher store and loopback provider. The
// self-owned database/Redis sockets prevent any fallback to a developer store.
func TestLarkLoginHTTPPermissionsPresenceAndSafeProjection(t *testing.T) {
	safetyResources(t)
	e := newEnv(t)
	p := larktest.New(t)
	logs := &safetyLogs{}
	s := cliauth.NewService(e.st, nil, nil, cliauth.ServiceOptions{LoginTimeout: 10 * time.Second, RecordTimeout: time.Second}, slog.New(slog.NewJSONHandler(logs, nil)))
	s.Lark, s.Account = p.Client, e.api.ProjectCredentials.Get
	e.api.CLIAuth = s
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	base := "/projects/" + e.dc.Project.ID.String() + "/cli-auth/operations"
	accounts := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts"
	source := decode[cm.Account](t, e.expect(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "source", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-http-fixture", "appSecret": "http-app-secret-sentinel", "brand": "feishu"}}}))
	input := func(id uuid.UUID) map[string]any {
		return map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision}
	}
	_, token := e.identity("lark-http-admin")
	external := e.external(token)
	agent := e.agentCaller(e.platformAgent("lark-http-agent").ID)
	// All four management routes remain unavailable to an Agent, including
	// reads and cancellation. No request below can start a provider operation.
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"POST", base, input(uuid.New())}, {"GET", base + "?cli=lark-cli", nil},
		{"GET", base + "/" + uuid.NewString(), nil}, {"POST", base + "/" + uuid.NewString() + "/cancel", nil},
	} {
		e.external("").expectError(401, "unauthenticated", route.method, route.path, route.body)
		e.console().expectError(403, "forbidden", route.method, route.path, route.body, "Authorization", "Bearer "+agent.token)
		agent.client = &http.Client{}
		agent.expectError(404, "not_found", route.method, route.path, route.body)
	}
	for _, key := range []string{"sourceAccountId", "expectedSourceRevision", "operationId", "cli", "authType"} {
		bad := input(uuid.New())
		bad[key] = nil
		e.console().expectError(422, "validation_failed", "POST", base, bad, "X-Contract-Invalid-Request", "1")
	}
	for _, key := range []string{"name", "accountId", "expectedRevision", "appSecret", "scopes", "SourceAccountID"} {
		bad := input(uuid.New())
		bad[key] = nil
		e.console().expectError(422, "validation_failed", "POST", base, bad, "X-Contract-Invalid-Request", "1")
	}
	var count int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM project_cli_auth_operations`).Scan(&count); err != nil || count != 0 || p.DeviceCalls.Load() != 0 {
		t.Fatal("invalid/unauthorized request had a side effect")
	}
	wait := func(id uuid.UUID, status string) cm.CLIAuthState {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
			code, body, headers := external.do("GET", base+"/"+id.String(), nil)
			if code != 200 || headers.Get("Cache-Control") != "no-store" {
				t.Fatal("operation GET status/cache boundary")
			}
			state := decode[cm.CLIAuthState](t, body)
			if state.Operation.Status == status {
				return state
			}
		}
		t.Fatal("operation status timeout")
		return cm.CLIAuthState{}
	}
	a := p.Add("ou-http-user", "HTTP user", nil, "")
	id := uuid.New()
	external.expect(202, "POST", base, input(id))
	awaiting := wait(id, "awaiting_authorization")
	if awaiting.Operation.AuthorizationURL == nil || awaiting.Operation.UserCode != nil || awaiting.Operation.SourceVersionID == nil {
		t.Fatal("Lark optional code/frozen source projection")
	}
	external.expect(202, "POST", base, input(id))
	conflict := e.expect(409, "POST", base, input(uuid.New()))
	var ce struct {
		Error struct {
			Details struct {
				CLI string `json:"cli"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(conflict, &ce) != nil || ce.Error.Details.CLI != "lark-cli" {
		t.Fatal("conflict details lost real CLI")
	}
	active := decode[cm.CLIAuthOperations](t, external.expect(200, "GET", base+"?cli=lark-cli", nil))
	google := decode[cm.CLIAuthOperations](t, e.expect(200, "GET", base, nil))
	if len(active.Operations) != 1 || len(google.Operations) != 0 || p.DeviceCalls.Load() != 1 {
		t.Fatal("CLI filter/idempotency boundary")
	}
	a.Release()
	done := wait(id, "succeeded")
	if done.Account == nil || done.Account.Status != "logged_in" || done.Account.LarkIdentity == nil || done.Account.LarkIdentity.GrantedScopes != nil || done.Operation.UserCode != nil || done.Operation.AuthorizationURL != nil || done.Operation.SourceVersionID == nil {
		t.Fatal("terminal/current metadata inconsistent")
	}
	// Re-login policy is not inferred from GitHub. Its current explicit
	// unavailable branch must not create an operation or contact a provider.
	e.expect(503, "POST", base, map[string]any{"operationId": uuid.New(), "cli": "lark-cli", "authType": "lark_user", "accountId": done.Account.ID, "expectedRevision": done.Account.Revision})
	b := p.Add("ou-cancelled", "cancelled", []string{}, "")
	cancelID := uuid.New()
	e.expect(202, "POST", base, input(cancelID))
	wait(cancelID, "awaiting_authorization")
	for i := 0; i < 2; i++ {
		out := decode[cm.CLIAuthState](t, external.expect(200, "POST", base+"/"+cancelID.String()+"/cancel", nil))
		if out.Operation.Status != "cancelled" || out.Operation.AuthorizationURL != nil || out.Operation.UserCode != nil || out.Operation.SourceVersionID == nil {
			t.Fatal("cancel must absorb and preserve safe source audit")
		}
	}
	b.Release()
	if err := s.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.DeniedDials.Load() != 0 || p.RefreshCalls.Load() != 0 || p.DeviceCalls.Load() != 2 {
		t.Fatal("unexpected provider work")
	}
	for _, body := range [][]byte{logs.Snapshot(), external.expect(200, "GET", accounts, nil), external.expect(200, "GET", base+"/"+id.String(), nil), external.expect(200, "GET", base+"/"+cancelID.String(), nil)} {
		for _, secret := range []string{"http-app-secret-sentinel", "synthetic-access-", "synthetic-refresh-", "flow_id="} {
			if strings.Contains(string(body), secret) {
				t.Fatal("secret/temporary authorization channel leaked")
			}
		}
	}
	if len(e.v.Failures()) != 0 {
		t.Fatal("formal HTTP contract validation failed")
	}
}
