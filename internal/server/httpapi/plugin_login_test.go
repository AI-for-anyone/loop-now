package httpapi_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
)

type httpPluginHelper struct{}

func (httpPluginHelper) Login(ctx context.Context, req modelauth.PluginLoginRequest, ui modelauth.PluginLoginUI, replies <-chan cm.LoginInput) ([]byte, error) {
	ui.Prompt(cm.LoginPrompt{ID: "1", Kind: "select", Message: "Pick", Options: []cm.LoginPromptOption{{ID: "browser", Label: "Browser"}, {ID: "device", Label: "Device"}}})
	select {
	case <-replies:
		ui.PromptClosed("1")
	case <-ctx.Done():
		return nil, modelauth.ErrAuthCancelled
	}
	if err := ui.DeviceCode("ABCD-EFGH", "https://accounts.example.test/device", time.Minute); err != nil {
		return nil, err
	}
	ui.Prompt(cm.LoginPrompt{ID: "2", Kind: "manual_code", Options: []cm.LoginPromptOption{}})
	select {
	case <-replies:
	case <-ctx.Done():
		return nil, modelauth.ErrAuthCancelled
	}
	return []byte(`{"type":"oauth","access":"m","refresh":"m","expires":1}`), nil
}

// 插件提供方的项目登录经 HTTP：开始、问题、设备码、回答、成功；列表可以不带 provider。
func TestPluginProviderLoginAPI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	plugin := "grokish"
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: plugin, Provider: modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off", Input: []string{"text"}}}}}})
	e.api.Models, e.api.ProjectCredentials.Auth.Models = reg, reg
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: plugin, DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.st.SaveProjectPlugin(ctx, e.dc.Project.ID, plugin, true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
		t.Fatal(err)
	}
	e.api.ProjectCredentials.Plugins = &modelauth.PluginLogins{Helper: httpPluginHelper{}, States: &providerstate.Manager{Root: filepath.Join(t.TempDir(), "states")},
		Extensions: func(context.Context, string) ([]string, error) { return []string{"/x/index.ts"}, nil }}
	base := "/projects/" + e.dc.Project.ID.String() + "/model-auth/operations"
	start := decode[cm.LoginState](t, e.expect(202, "POST", base, map[string]any{"provider": "fake-oauth", "name": "订阅"}))
	op := base + "/" + start.Operation.ID.String()
	poll := func(ok func(cm.Operation) bool) cm.LoginState {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			s := decode[cm.LoginState](t, e.expect(200, "GET", op, nil))
			if ok(s.Operation) {
				return s
			}
		}
		t.Fatal("等待登录状态超时")
		return cm.LoginState{}
	}
	s := poll(func(o cm.Operation) bool { return o.Prompt != nil })
	if s.Operation.Prompt.Kind != "select" || s.Operation.Provider != "fake-oauth" {
		t.Fatalf("问题：%+v", s.Operation)
	}
	list := decode[cm.Operations](t, e.expect(200, "GET", base, nil))
	if len(list.Operations) != 1 || list.Operations[0].Provider != "fake-oauth" || list.Operations[0].Prompt == nil {
		t.Fatalf("不带 provider 的列表：%+v", list)
	}
	e.expect(422, "POST", op+"/input", map[string]any{"promptId": "1", "value": "pigeon"})
	e.expect(200, "POST", op+"/input", map[string]any{"promptId": "1", "value": "device"})
	s = poll(func(o cm.Operation) bool { return o.DeviceCode != nil && o.Prompt != nil })
	if s.Operation.Status != "awaiting_authorization" || s.Operation.DeviceCode.UserCode != "ABCD-EFGH" || s.Operation.DeviceCode.ExpiresAt == nil || s.Operation.Prompt.Kind != "manual_code" {
		t.Fatalf("设备码与手工输入：%+v", s.Operation)
	}
	e.expect(200, "POST", op+"/input", map[string]any{"promptId": "2", "value": "pasted-callback"})
	s = poll(func(o cm.Operation) bool { return o.Status == "succeeded" })
	if s.Account == nil || *s.Account.AuthMethod != "oauth" || s.Operation.DeviceCode != nil {
		t.Fatalf("成功：%+v", s)
	}
	e.expect(422, "POST", op+"/input", map[string]any{"promptId": "2", "value": "late"})
	e.expect(422, "POST", base, map[string]any{"provider": "nope", "name": "x"})

	// Agent 选这个提供方并绑定账号；开着“用本机的登录状态”时选择里带 localLogin。
	agent := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "订阅助手", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, plugin, true, nil); err != nil {
		t.Fatal(err)
	}
	e.api.Config.Models = reg
	e.expect(200, "PATCH", "/agents/"+agent.ID.String(), map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}})
	missing := "missing"
	e.api.ProjectCredentials.LocalLogin = func(_ context.Context, provider string) (*string, error) {
		if provider == "fake-oauth" {
			return &missing, nil
		}
		return nil, nil
	}
	sel := decode[cm.Selections](t, e.expect(200, "GET", "/agents/"+agent.ID.String()+"/credential-selections", nil))
	found := false
	for _, v := range sel.Selections {
		if v.Purpose == "model" && v.Provider != nil && *v.Provider == "fake-oauth" {
			found = v.LocalLogin != nil && *v.LocalLogin == "missing"
		}
	}
	if !found {
		t.Fatalf("选择里应带 localLogin：%+v", sel.Selections)
	}
}

