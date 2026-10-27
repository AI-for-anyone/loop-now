package httpapi_test

import (
	"fmt"
	cm "loop-now/internal/server/credentialmodel"
	"net/http"
	"strings"
	"testing"
)

func TestDBConnectionHTTPContract(t *testing.T) {
	e := newEnv(t)
	a, path := e.newAgent("数据库集合")
	_, token := e.identity("db-admin")
	external := e.external(token)
	e.expect(200, "PUT", path+"/clis/mysql", nil)
	target := path + "/db-connections/mysql"
	state := decode[cm.DBConnections](t, external.expect(200, "GET", target, nil))
	if state.Mode != "legacy_single" || state.Status != "not_configured" {
		t.Fatal("initial mode")
	}
	base := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts"
	create := func(name string) cm.Account {
		return decode[cm.Account](t, external.expect(201, "POST", base, map[string]any{"purpose": "cli", "cli": "mysql", "name": name, "credential": map[string]any{"type": "cli", "fields": map[string]string{"host": "127.0.0.1", "port": "3306", "user": "synthetic-user", "password": "synthetic-db-password", "database": "synthetic-db", "sslMode": "DISABLED"}}}))
	}
	one, two := create("A"), create("B")
	// New field presence must not silently become an empty authorization set.
	for _, body := range []map[string]any{{"accountIds": nil, "expectedConfigVersion": state.ConfigVersion}, {"expectedConfigVersion": state.ConfigVersion}, {"accountIds": []any{nil}, "expectedConfigVersion": state.ConfigVersion}, {"accountIds": []any{one.ID, one.ID}, "expectedConfigVersion": state.ConfigVersion}} {
		external.expectError(422, "validation_failed", "PUT", target, body, "X-Contract-Invalid-Request", "1")
	}
	state = decode[cm.DBConnections](t, external.expect(200, "PUT", target, map[string]any{"accountIds": []any{one.ID, two.ID}, "expectedConfigVersion": state.ConfigVersion}))
	if state.Mode != "explicit_set" || len(state.Connections) != 2 {
		t.Fatal("set not persisted")
	}
	external.expectError(409, "credential_selection_mode_conflict", "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "mysql", "accountId": nil, "expectedConfigVersion": state.ConfigVersion})
	impact := decode[cm.Impact](t, external.expect(200, "GET", "/projects/"+e.dc.Project.ID.String()+"/credential-account-impact?purpose=cli&cli=mysql&accountId="+one.ID.String(), nil))
	if len(impact.Agents) != 1 || impact.Agents[0].Mode != "connection_set" {
		t.Fatal("impact did not report member")
	}
	external.expectError(412, "precondition_failed", "PUT", target, map[string]any{"accountIds": []any{}, "expectedConfigVersion": state.ConfigVersion - 1})
	state = decode[cm.DBConnections](t, external.expect(200, "PUT", target, map[string]any{"accountIds": []any{}, "expectedConfigVersion": state.ConfigVersion}))
	if len(state.Connections) != 0 || state.Mode != "explicit_set" {
		t.Fatal("empty fallback")
	}
	external.expect(200, "DELETE", target+fmt.Sprintf("?expectedConfigVersion=%d", state.ConfigVersion), nil)
	agentCaller := e.agentCaller(a.ID)
	agentCaller.client = &http.Client{}
	agentCaller.expectError(404, "not_found", "GET", target, nil)
	platform := e.platformAgent("数据库私有")
	external.expectError(422, "validation_failed", "GET", "/agents/"+platform.ID.String()+"/db-connections/mysql", nil)
	if failures := e.v.Failures(); len(failures) > 0 {
		t.Fatal(strings.Join(failures, "\n"))
	}
}
