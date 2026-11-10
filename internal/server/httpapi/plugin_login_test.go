package httpapi_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: plugin, DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", json.RawMessage(`{"providers":[],"errors":[]}`), nil); err != nil {
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
}
