package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/cliimport"
	"loop-now/internal/server/cliimport/source"
)

// Actual App permissions are separate from the early DTO-controller contract
// fixture and the direct Service principal test. No probe or source is reached.
func TestCLIManagementActualAppAgentManagerCannotProbeOrImport(t *testing.T) {
	w := newWorld(t)
	home := t.TempDir()
	var opened atomic.Int32
	s := startCLIImportWorld(t, w, func(ctx context.Context) (cliimport.Reader, error) { opened.Add(1); return source.New(home, nil) })
	defer s.stop()
	_, agent := s.createAgent("actual manager boundary")
	if _, e := s.a.Store.SetProjectManager(context.Background(), *agent.ProjectID, agent.ID); e != nil {
		t.Fatal(e)
	}
	raw := s.must(201, "POST", "/projects/"+agent.ProjectID.String()+"/credential-accounts", map[string]any{"purpose": "cli", "cli": "gh", "name": "synthetic account", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-not-a-real-token"}}})
	var account struct {
		ID               uuid.UUID `json:"id"`
		CurrentVersionID uuid.UUID `json:"currentVersionId"`
	}
	if json.Unmarshal(raw, &account) != nil || account.ID == uuid.Nil || account.CurrentVersionID == uuid.Nil {
		t.Fatal("account metadata missing")
	}
	token, e := auth.NewToken(auth.AgentPrefix)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.InsertAgentToken(context.Background(), token.ID, agent.ID, s.a.InstanceID, token.Hash); e != nil {
		t.Fatal(e)
	}
	request := func(status int, method, path string, value any) {
		t.Helper()
		var body io.Reader
		if value != nil {
			b, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(b)
		}
		req, e := http.NewRequest(method, "http://"+s.a.Addr()+path, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+token.Plain)
		if value != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s expected %d got %d", path, status, resp.StatusCode)
		}
		io.Copy(io.Discard, resp.Body)
	}
	request(200, "GET", "/agent/v1/whoami", nil) // Real, accepted runtime identity.
	base := "/projects/" + agent.ProjectID.String() + "/credential-accounts/" + account.ID.String() + "/verification-operations"
	request(403, "POST", "/api/v1"+base, map[string]any{"operationId": uuid.New(), "expectedVersionId": account.CurrentVersionID})
	request(403, "GET", "/api/v1"+base, nil)
	request(404, "POST", "/agent/v1"+base, map[string]any{"operationId": uuid.New(), "expectedVersionId": account.CurrentVersionID})
	request(403, "POST", "/api/v1/projects/"+agent.ProjectID.String()+"/cli-credential-import/scans", map[string]any{})
	request(404, "POST", "/agent/v1/projects/"+agent.ProjectID.String()+"/cli-credential-import/scans", map[string]any{})
	var verification, imports, runs, turns int
	e = s.a.Store.Pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM cli_credential_verifications),(SELECT count(*) FROM cli_credential_import_operations),(SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)").Scan(&verification, &imports, &runs, &turns)
	if e != nil || verification != 0 || imports != 0 || runs != 0 || turns != 0 || opened.Load() != 0 {
		t.Fatal("denied management request reached worker/source/runtime", e)
	}
	t.Log("actual App accepted Agent Manager whoami200 but console management403/Agent path404; verification/import/source/model/task0")
}
