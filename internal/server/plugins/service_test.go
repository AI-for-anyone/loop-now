package plugins_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/plugins"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

type env struct {
	t        *testing.T
	st       *store.Store
	svc      *plugins.Service
	official string
	project  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := pgtest.NewStore(t)
	dc, err := st.EnsureDefaults(context.Background(), "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, official: t.TempDir(), project: dc.Project.ID}
	e.svc = &plugins.Service{Store: st, Root: filepath.Join(t.TempDir(), "plugins"), OfficialDir: e.official, GitBin: "git", NodeBin: nodeBin(),
		APIURL: "http://127.0.0.1:1/api/v1", PlatformVersion: "2026.11", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := e.svc.Init(); err != nil {
		t.Fatal(err)
	}
	return e
}

// nodeBin 返回本机的 node（npm 来源的测试需要）；没有时为空。
func nodeBin() string {
	p := filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// plugin 写一个插件目录：平台半边、Agent 扩展、Skill、CLI 目录、MCP 服务器、配置与账号都有。
func plugin(t *testing.T, dir, id, version string) string {
	t.Helper()
	pkg := map[string]any{
		"name": "@test/" + id, "version": version, "description": "测试插件 " + id,
		"pi": map[string]any{"extensions": []any{"./agent/index.ts"}, "skills": []any{"./skills"}},
		"loopNow": map[string]any{
			"id": id, "displayName": strings.ToUpper(id), "platform": "./platform/index.ts",
			"events": []any{"inbox.received", "agent.created"}, "permissions": []any{"agents.read", "state"},
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "x-secret": true},
				"channel": map[string]any{"type": "string", "x-scope": "project"}, "key": map[string]any{"type": "string", "x-secret": true, "x-scope": "project"}}},
			"accounts": []any{
				map[string]any{"kind": "lark", "label": "飞书", "multiple": false, "fields": map[string]any{"type": "object", "required": []any{"appId"},
					"properties": map[string]any{"appId": map[string]any{"type": "string"}, "secret": map[string]any{"type": "string", "x-secret": true}}}},
				map[string]any{"kind": "mail", "label": "邮箱", "multiple": true, "fields": map[string]any{"type": "object"}},
			},
			"mcpServers": map[string]any{"browser": map[string]any{"command": "node", "args": []any{"./mcp/server.js", "--data", "${LOOP_NOW_PLUGIN_DATA}/profiles/${LOOP_NOW_AGENT_ID}"},
				"env": map[string]any{"HOME_DIR": "${LOOP_NOW_PLUGIN_ROOT}", "OTHER": "${KEEP_ME}"}}},
			"bin": []any{"./bin"},
		},
	}
	b, _ := json.Marshal(pkg)
	for f, content := range map[string]string{"package.json": string(b), "agent/index.ts": "export default () => {}", "platform/index.ts": "export default () => {}",
		"skills/demo/SKILL.md": "---\nname: demo\n---\n", "mcp/server.js": "", "bin/tool": "#!/bin/sh\n"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func (e *env) install(src plugins.Source, confirm bool) plugins.View {
	e.t.Helper()
	pv, err := e.svc.Preview(context.Background(), plugins.PreviewRequest{Source: &src})
	if err != nil {
		e.t.Fatal(err)
	}
	v, err := e.svc.Install(context.Background(), plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: confirm}, "")
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func code(err error) string {
	if ae, ok := apperr.As(err); ok {
		return ae.Code
	}
	return ""
}

// 本地目录原地使用、须确认；官方来源直接装，复制到 <Root>/<id>/<version>/；同版本同内容的更新沿用原目录，内容不同须改版本号；
// 卸载删除平台的安装目录（本地目录不删），数据目录只在要求时删除。
func TestInstallLocalAndBuiltin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	local := plugin(t, t.TempDir(), "devkit", "0.1.0")
	pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: local}})
	if err != nil || pv.Trusted || pv.Source.Location != local || pv.Manifest.ID != "devkit" || pv.InstalledVersion != nil {
		t.Fatalf("本地预览：%+v %v", pv, err)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID}, ""); code(err) != apperr.CodePluginConfirmationRequired {
		t.Fatalf("非官方来源须确认：%v", err)
	}
	v, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, "")
	if err != nil || v.Status != "installed" || !v.HasPlatform || v.Source.Kind != "local" {
		t.Fatalf("本地安装：%+v %v", v, err)
	}
	d, err := e.svc.Get(ctx, "devkit")
	if err != nil || d.InstallDir != local || len(d.Versions) != 1 || !d.Versions[0].Current {
		t.Fatalf("本地来源原地使用：%+v %v", d, err)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNotFound {
		t.Fatalf("预览用过即失效：%v", err)
	}
	pv, _ = e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: local}})
	if pv.InstalledVersion == nil || *pv.InstalledVersion != "0.1.0" {
		t.Fatalf("预览应给出已装版本：%+v", pv)
	}
	if _, err := e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID, Confirm: true}, ""); code(err) != apperr.CodeNameConflict {
		t.Fatalf("同 id 再装应提示更新：%v", err)
	}

	official := plugin(t, filepath.Join(e.official, "hello"), "hello", "1.0.0")
	if got := e.svc.BuiltinSources(); len(got) != 1 || got[0].ID != "hello" || got[0].Version != "1.0.0" {
		t.Fatalf("官方插件列表：%+v", got)
	}
	v = e.install(plugins.Source{Kind: "builtin", Location: "hello"}, false)
	v1 := filepath.Join(e.svc.Root, "hello", "1.0.0")
	if d, _ := e.svc.Get(ctx, "hello"); d.InstallDir != v1 || v.Source.Kind != "builtin" {
		t.Fatalf("官方来源复制到版本目录：%+v", d)
	}
	if _, err := os.Stat(filepath.Join(v1, "platform/index.ts")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "hello", "data")); err != nil {
		t.Fatalf("数据目录：%v", err)
	}
	update := func() error {
		pv, err := e.svc.Preview(ctx, plugins.PreviewRequest{PluginID: "hello"})
		if err != nil {
			return err
		}
		_, err = e.svc.Install(ctx, plugins.InstallInput{PreviewID: pv.PreviewID}, "hello")
		return err
	}
	if err := update(); err != nil {
		t.Fatalf("同版本同内容可以重装：%v", err)
	}
	if err := os.WriteFile(filepath.Join(official, "platform/index.ts"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := update(); code(err) != apperr.CodePluginVersionConflict {
		t.Fatalf("同版本不同内容应拒绝：%v", err)
	}
	plugin(t, official, "hello", "1.1.0")
	if err := update(); err != nil {
		t.Fatal(err)
	}
	d, _ = e.svc.Get(ctx, "hello")
	if d.Plugin.Version != "1.1.0" || len(d.Versions) != 2 || d.InstallDir != filepath.Join(e.svc.Root, "hello", "1.1.0") {
		t.Fatalf("更新后：%+v", d)
	}
	if _, err := os.Stat(v1); err != nil {
		t.Fatalf("旧版本目录保留给已经启动的 Pi：%v", err)
	}
	if err := e.svc.Uninstall(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(v1); !os.IsNotExist(err) {
		t.Fatalf("卸载应删除版本目录：%v", err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "hello", "data")); err != nil {
		t.Fatalf("数据目录默认保留：%v", err)
	}
	if err := e.svc.Uninstall(ctx, "devkit", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(local, "package.json")); err != nil {
		t.Fatalf("本地来源的目录不能删：%v", err)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "devkit")); !os.IsNotExist(err) {
		t.Fatalf("deleteData 后插件目录应已删空：%v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "builtin", Location: "nope"}}); code(err) != apperr.CodeNotFound {
		t.Fatalf("不存在的官方插件：%v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "local", Location: "relative/dir"}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("本地目录须是绝对路径：%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(e.svc.Root, ".staging")); len(entries) != 0 {
		t.Fatalf("暂存区应已清空：%v", entries)
	}
}

// git 来源按分支克隆，记下提交，去掉 .git；npm 来源（本机有 node 时）打包后解开。两者都是非官方来源。
func TestInstallGitAndNPM(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	repo := plugin(t, t.TempDir(), "gitty", "2.0.0")
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v：%v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(repo, "init", "-q", "-b", "main")
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	run(repo, "checkout", "-q", "-b", "release")
	commit := run(repo, "rev-parse", "HEAD")
	ref := "release"
	v := e.install(plugins.Source{Kind: "git", Location: "file://" + repo, Ref: &ref}, true)
	if v.Source.Commit == nil || *v.Source.Commit != commit || *v.Source.Ref != "release" {
		t.Fatalf("git 来源：%+v", v.Source)
	}
	dir := filepath.Join(e.svc.Root, "gitty", "2.0.0")
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("应去掉 .git：%v", err)
	}
	if _, err := e.svc.Preview(ctx, plugins.PreviewRequest{Source: &plugins.Source{Kind: "git", Location: "file:///nonexistent/repo"}}); code(err) != apperr.CodePluginSourceFailed {
		t.Fatalf("取不到的 git 来源：%v", err)
	}
	if e.svc.NodeBin == "" {
		t.Log("本机没有 node，跳过 npm 来源")
		return
	}
	pkg := plugin(t, t.TempDir(), "npmmy", "3.1.0")
	v = e.install(plugins.Source{Kind: "npm", Location: pkg}, true)
	if v.Source.NpmVersion == nil || *v.Source.NpmVersion != "3.1.0" {
		t.Fatalf("npm 来源：%+v", v.Source)
	}
	if _, err := os.Stat(filepath.Join(e.svc.Root, "npmmy", "3.1.0", "platform/index.ts")); err != nil {
		t.Fatalf("npm 包应解开到版本目录：%v", err)
	}
}

// 全局配置：secret 只写不回显，按 schema 校验，版本不一致 412，保存后发 plugin.config_changed；项目配置只能写 x-scope project 的字段。
func TestConfigAccountsAndGrants(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "cfg", "1.0.0")}, true)
	if _, err := e.st.Pool.Exec(ctx, `UPDATE plugins SET manifest=jsonb_set(manifest::jsonb,'{events}','["plugin.config_changed","plugin.enabled"]')::json`); err != nil {
		t.Fatal(err)
	}
	tok := "s3cret"
	c, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"url": "https://x"}, Secrets: map[string]*string{"token": &tok}})
	if err != nil || c.Revision != 1 || c.SecretsSet[0] != "token" || c.Values["token"] != nil {
		t.Fatalf("保存全局配置：%+v %v", c, err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{}, ExpectedRevision: 0}); code(err) != apperr.CodePreconditionFailed {
		t.Fatalf("版本不一致：%v", err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"url": 3}, ExpectedRevision: 1}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("类型不对：%v", err)
	}
	if _, err := e.svc.PutConfig(ctx, "cfg", plugins.ConfigInput{Values: map[string]any{"token": "x"}, ExpectedRevision: 1}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("secret 放在 values 里：%v", err)
	}
	var global string
	if err := e.st.Pool.QueryRow(ctx, `SELECT secrets->>'token' FROM plugin_config WHERE plugin_id='cfg' AND project_id IS NULL`).Scan(&global); err != nil || global != tok {
		t.Fatalf("secret 已保存：%q %v", global, err)
	}
	if _, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: true, Values: map[string]any{"url": "x"}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("非项目字段不能在项目覆盖：%v", err)
	}
	key := "pk"
	pp, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: true, Values: map[string]any{"channel": "#ops"}, Secrets: map[string]*string{"key": &key}})
	// 上面用 jsonb_set 改过清单，属性的先后已被 jsonb 重排；这里只核对有哪些字段（先后见 httpapi 的 TestPluginSchemaOrder）。
	if err != nil || !pp.Enabled || pp.Config.Revision != 1 || pp.Config.SecretsSet[0] != "key" || !slices.Equal(slices.Sorted(slices.Values(pp.ProjectFields)), []string{"channel", "key"}) {
		t.Fatalf("项目配置：%+v %v", pp, err)
	}
	var events []string
	rows, _ := e.st.Pool.Query(ctx, `SELECT type||':'||coalesce(project_id::text,'-') FROM plugin_events ORDER BY id`)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		events = append(events, s)
	}
	rows.Close()
	want := []string{"plugin.config_changed:-", "plugin.enabled:" + e.project.String(), "plugin.config_changed:" + e.project.String()}
	if !slices.Equal(events, want) {
		t.Fatalf("事件：%v", events)
	}

	// 账号：单账号种类只能有一个；secret 字段不回显；required 生效。
	sec := map[string]string{"secret": "lark-secret"}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "主", Values: map[string]any{}, Secrets: sec}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("缺少 required 字段：%v", err)
	}
	lark, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "主", Values: map[string]any{"appId": "cli_1"}, Secrets: sec})
	if err != nil || lark.Values["secret"] != nil || lark.SecretsSet[0] != "secret" || lark.Values["appId"] != "cli_1" {
		t.Fatalf("添加账号：%+v %v", lark, err)
	}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "lark", Name: "副", Values: map[string]any{"appId": "cli_2"}}); code(err) != apperr.CodeNameConflict {
		t.Fatalf("单账号种类：%v", err)
	}
	if _, err := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "github", Name: "x", Values: map[string]any{}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("未声明的种类：%v", err)
	}
	mail1, _ := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "mail", Name: "a", Values: map[string]any{"addr": "a@x"}})
	mail2, _ := e.svc.CreateAccount(ctx, e.project, "cfg", plugins.AccountCreate{Kind: "mail", Name: "b", Values: map[string]any{"addr": "b@x"}})
	lark, err = e.svc.UpdateAccount(ctx, e.project, "cfg", lark.ID, plugins.AccountUpdate{Name: "主账号", Values: map[string]any{"appId": "cli_9"}, ExpectedRevision: 1})
	if err != nil || lark.Revision != 2 || lark.SecretsSet[0] != "secret" {
		t.Fatalf("修改账号（secret 保留）：%+v %v", lark, err)
	}

	// 授权：项目 Agent 要项目启用；平台 Agent 不受限制、没有账号。
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(e.project), "worker", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	platform, _ := e.st.CreateAgent(ctx, store.PlatformScope(), "patrol", "anthropic", "m", "")
	if _, err := e.svc.PutAgentPlugin(ctx, agent.ID, "cfg", plugins.AgentInput{Granted: true, AccountIDs: []uuid.UUID{lark.ID, mail1.ID, mail2.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, platform.ID, "cfg", plugins.AgentInput{Granted: true, AccountIDs: []uuid.UUID{lark.ID}}); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("平台 Agent 没有项目账号：%v", err)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, platform.ID, "cfg", plugins.AgentInput{Granted: true}); err != nil {
		t.Fatal(err)
	}

	// 交给 Pi 的东西：路径为绝对路径，mcp.json 的占位与相对路径已换成这个插件与 Agent 的值，配置不含 secret，账号按种类注入。
	assets, err := e.svc.AgentAssets(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := e.svc.Get(ctx, "cfg")
	root := d.InstallDir
	if !slices.Equal(assets.Extensions, []string{filepath.Join(root, "agent/index.ts")}) || !slices.Equal(assets.Skills, []string{filepath.Join(root, "skills")}) ||
		!slices.Equal(assets.Bin, []string{filepath.Join(root, "bin")}) {
		t.Fatalf("路径：%+v", assets)
	}
	var mcp struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(assets.MCPServers["cfg-browser"], &mcp); err != nil {
		t.Fatal(err)
	}
	if mcp.Command != "node" || mcp.Args[0] != filepath.Join(root, "mcp/server.js") || mcp.Args[2] != d.DataDir+"/profiles/"+agent.ID.String() ||
		mcp.Env["HOME_DIR"] != root || mcp.Env["OTHER"] != "${KEEP_ME}" {
		t.Fatalf("mcp：%+v", mcp)
	}
	var cfg map[string]any
	_ = json.Unmarshal([]byte(assets.Env["LOOP_NOW_PLUGIN_CFG_CONFIG"]), &cfg)
	if cfg["url"] != "https://x" || cfg["channel"] != "#ops" || cfg["token"] != nil || cfg["key"] != nil {
		t.Fatalf("配置变量：%v", assets.Env)
	}
	var mails []map[string]any
	_ = json.Unmarshal([]byte(assets.Env["LOOP_NOW_ACCOUNTS_MAIL"]), &mails)
	if assets.Env["LOOP_NOW_ACCOUNT_LARK_APPID"] != "cli_9" || assets.Env["LOOP_NOW_ACCOUNT_LARK_SECRET"] != "lark-secret" || len(mails) != 2 || mails[0]["name"] == nil {
		t.Fatalf("账号变量：%v", assets.Env)
	}
	if err := e.st.DeletePluginAccount(ctx, e.project, "cfg", mail1.ID); err != nil {
		t.Fatal(err)
	}
	views, _ := e.svc.AgentPlugins(ctx, agent.ID)
	if len(views) != 1 || !views[0].Granted || !views[0].Available || len(views[0].AccountIDs) != 2 {
		t.Fatalf("删除账号后授权里的引用应移除：%+v", views)
	}
	// 项目停用：授权保留但不再挂载；新的授权被拒绝。全局停用：什么都不挂载。
	if _, err := e.svc.PutProject(ctx, e.project, "cfg", plugins.ProjectInput{Enabled: false, Values: map[string]any{"channel": "#ops"}, ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.svc.AgentAssets(ctx, agent.ID); len(a.Extensions) != 0 {
		t.Fatalf("项目停用后不挂载：%+v", a)
	}
	if _, err := e.svc.PutAgentPlugin(ctx, agent.ID, "cfg", plugins.AgentInput{Granted: true}); code(err) != apperr.CodePluginNotEnabled {
		t.Fatalf("项目未启用时不能授权：%v", err)
	}
	if a, _ := e.svc.AgentAssets(ctx, platform.ID); len(a.Extensions) != 1 {
		t.Fatalf("平台 Agent 照常挂载：%+v", a)
	}
	if _, err := e.svc.PutSettings(ctx, store.PluginSettings{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if a, _ := e.svc.AgentAssets(ctx, platform.ID); len(a.Extensions) != 0 || len(a.Env) != 0 {
		t.Fatalf("全局停用后不挂载：%+v", a)
	}
}

// 宿主加载的插件按安装顺序给出；每个插件在加载前才签发令牌，只吊销它自己的旧令牌。
func TestSpecsIssueTokensPerPlugin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "first", "1.0.0")}, true)
	e.install(plugins.Source{Kind: "local", Location: plugin(t, t.TempDir(), "second", "1.0.0")}, true)
	specs, err := e.svc.Specs(ctx)
	if err != nil || len(specs) != 2 || specs[0].ID != "first" || specs[1].ID != "second" || !slices.Equal(specs[0].Permissions, []string{"agents.read", "state"}) {
		t.Fatalf("specs：%+v %v", specs, err)
	}
	load := func(i int) map[string]any {
		raw, err := specs[i].Prepare(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		return p
	}
	a, b := load(0), load(1)
	if a["entry"] != filepath.Join(a["root"].(string), "platform/index.ts") || a["apiUrl"] != e.svc.APIURL || !strings.HasPrefix(a["token"].(string), auth.PluginPrefix) {
		t.Fatalf("load 载荷：%v", a)
	}
	again := load(0)
	live := func(token string) bool {
		_, id, _, _ := auth.ParseToken(token)
		rec, err := e.st.LookupPluginToken(ctx, id)
		return err == nil && !rec.Revoked
	}
	if live(a["token"].(string)) || !live(again["token"].(string)) || !live(b["token"].(string)) {
		t.Fatal("重新加载一个插件只应吊销它自己的旧令牌")
	}
}
