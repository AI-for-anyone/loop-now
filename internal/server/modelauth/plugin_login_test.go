package modelauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// fakePluginHelper 模拟插件扩展的订阅登录：在 HOME 下写自己的状态，问一个选择题，给授权地址与设备码，返回不含密钥的标记。
type fakePluginHelper struct {
	fail  error
	homes []string
}

const fakeEntry = `{"type":"oauth","access":"vault-marker","refresh":"vault-marker","expires":9007199254740991}`

func (f *fakePluginHelper) Login(ctx context.Context, req PluginLoginRequest, ui PluginLoginUI, replies <-chan cm.LoginInput) ([]byte, error) {
	f.homes = append(f.homes, req.Home)
	if f.fail != nil {
		return nil, f.fail
	}
	if len(req.Extensions) != 1 || req.Extensions[0] != "/plugins/grokish/0.1.0/src/index.ts" || req.Provider != "fake-oauth" {
		return nil, errors.New("请求不对")
	}
	dir := filepath.Join(req.Home, ".pi", "fake-oauth")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(`{"token":"real-token-in-state"}`), 0o644); err != nil {
		return nil, err
	}
	ui.Prompt(cm.LoginPrompt{ID: "1", Kind: "select", Message: "Pick", Options: []cm.LoginPromptOption{{ID: "browser", Label: "Browser"}, {ID: "device", Label: "Device"}}})
	select {
	case in := <-replies:
		if in.PromptID != "1" || in.Value != "device" {
			return nil, errors.New("回答不对")
		}
		ui.PromptClosed("1")
	case <-ctx.Done():
		return nil, ErrAuthCancelled
	}
	if err := ui.AuthURL("https://accounts.example.test/authorize?state=s"); err != nil {
		return nil, err
	}
	if err := ui.DeviceCode("ABCD-EFGH", "https://accounts.example.test/device", 10*time.Minute); err != nil {
		return nil, err
	}
	return []byte(fakeEntry), nil
}

type pluginEnv struct {
	st      *store.Store
	p       *ProjectService
	svc     *Service
	helper  *fakePluginHelper
	states  *providerstate.Manager
	project uuid.UUID
}

func newPluginEnv(t *testing.T, enabled bool) *pluginEnv {
	t.Helper()
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InstallPlugin(ctx, store.Plugin{ID: "grokish", DisplayName: "Grokish", Source: json.RawMessage(`{"kind":"local","location":"/plugins/grokish/0.1.0"}`), Version: "0.1.0",
		Manifest: json.RawMessage(`{}`), InstallDir: "/plugins/grokish/0.1.0"}, "d", store.PluginVersionExtras{Providers: json.RawMessage(`{"providers":[],"errors":[]}`)}); err != nil {
		t.Fatal(err)
	}
	if enabled {
		if _, _, err := st.SaveProjectPlugin(ctx, dc.Project.ID, "grokish", true, store.PluginConfigUpdate{Apply: func(c store.PluginConfigRow) (store.PluginConfigRow, error) { return c, nil }}); err != nil {
			t.Fatal(err)
		}
	}
	plugin := "grokish"
	models := modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "openai-codex", AuthMethods: []string{"oauth"}},
		{ID: "fake-oauth", Name: "Fake", Source: "plugin", PluginID: &plugin, AuthMethods: []string{"oauth"}, Models: []modelcatalog.Model{{ID: "fake-1", ThinkingLevels: []string{"off"}, DefaultThinkingLevel: "off"}}}})
	templates, _ := clitemplate.Load("")
	svc := NewService(st, nil, nil, models, ServiceOptions{LoginTimeout: 5 * time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { closeAuth(t, svc) })
	e := &pluginEnv{st: st, svc: svc, helper: &fakePluginHelper{}, states: &providerstate.Manager{Root: filepath.Join(t.TempDir(), "provider-states")}, project: dc.Project.ID}
	e.p = NewProjectService(svc, templates, 0)
	e.p.Plugins = &PluginLogins{Helper: e.helper, States: e.states, Extensions: func(_ context.Context, id string) ([]string, error) {
		if id != "grokish" {
			return nil, apperr.NotFound("插件")
		}
		return []string{"/plugins/grokish/0.1.0/src/index.ts"}, nil
	}}
	return e
}

