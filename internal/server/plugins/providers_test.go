package plugins_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/plugins"
)

// fakeCapture 按扩展所在插件目录给出注册的提供方，不运行任何代码；记下每次被读取的目录。
type fakeCapture struct {
	byDir map[string]modelcatalog.Capture
	dirs  []string
}

func (f *fakeCapture) capture(_ context.Context, dir string, extensions []string) (modelcatalog.Capture, error) {
	f.dirs = append(f.dirs, dir)
	return f.byDir[dir], nil
}

func provider(id string, oauth bool) modelcatalog.CapturedProvider {
	return modelcatalog.CapturedProvider{ID: id, Name: "Name " + id, Extension: "agent/index.ts", OAuth: oauth,
		Models: []modelcatalog.Model{{ID: "m1", Name: "M1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off", Input: []string{"text"}}}}
}

// 安装确认后读取插件注册的提供方：带 OAuth 的新提供方并入模型目录（source=plugin、带 pluginId），
// 不带 OAuth 的、与内置同 id 的只记录；另一个插件注册同一 id 时不安装（409）；卸载后从目录里消失。
func TestPluginProvidersReachCatalog(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	base := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "anthropic", Models: []modelcatalog.Model{{ID: "c", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}})
	reg := modelcatalog.NewRegistry(base)
	e.svc.Models = reg
	dirA, dirB := t.TempDir(), t.TempDir()
	plugin(t, dirA, "grokish", "0.1.0")
	plugin(t, dirB, "copycat", "0.1.0")
	override := provider("anthropic", true)
	override.OverridesBuiltin = true
	fc := &fakeCapture{byDir: map[string]modelcatalog.Capture{
		dirA: {Providers: []modelcatalog.CapturedProvider{provider("fake-oauth", true), provider("plain", false), override}, Errors: []modelcatalog.CaptureError{{Path: "agent/other.ts", Error: "boom"}}},
		dirB: {Providers: []modelcatalog.CapturedProvider{provider("fake-oauth", true)}},
	}}
	e.svc.CaptureProviders = fc.capture

	pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: dirA}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.dirs) != 0 {
		t.Fatalf("预览时不能运行插件代码：%v", fc.dirs)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); err != nil {
		t.Fatal(err)
	}
	p, ok := reg.Provider("fake-oauth")
	if !ok || p.Source != "plugin" || p.PluginID == nil || *p.PluginID != "grokish" || p.Name != "Name fake-oauth" || !slices.Equal(p.AuthMethods, []string{"oauth"}) || !reg.HasModel("fake-oauth", "m1") {
		t.Fatalf("带 OAuth 的提供方应进目录：%+v %v", p, ok)
	}
	if _, ok := reg.Provider("plain"); ok {
		t.Fatal("不带 OAuth 的提供方不进目录")
	}
	if a, _ := reg.Provider("anthropic"); a.Source == "plugin" {
		t.Fatal("覆盖内置提供方的不进目录")
	}
	d, err := e.svc.Get(ctx, "grokish")
	if err != nil || len(d.Providers) != 3 || d.ProvidersError != nil || len(d.ProviderErrors) != 1 || d.ProviderErrors[0].Path != "agent/other.ts" {
		t.Fatalf("详情里的提供方：%+v %v", d, err)
	}
	in := map[string]bool{}
	for _, v := range d.Providers {
		in[v.ID] = v.InCatalog
	}
	if !in["fake-oauth"] || in["plain"] || in["anthropic"] {
		t.Fatalf("inCatalog：%v", in)
	}

	pv, _ = e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: dirB}})
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNameConflict {
		t.Fatalf("另一个插件注册同一提供方应 409：%v", err)
	}
	if _, err := e.st.GetPlugin(ctx, "copycat"); err == nil {
		t.Fatal("冲突时不应安装")
	}

	// 服务重启：从数据库重新并入。
	reg2 := modelcatalog.NewRegistry(base)
	e.svc.Models = reg2
	if err := e.svc.PublishProviders(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Provider("fake-oauth"); !ok {
		t.Fatal("重启后应从数据库并入")
	}
	// models.json 换掉 Pi 目录部分时插件提供方保留。
	reg2.Replace(modelcatalog.New("1.4.0", nil, nil))
	if _, ok := reg2.Provider("fake-oauth"); !ok {
		t.Fatal("换 Pi 目录后插件提供方应保留")
	}
	if err := e.svc.Uninstall(ctx, "grokish", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Provider("fake-oauth"); ok {
		t.Fatal("卸载后应从目录里消失")
	}
	if _, err := os.Stat(dirA); err != nil {
		t.Fatal("本地来源目录不删")
	}
}

// 读取失败不阻止安装，原因随版本保存在 providersError。
func TestPluginProviderCaptureFailureKeepsInstall(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Models = modelcatalog.NewRegistry(modelcatalog.New("1.4.0", nil, nil))
	e.svc.CaptureProviders = func(context.Context, string, []string) (modelcatalog.Capture, error) {
		return modelcatalog.Capture{}, os.ErrDeadlineExceeded
	}
	dir := plugin(t, t.TempDir(), "flaky", "0.1.0")
	e.install(plugins.Source{Kind: "local", Location: dir}, true)
	d, err := e.svc.Get(ctx, "flaky")
	if err != nil || d.ProvidersError == nil || len(d.Providers) != 0 {
		t.Fatalf("读取失败应记原因：%+v %v", d, err)
	}
}
