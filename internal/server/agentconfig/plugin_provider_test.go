package agentconfig_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
)

// Model providers registered by plugins: a new Agent cannot pick one directly; changing a project Agent requires the
// project to have the plugin enabled and the Agent to be granted it, each reason reported separately; a platform Agent
// has no project enable layer and can pick it once granted.
func TestPluginProviderNeedsGrant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := modelcatalog.NewRegistry(e.svc.Models.Snapshot())
	captured := modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "grokish", Provider: captured}})
	e.svc.Models = reg
	providers, _ := json.Marshal(modelcatalog.Capture{Providers: []modelcatalog.CapturedProvider{captured}})
	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: providers}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.CreateAgent(ctx, e.scope, "新建", "fake-oauth", "fake-1", ""); !isField(err, "model.provider", "Create the Agent first") {
		t.Fatalf("新建 Agent 不能直接选插件提供方：%v", err)
	}
	a := e.agent(t)
	set := func() error {
		p, m := "fake-oauth", "fake-1"
		_, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Provider: &p, ModelID: &m})
		return err
	}
	if err := set(); !isField(err, "model.provider", "Enable the plugin in the project first") {
		t.Fatalf("项目未启用：%v", err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "fake-oauth"); reason != store.PluginUnusableNotEnabled {
		t.Fatalf("原因：%q", reason)
	}
	if _, _, err := e.st.SaveProjectPlugin(ctx, e.scope.ProjectID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
		t.Fatal(err)
	}
	if err := set(); !isField(err, "model.provider", "Grant the plugin to this Agent") {
		t.Fatalf("未授权：%v", err)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, a.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := set(); err != nil {
		t.Fatalf("已授权后可以选：%v", err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "fake-oauth"); reason != "" {
		t.Fatalf("已授权：%q", reason)
	}
	if plugin, reason, _ := e.svc.PluginProviderUnusable(ctx, a.ID, "anthropic"); plugin != "" || reason != "" {
		t.Fatal("内置提供方不受影响")
	}

	if _, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "平台新建", "fake-oauth", "fake-1", ""); !isField(err, "model.provider", "Create the Agent first") {
		t.Fatalf("新建平台 Agent 也不能直接选插件提供方：%v", err)
	}
	platform, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "平台助手", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, platform.ID, "fake-oauth"); reason != store.PluginUnusableNotGranted {
		t.Fatalf("平台 Agent 没授权：%q", reason)
	}
	setPlatform := func() error {
		p, m := "fake-oauth", "fake-1"
		_, err := e.svc.UpdateAgent(ctx, platform.ID, agentconfig.AgentPatch{Provider: &p, ModelID: &m})
		return err
	}
	if err := setPlatform(); !isField(err, "model.provider", "Grant the plugin to this Agent") {
		t.Fatalf("平台 Agent 没授权不能选：%v", err)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, platform.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	if _, reason, _ := e.svc.PluginProviderUnusable(ctx, platform.ID, "fake-oauth"); reason != "" {
		t.Fatalf("平台 Agent 授权后：%q", reason)
	}
	if err := setPlatform(); err != nil {
		t.Fatalf("平台 Agent 授权后可以选（不看项目启用）：%v", err)
	}
}

func isField(err error, field, contains string) bool {
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 {
		return false
	}
	return ae.Fields[0].Field == field && strings.Contains(ae.Fields[0].Message, contains)
}

// A platform Agent starts Pi with a plugin provider it logged in to itself: the model binding in the execution snapshot
// is its own version, the state is looked up by (plugin, provider, version), and plugins are mounted by grant; it
// follows the same linking and failure rules as a project Agent (linkProviderState in executor/local).
func TestPlatformAgentPluginProviderSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := modelcatalog.NewRegistry(e.svc.Models.Snapshot())
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "grokish", Provider: modelcatalog.CapturedProvider{ID: "fake-oauth", Name: "Fake", OAuth: true,
		Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}}})
	e.svc.Models = reg
	coordinator := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coordinator, reg, modelauth.ServiceOptions{}, nil)
	t.Cleanup(func() { _ = auth.Close(context.Background()) })
	e.svc.ModelAuth = auth
	type call struct {
		plugin, provider string
		version          uuid.UUID
	}
	var calls []call
	e.svc.ProviderState = func(_ context.Context, plugin, provider string, version uuid.UUID) (*agentconfig.ProviderState, error) {
		calls = append(calls, call{plugin, provider, version})
		return &agentconfig.ProviderState{PluginID: plugin, VersionID: version, Dir: "/states/" + version.String()}, nil
	}
	e.svc.PluginAssets = func(_ context.Context, id uuid.UUID) (agentconfig.PluginAssets, error) {
		grants, err := e.st.AgentPluginGrants(ctx, id)
		out := agentconfig.PluginAssets{}
		for _, g := range grants {
			out.IDs = append(out.IDs, g.PluginID)
		}
		return out, err
	}

	if err := e.st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/x"}`), Version: "0.1.0", Manifest: json.RawMessage(`{}`), InstallDir: "/x"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	a, err := e.st.CreateAgent(ctx, store.PlatformScope(), "平台助手", "fake-oauth", "fake-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.st.SaveAgentPluginGrant(ctx, a.ID, "grokish", true, nil); err != nil {
		t.Fatal(err)
	}
	a, _ = e.st.GetAgent(ctx, a.ID)
	op, err := e.st.NewModelAuthOperation(ctx, a.ID, "fake-oauth", a.ConfigVersion, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	credential, _ := modelcredential.PluginStateOf("grokish")
	plain, _ := credential.Encode()
	version := uuid.Must(uuid.NewV7())
	if _, v, err := e.st.CompletePluginModelAuthOperation(ctx, op.ID, version, plain); err != nil || v == nil {
		t.Fatalf("提交登录：%v %v", v, err)
	}

	snap, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ContextProjectID != nil || snap.ModelBinding == nil || snap.ModelBinding.VersionID != version || snap.ModelCredential.Kind() != modelcredential.PluginState {
		t.Fatalf("模型绑定是 Agent 自己的版本：%+v", snap.ModelBinding)
	}
	if snap.ProviderState == nil || snap.ProviderState.VersionID != version || len(calls) != 1 || calls[0] != (call{"grokish", "fake-oauth", version}) {
		t.Fatalf("状态目录：%+v %v", snap.ProviderState, calls)
	}
	if !slices.Contains(snap.Plugins.IDs, "grokish") {
		t.Fatalf("授权的插件挂载：%+v", snap.Plugins)
	}
	// After the credential is cleared there is no model credential, so no state directory is looked up.
	if err := auth.Clear(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if snap, err = e.svc.ExecutionSnapshot(ctx, a.ID); err == nil && snap.ProviderState != nil {
		t.Fatalf("清除后不再取状态目录：%+v", snap.ProviderState)
	}
}