func (e *pluginEnv) wait(t *testing.T, id uuid.UUID, ok func(cm.Operation) bool) cm.LoginState {
	t.Helper()
	var last cm.LoginState
	waitAuth(t, func() bool {
		s, err := e.p.GetLogin(context.Background(), e.project, id)
		last = s
		return err == nil && ok(s.Operation)
	})
	return last
}

func fieldOf(err error) string {
	if ae, ok := apperr.As(err); ok && len(ae.Fields) == 1 {
		return ae.Fields[0].Field
	}
	return ""
}

// 插件提供方登录：问题经操作视图给出、只能回答当前那个；授权地址与设备码按通用规则显示；成功后建项目账号（oauth、已登录），
// 版本只记下插件，状态目录以版本 ID 命名、收紧权限、带 auth.json；重登用全新目录。
func TestPluginProviderLogin(t *testing.T) {
	e := newPluginEnv(t, true)
	ctx := context.Background()
	name := "订阅一"
	start, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	id := start.Operation.ID
	s := e.wait(t, id, func(o cm.Operation) bool { return o.Prompt != nil })
	if q := s.Operation.Prompt; q.Kind != "select" || len(q.Options) != 2 || s.Operation.Provider != "fake-oauth" {
		t.Fatalf("问题：%+v", s.Operation)
	}
	if _, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "9", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("不是当前问题：%v", err)
	}
	if _, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "1", Value: "pigeon"}); fieldOf(err) != "value" {
		t.Fatalf("不是选项：%v", err)
	}
	if after, err := e.p.Input(ctx, e.project, id, cm.LoginInput{PromptID: "1", Value: "device"}); err != nil || after.Operation.Prompt != nil {
		t.Fatalf("回答后问题清空：%+v %v", after, err)
	}
	done := e.wait(t, id, func(o cm.Operation) bool { return terminal(o.Status) })
	if done.Operation.Status != "succeeded" || done.Account == nil || done.Operation.DeviceCode != nil || done.Operation.AuthorizationURL != nil {
		t.Fatalf("登录结果：%+v", done)
	}
	account := *done.Account
	if account.Provider == nil || *account.Provider != "fake-oauth" || account.AuthMethod == nil || *account.AuthMethod != "oauth" || account.Status != "logged_in" || account.CurrentVersionID == nil {
		t.Fatalf("账号：%+v", account)
	}
	version, err := e.st.GetModelAuthVersion(ctx, *account.CurrentVersionID)
	if err != nil {
		t.Fatal(err)
	}
	c, err := modelcredential.Decode(version.Secret)
	if err != nil || c.Kind() != modelcredential.PluginState || c.Plugin() != "grokish" {
		t.Fatalf("版本只记下插件：%v %v", c.Kind(), err)
	}
	dir := e.states.Dir(*account.CurrentVersionID)
	auth, err := os.ReadFile(filepath.Join(dir, providerstate.AuthFile))
	if err != nil || string(auth) != `{"fake-oauth":`+fakeEntry+`}` {
		t.Fatalf("auth.json：%s %v", auth, err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, ".pi", "fake-oauth"): 0o700, filepath.Join(dir, ".pi", "fake-oauth", "accounts.json"): 0o600, filepath.Join(dir, providerstate.AuthFile): 0o600} {
		// 状态目录里是订阅令牌：各平台都要只有当前用户能访问（Windows 看 DACL），Unix 上再核对确切的权限位。
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !privatefs.IsPrivate(path, info) || (runtime.GOOS != "windows" && info.Mode().Perm() != mode) {
			t.Fatalf("%s 不是只有当前用户能访问：%v", path, info.Mode())
		}
	}
	if pending, _ := filepath.Glob(filepath.Join(e.states.Root, ".pending-*")); len(pending) != 0 {
		t.Fatalf("暂存目录应已改名：%v", pending)
	}

	// 进行中的视图：等待设备码时给出（这里用列表确认同一项目可以不带 provider 列出）。
	list, err := e.p.ListLogins(ctx, e.project, "", nil)
	if err != nil || len(list.Operations) != 0 {
		t.Fatalf("已结束的不在进行中列表：%+v %v", list, err)
	}

	// 重登：新版本、新目录，旧目录留给清理器。
	rev := account.Revision
	again, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", AccountID: &account.ID, ExpectedRevision: &rev})
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, again.Operation.ID, func(o cm.Operation) bool { return o.Prompt != nil })
	if _, err := e.p.Input(ctx, e.project, again.Operation.ID, cm.LoginInput{PromptID: "1", Value: "device"}); err != nil {
		t.Fatal(err)
	}
	relogin := e.wait(t, again.Operation.ID, func(o cm.Operation) bool { return terminal(o.Status) })
	if relogin.Operation.Status != "succeeded" || *relogin.Account.CurrentVersionID == *account.CurrentVersionID || relogin.Account.ID != account.ID {
		t.Fatalf("重登：%+v", relogin)
	}
	if e.helper.homes[0] == e.helper.homes[1] {
		t.Fatal("重登要用全新状态目录")
	}
	if _, err := os.Stat(e.states.Dir(*relogin.Account.CurrentVersionID)); err != nil {
		t.Fatal(err)
	}

	// 清理：只保留账号的当前版本（运行中的 Pi 与未结束任务在用的由调用方加进来），旧版本的目录删掉。
	keep, err := e.st.ProviderStateVersionsInUse(ctx)
	if err != nil || !keep[*relogin.Account.CurrentVersionID] || keep[*account.CurrentVersionID] {
		t.Fatalf("要保留的版本：%v %v", keep, err)
	}
	removed, err := e.states.Sweep(func() (map[uuid.UUID]bool, error) { return keep, nil }, true)
	if err != nil || len(removed) != 1 || removed[0] != *account.CurrentVersionID {
		t.Fatalf("清理：%v %v", removed, err)
	}
	if _, err := os.Stat(e.states.Dir(*account.CurrentVersionID)); !os.IsNotExist(err) {
		t.Fatal("旧版本目录应已删除")
	}
	// 删除账号：当前版本也不再保留。
	if _, err := e.p.Delete(ctx, e.project, account.ID, relogin.Account.Revision); err != nil {
		t.Fatal(err)
	}
	if keep, _ = e.st.ProviderStateVersionsInUse(ctx); keep[*relogin.Account.CurrentVersionID] {
		t.Fatal("删除账号后不再保留")
	}
	if removed, _ = e.states.Sweep(func() (map[uuid.UUID]bool, error) { return keep, nil }, false); len(removed) != 1 {
		t.Fatalf("删除账号后清理：%v", removed)
	}
}

