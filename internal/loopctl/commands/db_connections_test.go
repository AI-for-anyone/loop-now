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

func dbConnectionsBody(mode, status string, ids ...string) map[string]any {
	accounts := []string{}
	connections := []any{}
	for _, id := range ids {
		accounts = append(accounts, id)
		var version any = actionUUID
		if status == "account_deleted" {
			version = nil
		}
		connections = append(connections, map[string]any{"accountId": id, "name": "duplicate label", "versionId": version, "status": status})
	}
	return map[string]any{"agentId": agentUUID, "projectId": credentialProjectID, "cli": "psql", "configVersion": 2, "credentialsRevision": 1, "mode": mode, "accountIds": accounts, "connections": connections, "status": status}
}

func dbConnectionSamples() map[string]clitest.Sample {
	samples := map[string]clitest.Sample{}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		samples[method+" /agents/{agentId}/db-connections/{cli}"] = clitest.Sample{Status: 200, Body: dbConnectionsBody("explicit_set", "resolved", credentialAccountID)}
	}
	return samples
}

func dbStub(t *testing.T) (*clitest.Stub, *env) {
	s, e := stub(t)
	for op, sample := range dbConnectionSamples() {
		s.On(op, sample)
	}
	return s, e
}

func TestDBConnectionsRequests(t *testing.T) {
	for _, name := range []string{"psql", "mysql"} {
		t.Run(name, func(t *testing.T) {
			s, e := dbStub(t)
			e.run("agent", "db-connections", "set", agentUUID, "--cli", name, "--account-id", credentialAccountID, "--account-id", actionUUID, "--expected-config-version", "7", "--yes").ok(t)
			b := lastBody(t, s)
			if len(b) != 2 || b["expectedConfigVersion"] != float64(7) || !reflect.DeepEqual(b["accountIds"], []any{credentialAccountID, actionUUID}) {
				t.Fatalf("PUT body: %v", b)
			}
			e.run("agent", "db-connections", "set", agentUUID, "--cli", name, "--empty", "--expected-config-version", "8", "--yes").ok(t)
			if ids := lastBody(t, s)["accountIds"]; !reflect.DeepEqual(ids, []any{}) {
				t.Fatalf("explicit empty must be [], got %v", ids)
			}
			e.run("agent", "db-connections", "clear", agentUUID, "--cli", name, "--expected-config-version", "9", "--yes").ok(t)
			reqs := s.Requests()
			if len(reqs) != 3 || reqs[2].RawQuery != "expectedConfigVersion=9" || len(reqs[2].Body) != 0 {
				t.Fatalf("DELETE must use query CAS and no body: %+v", reqs)
			}
			e.run("agent", "db-connections", "show", agentUUID, "--cli", name).ok(t)
			if len(s.Requests()) != 4 {
				t.Fatal("must not GET before writes or retry")
			}
		})
	}
}

func TestDBConnectionsInvalidZeroDial(t *testing.T) {
	base := []string{"agent", "db-connections", "set", "named-agent", "--cli", "psql", "--expected-config-version", "1", "--yes"}
	for _, flags := range [][]string{
		nil, {"--empty", "--account-id", actionUUID}, {"--empty=false"},
		{"--account-id", "label"}, {"--account-id", "00000000-0000-0000-0000-000000000000"},
		{"--account-id", actionUUID, "--account-id", actionUUID},
		{"--account-id", credentialAccountID, "--account-id", strings.ToUpper(credentialAccountID)},
		{"--empty", "--default-account-id", actionUUID},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			s, e := dbStub(t)
			e.block = true
			code := output.CodeInvalidArgument
			if len(flags) == 0 {
				code = output.CodeMissingArgument
			}
			e.run(append(append([]string{}, base...), flags...)...).fails(t, code, 2)
			if e.dialCount() != 0 || len(s.Requests()) != 0 {
				t.Fatal("invalid input performed address resolution or dial")
			}
		})
	}
	for _, args := range [][]string{
		{"set", agentUUID, "--cli", "mysql", "--empty", "--yes"},
		{"clear", agentUUID, "--cli", "mysql", "--expected-config-version", "0", "--yes"},
		{"clear", agentUUID, "--cli", "mysql", "--expected-config-version", "9223372036854775808", "--yes"},
		{"show", agentUUID, "--cli", "gh"},
	} {
		s, e := dbStub(t)
		code := output.CodeInvalidArgument
		if args[0] == "set" {
			code = output.CodeMissingArgument
		}
		e.run(append([]string{"agent", "db-connections"}, args...)...).fails(t, code, 2)
		if len(s.Requests()) != 0 {
			t.Fatal("invalid parameters requested")
		}
	}
}

