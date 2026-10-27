package httpapi_test

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
	"net/http"
	"strings"
	"testing"
)

func TestProjectCredentialHTTPCRUDSelections(t *testing.T) {
	e := newEnv(t)
	a, path := e.newAgent("共享使用者")
	_, token := e.identity("credential-admin")
	external := e.external(token)
	base := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts"
	list := decode[cm.AccountList](t, external.expect(200, "GET", base+"?purpose=model&provider=anthropic", nil))
	if len(list.Accounts) != 0 {
		t.Fatal("nonempty fixture")
	}
	state := decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	if state.Selections[0].Status != "not_configured" {
		t.Fatal("zero candidate")
	}
	create := func(name string) cm.Account {
		return decode[cm.Account](t, external.expect(201, "POST", base, map[string]any{"purpose": "model", "provider": "anthropic", "name": name, "credential": map[string]any{"type": "api_key", "key": "synthetic-project-key"}}))
	}
	x := create("共享X")
	state = decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	if state.Selections[0].Mode != "automatic" || state.Selections[0].ResolvedAccountID == nil || *state.Selections[0].ResolvedAccountID != x.ID {
		t.Fatal("unique not automatic")
	}
	metadata := decode[apitypes.Agent](t, e.expect(200, "GET", path, nil))
	if metadata.ModelCredential == nil || metadata.ModelCredential.ProjectID == nil || metadata.ModelCredential.AccountID == nil || *metadata.ModelCredential.AccountID != x.ID {
		t.Fatal("legacy read lost project source")
	}
	y := create("共享Y")
	state = decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	if state.Selections[0].Status != "selection_required" || len(state.Selections[0].Candidates) != 2 {
		t.Fatal("multi candidate silently picked")
	}
	state = decode[cm.Selections](t, external.expect(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": x.ID, "expectedConfigVersion": state.ConfigVersion}))
	if state.Selections[0].Mode != "explicit" || *state.Selections[0].SelectedAccountID != x.ID {
		t.Fatal("explicit missing")
	}
	external.expectError(412, "precondition_failed", "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": y.ID, "expectedConfigVersion": state.ConfigVersion - 1})
	renamed := decode[cm.Account](t, external.expect(200, "PATCH", base+"/"+x.ID.String(), map[string]any{"name": "重命名", "expectedRevision": x.Revision}))
	if renamed.ID != x.ID || renamed.CurrentVersionID == nil || *renamed.CurrentVersionID != *x.CurrentVersionID {
		t.Fatal("rename changed identity")
	}
	external.expectError(412, "precondition_failed", "DELETE", base+"/"+x.ID.String()+"?expectedRevision=1", nil)
	replacement := decode[cm.Account](t, external.expect(200, "PUT", base+"/"+x.ID.String()+"/credential", map[string]any{"expectedRevision": renamed.Revision, "credential": map[string]any{"type": "api_key", "key": "synthetic-replacement-key"}}))
	if *replacement.CurrentVersionID == *x.CurrentVersionID {
		t.Fatal("replacement reused version")
	}
	impact := decode[cm.Impact](t, external.expect(200, "GET", "/projects/"+e.dc.Project.ID.String()+"/credential-account-impact?purpose=model&provider=anthropic&accountId="+x.ID.String(), nil))
	if len(impact.Agents) != 1 || impact.Agents[0].Mode != "explicit" || impact.Agents[0].AgentID != a.ID {
		t.Fatal("impact not current explicit")
	}
	external.expect(200, "DELETE", base+"/"+x.ID.String()+"?expectedRevision=3", nil)
	state = decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	if state.Selections[0].Status != "account_deleted" || state.Selections[0].ResolvedAccountID != nil {
		t.Fatal("deleted explicit fell back")
	}
	state = decode[cm.Selections](t, external.expect(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": nil, "expectedConfigVersion": state.ConfigVersion}))
	if state.Selections[0].Mode != "automatic" || *state.Selections[0].ResolvedAccountID != y.ID {
		t.Fatal("null did not restore automatic")
	}
	external.expectError(409, "project_credential_required", "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-legacy-key"})
	external.expectError(409, "project_credential_required", "DELETE", path+"/model-credential", nil)
	e.expect(200, "PUT", path+"/clis/gh", nil)
	external.expectError(409, "project_credential_required", "PUT", path+"/clis/gh/credential", map[string]any{"fields": map[string]string{"token": "synthetic-legacy-gh"}})
	cli := decode[cm.Account](t, external.expect(201, "POST", base, map[string]any{"purpose": "cli", "cli": "gh", "name": "共享gh", "credential": map[string]any{"type": "cli", "fields": map[string]string{"token": "synthetic-gh"}}}))
	legacyRead := decode[apitypes.Agent](t, e.expect(200, "GET", path, nil))
	foundCLI := false
	for _, tool := range legacyRead.Clis {
		if tool.Name == "gh" {
			foundCLI = tool.Credential != nil && tool.Credential.ProjectID != nil && *tool.Credential.ProjectID == e.dc.Project.ID && tool.Credential.AccountID != nil && *tool.Credential.AccountID == cli.ID && tool.Credential.Status == "set"
		}
	}
	if !foundCLI {
		t.Fatal("old Agent CLI read lost shared safe source metadata")
	}
	state = decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	external.expectError(422, "credential_scope_mismatch", "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": cli.ID, "expectedConfigVersion": state.ConfigVersion})
	platform := e.platformAgent("私有兼容")
	e.expect(200, "PUT", "/agents/"+platform.ID.String()+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-platform-key"})
	external.expectError(403, "forbidden", "GET", "/agents/"+platform.ID.String()+"/model-auth", nil)
	agentCaller := e.agentCaller(a.ID)
	agentCaller.client = &http.Client{}
	agentCaller.expectError(404, "not_found", "GET", base, nil)
	var versions int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, x.ID).Scan(&versions); err != nil || versions != 0 {
		t.Fatal("deleted account kept its versions", versions, err)
	}
	if failures := e.v.Failures(); len(failures) > 0 {
		t.Fatal(failures)
	}
}

func TestProjectCredentialHTTPResolutionErrorsAndLegacyHistory(t *testing.T) {
	e, manager, task := taskEnv(t)
	agent := task.Nodes[0].AgentID
	path := "/agents/" + agent.String()
	x := e.sharedCredential(t, "model", "anthropic", "A", "synthetic-A")
	e.sharedCredential(t, "model", "anthropic", "B", "synthetic-B")
	create := func() {
		manager.expectError(409, "credential_selection_required", "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "需选择", "request": map[string]any{}, "workflow": task.Workflow})
	}
	create()
	state := decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	state = decode[cm.Selections](t, e.expect(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": x.ID, "expectedConfigVersion": state.ConfigVersion}))
	e.expect(200, "DELETE", "/projects/"+e.dc.Project.ID.String()+"/credential-accounts/"+x.ID.String()+"?expectedRevision=1", nil)
	e.expectError(409, "credential_account_deleted", "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": x.ID, "expectedConfigVersion": state.ConfigVersion})
	manager.expectError(409, "credential_account_deleted", "POST", "/tasks", map[string]any{"actionId": uuid.New(), "title": "已删明确账号", "request": map[string]any{}, "workflow": task.Workflow})
	// The old project login entry cannot start even without a usable helper.
	e.expectError(409, "project_credential_required", "POST", path+"/model-auth/operations", map[string]any{"provider": "openai-codex", "expectedConfigVersion": state.ConfigVersion})
	old := uuid.New()
	if _, err := e.st.Pool.Exec(context.Background(), `INSERT INTO model_auth_operations(id,agent_id,provider,config_version,status,expires_at,completed_at)VALUES($1,$2,'openai-codex',1,'cancelled',now(),now())`, old, agent); err != nil {
		t.Fatal(err)
	}
	historyPath := path + "/model-auth/operations/" + old.String()
	for _, method := range []string{"GET", "POST"} {
		target := historyPath
		var input any
		if method == "POST" {
			target += "/cancel"
			input = map[string]any{}
		}
		body := e.expect(200, method, target, input)
		if strings.Contains(string(body), "https://") || !strings.Contains(string(body), `"status":"cancelled"`) {
			t.Fatal("legacy terminal view revived URL/state")
		}
	}
	// Callback-only fixture isolates the formal conflict response. Actual fd
	// helper lifecycle coverage is separate (4.2/7.4), not claimed by this test.
	e.api.Config.ModelAuth.Helper = projectContractWaitingLogin{}
	base := "/projects/" + e.dc.Project.ID.String() + "/model-auth/operations"
	stateOp := decode[cm.LoginState](t, e.expect(202, "POST", base, map[string]any{"provider": "openai-codex", "name": "等待登录"}))
	e.expectError(409, "model_auth_in_progress", "POST", base, map[string]any{"provider": "openai-codex", "name": "另一登录"})
	e.expect(200, "POST", base+"/"+stateOp.Operation.ID.String()+"/cancel", nil)
	if failures := e.v.Failures(); len(failures) > 0 {
		t.Fatal(failures)
	}
}

type projectContractWaitingLogin struct{}

func (projectContractWaitingLogin) Login(ctx context.Context, _ uuid.UUID, notify func(string) error) (modelcredential.Credential, error) {
	if err := notify("https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture"); err != nil {
		return modelcredential.Credential{}, err
	}
	<-ctx.Done()
	return modelcredential.Credential{}, modelauth.ErrAuthCancelled
}
func TestProjectCredentialHTTPInvalidPresenceNoWrites(t *testing.T) {
	e := newEnv(t)
	_, path := e.newAgent("输入边界")
	base := "/projects/" + e.dc.Project.ID.String() + "/credential-accounts"
	for _, input := range []any{
		map[string]any{"purpose": "model", "provider": "anthropic", "cli": nil, "name": "bad", "credential": map[string]any{"type": "api_key", "key": "sentinel-secret"}},
		map[string]any{"purpose": "model", "provider": "anthropic", "name": "bad", "credential": map[string]any{"type": "api_key", "key": "sentinel-secret", "metadata": "sentinel-secret"}},
		map[string]any{"purpose": "cli", "cli": "gh", "name": "bad", "credential": map[string]any{"type": "cli", "fields": map[string]any{"token": nil}}},
	} {
		code, body, _ := e.do("POST", base, input, "X-Contract-Invalid-Request", "1")
		if code != 422 || strings.Contains(string(body), "sentinel-secret") {
			t.Fatal("unsafe invalid input response", code)
		}
	}
	for _, input := range []any{map[string]any{"purpose": "model", "provider": "anthropic", "expectedConfigVersion": 1}, map[string]any{"purpose": "model", "provider": nil, "accountId": nil, "expectedConfigVersion": 1}} {
		e.expect(422, "PUT", path+"/credential-selections", input, "X-Contract-Invalid-Request", "1")
	}
	list := decode[cm.AccountList](t, e.expect(200, "GET", base, nil))
	if len(list.Accounts) != 0 {
		t.Fatal("invalid input wrote accounts")
	}
	var version int64
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT config_version FROM agents WHERE id=$1`, strings.TrimPrefix(path, "/agents/")).Scan(&version); err != nil || version != 1 {
		t.Fatal("invalid selection changed Agent", err)
	}
}

func (e *env) sharedCredential(t *testing.T, purpose, slot, name, value string) cm.Account {
	t.Helper()
	input := map[string]any{"purpose": purpose, "name": name}
	if purpose == "model" {
		input["provider"] = slot
		input["credential"] = map[string]any{"type": "api_key", "key": value}
	} else {
		input["cli"] = slot
		input["credential"] = map[string]any{"type": "cli", "fields": map[string]string{"token": value}}
	}
	return decode[cm.Account](t, e.expect(201, "POST", "/projects/"+e.dc.Project.ID.String()+"/credential-accounts", input))
}

func TestProjectCredentialHTTPSelectionScopeAndTemplate(t *testing.T) {
	e := newEnv(t)
	a, path := e.newAgent("选择边界")
	cli := e.sharedCredential(t, "cli", "gh", "共享工具", "synthetic-gh")
	e.expect(200, "PUT", path+"/clis/jq", nil)
	state := decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	for _, item := range state.Selections {
		if item.CLI != nil && *item.CLI == "jq" {
			t.Fatal("no-template CLI offered account selection")
		}
	}
	e.expectError(422, "validation_failed", "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "jq", "accountId": cli.ID, "expectedConfigVersion": state.ConfigVersion})
	project := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "其他项目账号"}))
	other := decode[cm.Account](t, e.expect(201, "POST", "/projects/"+project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "另一项目", "credential": map[string]string{"type": "api_key", "key": "synthetic-other"}}))
	e.expectError(404, "not_found", "PUT", path+"/credential-selections", map[string]any{"purpose": "model", "provider": "anthropic", "accountId": other.ID, "expectedConfigVersion": state.ConfigVersion})
	after := decode[cm.Selections](t, e.expect(200, "GET", path+"/credential-selections", nil))
	if after.ConfigVersion != state.ConfigVersion {
		t.Fatal("invalid scope/template changed config")
	}
	var rows int
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_credential_selections WHERE agent_id=$1`, a.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("rejected selection persisted", err)
	}
	if failures := e.v.Failures(); len(failures) > 0 {
		t.Fatal(failures)
	}
}
