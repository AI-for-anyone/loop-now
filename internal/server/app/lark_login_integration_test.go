package app_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"strings"
	"testing"
	"time"
)

func waitLarkApp(t *testing.T, s *server, base string, id uuid.UUID, want string) cm.CLIAuthState {
	t.Helper()
	for until := time.Now().Add(7 * time.Second); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
		state := decode[cm.CLIAuthState](t, s.must(200, "GET", base+"/"+id.String(), nil))
		if state.Operation.Status == want {
			return state
		}
		if state.Operation.Status == "failed" || state.Operation.Status == "expired" {
			t.Fatalf("unexpected safe status=%s", state.Operation.Status)
		}
	}
	t.Fatal("Lark App timeout")
	return cm.CLIAuthState{}
}
func TestLarkAppOfficialProtocolSourceAndNormalCapture(t *testing.T) {
	w := githubAppWorld(t)
	p := larktest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.Lark = p.Client
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	base := "/projects/" + project.String() + "/cli-auth/operations"
	accounts := "/projects/" + project.String() + "/credential-accounts"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "离线模型", "credential": map[string]string{"type": "api_key", "key": "synthetic-lark-model"}})
	source := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "原bot应用", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	if source.Status != "saved" || source.LarkIdentity != nil || source.LarkApplication == nil || source.LarkApplication.AppID != "app-fixture" {
		t.Fatal("safe bot/source projection invalid")
	}
	path, agent := s.createAgent("Lark授权Agent")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	before, e := s.a.Config.ExecutionSnapshot(ctx, agent.ID)
	if e != nil || before.LarkBinding != nil || len(before.CLIPayloads["lark-cli"].Files) == 0 {
		t.Fatal("original bot capture changed", e)
	}
	a := p.Add("ou-actual-fixture", "核实用户", nil, "")
	a.Release()
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": source.ID, "expectedSourceRevision": source.Revision})
	state := waitLarkApp(t, s, base, id, "succeeded")
	if state.Account == nil || state.Account.LarkIdentity == nil || state.Account.Status != "logged_in" || state.Account.LarkIdentity.GrantedScopes != nil || state.Operation.AuthorizationURL != nil || state.Operation.UserCode != nil {
		t.Fatal("actual App did not atomically project verified independent user")
	}
	user := *state.Account
	// Two saved choices are ambiguous until a human selects explicitly; adding
	// user OAuth must not silently replace the old bot or grant a denied Agent.
	if _, e = s.a.Config.ExecutionSnapshot(ctx, agent.ID); e == nil {
		t.Fatal("new user silently won automatic selection")
	}
	current := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": user.ID, "expectedConfigVersion": current.ConfigVersion})
	after, e := s.a.Config.ExecutionSnapshot(ctx, agent.ID)
	if e != nil || after.LarkBinding == nil || after.LarkBinding.VersionID != *user.CurrentVersionID || len(after.CLIPayloads["lark-cli"].Files) != 0 || before.LarkBinding != nil {
		t.Fatal("fixed user capture mixed rotating secrets/static bot or retrofitted old capture", e)
	}
	s.must(200, "DELETE", accounts+"/"+source.ID.String()+"?expectedRevision=1", nil)
	read := decode[cm.Account](t, s.must(200, "GET", accounts+"/"+user.ID.String(), nil))
	if read.Status != "logged_in" || read.LarkIdentity.OpenID != "ou-actual-fixture" {
		t.Fatal("source deletion changed independent user")
	}
	active := decode[cm.CLIAuthOperations](t, s.must(200, "GET", base+"?cli=lark-cli", nil))
	legacy := decode[cm.CLIAuthOperations](t, s.must(200, "GET", base, nil))
	if len(active.Operations) != 0 || len(legacy.Operations) != 0 {
		t.Fatal("active list/legacy default changed")
	}
	var runs, turns, grants int
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM lark_cli_grants)`).Scan(&runs, &turns, &grants); e != nil || runs+turns != 0 || grants != 1 || p.DeniedDials.Load() != 0 {
		t.Fatal("management triggered model/task or non-loopback provider")
	}
	raw, _ := json.Marshal(read)
	if strings.Contains(string(raw), "synthetic-access") || strings.Contains(string(raw), "fixture-app-secret") {
		t.Fatal("safe metadata leaked secret")
	}
}