func TestDBConnectionsStatesTransparent(t *testing.T) {
	for _, mode := range []string{"legacy_single", "explicit_set"} {
		for _, status := range []string{"resolved", "not_configured", "selection_required", "account_deleted", "scope_mismatch", "unavailable"} {
			t.Run(mode+"/"+status, func(t *testing.T) {
				s, e := dbStub(t)
				ids := []string{}
				if status != "not_configured" && status != "selection_required" {
					ids = append(ids, credentialAccountID)
				}
				want := dbConnectionsBody(mode, status, ids...)
				s.On("GET /agents/{agentId}/db-connections/{cli}", clitest.Sample{Status: 200, Body: want})
				r := e.run("agent", "db-connections", "show", agentUUID, "--cli", "psql")
				r.ok(t)
				var got any
				_ = json.Unmarshal(r.Env.Data, &got)
				b, _ := json.Marshal(got)
				w, _ := json.Marshal(want)
				if string(b) != string(w) {
					t.Fatalf("state filtered or changed: %s", b)
				}
			})
		}
	}
}

func TestDBConnectionsCASAndLegacyConflict(t *testing.T) {
	for _, action := range []string{"set", "clear"} {
		s, e := dbStub(t)
		method := "PUT"
		args := []string{"agent", "db-connections", action, agentUUID, "--cli", "psql", "--expected-config-version", "1", "--yes"}
		if action == "set" {
			args = append(args, "--empty")
		} else {
			method = "DELETE"
		}
		details := map[string]any{"resourceKind": "agent", "resourceId": agentUUID, "expectedVersion": 1, "currentVersion": 2}
		s.On(method+" /agents/{agentId}/db-connections/{cli}", clitest.Sample{Status: 412, Body: map[string]any{"error": map[string]any{"code": "precondition_failed", "message": "CAS", "details": details}}})
		r := e.run(args...)
		r.fails(t, "precondition_failed", 8)
		want, _ := json.Marshal(details)
		if string(r.Env.Error.Details) != string(want) || len(s.Requests()) != 1 {
			t.Fatalf("CAS must retain details without reread/retry: %s", r.Stdout)
		}
	}
	for _, action := range []string{"set", "clear"} {
		s, e := credentialStub(t)
		details := map[string]any{"agentId": agentUUID, "cli": "psql", "mode": "explicit_set", "configVersion": 2}
		s.On("PUT /agents/{agentId}/credential-selections", clitest.Sample{Status: 409, Body: map[string]any{"error": map[string]any{"code": "credential_selection_mode_conflict", "message": "集合模式", "details": details}}})
		args := []string{"agent", "credential-selection", action, agentUUID, "--cli", "psql", "--expected-config-version", "1"}
		if action == "set" {
			args = append(args, "--account-id", credentialAccountID)
		} else {
			args = append(args, "--yes")
		}
		r := e.run(args...)
		r.fails(t, "credential_selection_mode_conflict", 8)
		want, _ := json.Marshal(details)
		if string(r.Env.Error.Details) != string(want) || len(s.Requests()) != 1 {
			t.Fatalf("legacy must not reinterpret conflict: %s", r.Stdout)
		}
		b := lastBody(t, s)
		if _, exists := b["accountIds"]; exists || (action == "clear" && b["accountId"] != nil) {
			t.Fatal("old single shape changed")
		}
	}
}

func TestDBConnectionsUnknownWriteNotRetried(t *testing.T) {
	for _, action := range []string{"set", "clear"} {
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
			e.vars[conn.EnvCAFile] = writeFile(t, "ca.pem", string(pemCert(srv)))
			e.vars[conn.EnvRetryMax] = "3"
			args := []string{"agent", "db-connections", action, agentUUID, "--cli", "psql", "--expected-config-version", "1", "--yes"}
			if action == "set" {
				args = append(args, "--empty")
			}
			r := e.run(args...)
			if r.Code == 0 || count.Load() != 1 || !strings.Contains(r.Stdout, "写入结果未知") || !strings.Contains(r.Stdout, "db-connections show") {
				t.Fatalf("unknown mutation must not replay: calls=%d %s", count.Load(), r.Stdout)
			}
		})
	}
}