// 平台 Agent 经 HTTP 用插件提供方：目录里的原因只看授权；没授权时不能选、不能登录（409）；授权后选用并在 Agent 自己的
// 模型凭证里登录，问题、设备码与手工输入经操作视图与回答接口交给控制台；成功后凭证是这个 Agent 的版本，状态里带 localLogin。
func TestPlatformAgentPluginLoginAPI(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	plugin := "grokish"
	reg := modelcatalog.NewRegistry(e.api.Models.Snapshot())
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: plugin, Provider: modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off", Input: []string{"text"}}}}}})
	e.api.Models, e.api.Config.Models, e.api.ProjectCredentials.Auth.Models = reg, reg, reg
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: plugin, DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	e.api.ProjectCredentials.Plugins = &modelauth.PluginLogins{Helper: httpPluginHelper{}, States: &providerstate.Manager{Root: filepath.Join(t.TempDir(), "states")},
		Extensions: func(context.Context, string) ([]string, error) { return []string{"/x/index.ts"}, nil }}
	missing := "missing"
	e.api.ProjectCredentials.LocalLogin = func(_ context.Context, provider string) (*string, error) {
		if provider == "fake-oauth" {
			return &missing, nil
		}
		return nil, nil
	}

	agent := e.platformAgent("平台订阅助手")
	path := "/agents/" + agent.ID.String()
	reason := func() *string {
		t.Helper()
		list := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", "/model-providers?agentId="+agent.ID.String(), nil))
		for _, p := range list.Providers {
			if p.ID == "fake-oauth" {
				return p.UnavailableReason
			}
		}
		t.Fatal("目录里没有插件提供方")
		return nil
	}
	if r := reason(); r == nil || *r != "agent_not_granted" {
		t.Fatalf("没授权：%v", r)
	}
	e.expect(422, "PATCH", path, map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}})
	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, plugin, true, nil); err != nil {
		t.Fatal(err)
	}
	if r := reason(); r != nil {
		t.Fatalf("授权后（平台 Agent 不看项目启用）：%v", *r)
	}
	e.expect(200, "PATCH", path, map[string]any{"model": map[string]string{"provider": "fake-oauth", "modelId": "fake-1"}})
	version := func() int64 {
		t.Helper()
		return decode[apitypes.Agent](t, e.expect(200, "GET", path, nil)).ConfigVersion
	}

	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, plugin, false, nil); err != nil {
		t.Fatal(err)
	}
	refused := decode[apitypes.ErrorBody](t, e.expect(409, "POST", path+"/model-auth/operations", map[string]any{"provider": "fake-oauth", "expectedConfigVersion": version()}))
	if refused.Error.Code != "plugin_not_enabled" {
		t.Fatalf("没授权不能登录：%+v", refused)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, agent.ID, plugin, true, nil); err != nil {
		t.Fatal(err)
	}
	e.expect(422, "POST", path+"/model-auth/operations", map[string]any{"provider": "nope", "expectedConfigVersion": version()})

	start := decode[modelauth.Operation](t, e.expect(200, "POST", path+"/model-auth/operations", map[string]any{"provider": "fake-oauth", "expectedConfigVersion": version()}))
	op := path + "/model-auth/operations/" + start.ID.String()
	poll := func(ok func(modelauth.Operation) bool) modelauth.Operation {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			o := decode[modelauth.Operation](t, e.expect(200, "GET", op, nil))
			if ok(o) {
				return o
			}
		}
		t.Fatal("等待登录状态超时")
		return modelauth.Operation{}
	}
	o := poll(func(o modelauth.Operation) bool { return o.Prompt != nil })
	if o.Prompt.Kind != "select" || o.Provider != "fake-oauth" {
		t.Fatalf("问题：%+v", o)
	}
	e.expect(422, "POST", op+"/input", map[string]any{"promptId": "1", "value": "pigeon"})
	answered := decode[modelauth.State](t, e.expect(200, "POST", op+"/input", map[string]any{"promptId": "1", "value": "device"}))
	if answered.Operation == nil || answered.Operation.ID != start.ID {
		t.Fatalf("回答后的状态：%+v", answered)
	}
	o = poll(func(o modelauth.Operation) bool { return o.DeviceCode != nil && o.Prompt != nil })
	if o.Status != "awaiting_authorization" || o.DeviceCode.UserCode != "ABCD-EFGH" || o.DeviceCode.ExpiresAt == nil || o.Prompt.Kind != "manual_code" {
		t.Fatalf("设备码与手工输入：%+v", o)
	}
	state := decode[modelauth.State](t, e.expect(200, "GET", path+"/model-auth", nil))
	if state.Operation == nil || state.Operation.DeviceCode == nil || state.LocalLogin == nil || *state.LocalLogin != "missing" {
		t.Fatalf("状态视图：%+v", state)
	}
	e.expect(200, "POST", op+"/input", map[string]any{"promptId": "2", "value": "pasted-callback"})
	o = poll(func(o modelauth.Operation) bool { return o.Status == "succeeded" })
	if o.DeviceCode != nil || o.Prompt != nil {
		t.Fatalf("终态不带设备码与问题：%+v", o)
	}
	e.expect(422, "POST", op+"/input", map[string]any{"promptId": "2", "value": "late"})
	state = decode[modelauth.State](t, e.expect(200, "GET", path+"/model-auth", nil))
	if c := state.Credential; c == nil || c.Provider == nil || *c.Provider != "fake-oauth" || c.AuthMethod == nil || *c.AuthMethod != "oauth" || c.VersionID == nil {
		t.Fatalf("登录后的凭证：%+v", state.Credential)
	}
}