func TestPluginProviderLoginRefusalsAndFailures(t *testing.T) {
	ctx := context.Background()
	name := "订阅"
	off := newPluginEnv(t, false)
	if _, err := off.p.Start(ctx, off.project, cm.LoginStart{Provider: "fake-oauth", Name: &name}); !apperr.Is(err, apperr.CodePluginNotEnabled) {
		t.Fatalf("项目没有启用插件：%v", err)
	}
	if _, err := off.p.Start(ctx, off.project, cm.LoginStart{Provider: "nope", Name: &name}); fieldOf(err) != "provider" {
		t.Fatalf("不在目录里：%v", err)
	}

	e := newPluginEnv(t, true)
	e.helper.fail = ErrExtensionLoad
	start, err := e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	s := e.wait(t, start.Operation.ID, func(o cm.Operation) bool { return terminal(o.Status) })
	if s.Operation.Status != "failed" || s.Operation.ErrorCode == nil || *s.Operation.ErrorCode != "extension_load_failed" || s.Operation.ErrorMessage == nil {
		t.Fatalf("扩展加载失败：%+v", s.Operation)
	}
	if entries, _ := os.ReadDir(e.states.Root); len(entries) != 0 {
		t.Fatalf("失败时删除暂存目录：%v", entries)
	}

	e.helper.fail = nil
	start, err = e.p.Start(ctx, e.project, cm.LoginStart{Provider: "fake-oauth", Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	e.wait(t, start.Operation.ID, func(o cm.Operation) bool { return o.Prompt != nil })
	end, err := e.p.CancelLogin(ctx, e.project, start.Operation.ID)
	if err != nil || end.Operation.Status != "cancelled" || end.Operation.Prompt != nil {
		t.Fatalf("取消：%+v %v", end, err)
	}
	if _, err := e.p.Input(ctx, e.project, start.Operation.ID, cm.LoginInput{PromptID: "1", Value: "device"}); fieldOf(err) != "promptId" {
		t.Fatalf("取消后不能再回答：%v", err)
	}
	waitAuth(t, func() bool { entries, _ := os.ReadDir(e.states.Root); return len(entries) == 0 })
}
