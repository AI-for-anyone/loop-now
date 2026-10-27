package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/pluginhost/pluginhosttest"
)

// 假宿主见 pluginhosttest；LOOPNOW_FAKE_PLUGIN_HOST=1 时只跑这个测试。
func TestFakeHostProcess(t *testing.T) {
	if os.Getenv(pluginhosttest.Env) != "1" {
		t.Skip("只在作为假宿主进程运行时执行")
	}
	pluginhosttest.Run()
	os.Exit(0)
}

// patchManifest 修改插件目录 package.json 里的 loopNow。
func patchManifest(t *testing.T, dir string, patch func(loopNow map[string]any)) {
	t.Helper()
	path := filepath.Join(dir, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]any
	if err := json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	patch(pkg["loopNow"].(map[string]any))
	b, _ = json.Marshal(pkg)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// startFakeHost 以假宿主拉起插件宿主，等 ids 都加载完成，返回假宿主的记录文件。
func (e *env) startFakeHost(ids ...string) string {
	e.t.Helper()
	journal := filepath.Join(e.t.TempDir(), "journal.jsonl")
	e.startHost([]string{os.Args[0], "-test.run=^TestFakeHostProcess$"}, append(os.Environ(), pluginhosttest.Env+"=1", "FAKE_HOST_JOURNAL="+journal), ids...)
	return journal
}

// startHost 以 command 拉起插件宿主（测试里没有事件通知，投递靠短间隔轮询），等 ids 都加载完成。
func (e *env) startHost(command, env []string, ids ...string) *pluginhost.Supervisor {
	e.t.Helper()
	sup := pluginhost.New(pluginhost.Options{
		Command: command, Dir: e.t.TempDir(), Env: env, PollInterval: 50 * time.Millisecond,
		Store: e.st, Specs: e.api.Plugins.Specs, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	e.api.Plugins.Host = sup
	sup.Start(false)
	e.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = sup.Stop(ctx)
	})
	deadline := time.Now().Add(10 * time.Second)
	for slices.ContainsFunc(ids, func(id string) bool { return !slices.Contains(sup.Status().Loaded, id) }) {
		if time.Now().After(deadline) {
			e.t.Fatalf("宿主没有加载 %v：%+v", ids, sup.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return sup
}

// journalEntries 读出假宿主记录里某类消息。
func journalEntries(t *testing.T, path, typ string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil && m["type"] == typ {
			out = append(out, m)
		}
	}
	return out
}

// 操作按钮：清单里的操作在 where 允许的页面上执行，input 按 fields 校验，所在项目须启用插件、插件须在宿主里；
// 插件的回应作为提示返回，插件报错 502、超时 504。
func TestPluginActions(t *testing.T) {
	e := newEnv(t)
	project := e.dc.Project.ID.String()
	dir := writePluginDir(t, "bridge", "agents.read")
	patchManifest(t, dir, func(ln map[string]any) {
		ln["actions"] = []any{map[string]any{"id": "resend", "label": "重发", "where": []any{"agent", "identity"},
			"fields": map[string]any{"type": "object", "required": []any{"note"}, "properties": map[string]any{"note": map[string]any{"type": "string"}, "mode": map[string]any{"type": "string"}}}}}
	})
	e.installPlugin(dir)
	agent, _ := e.newAgent("助手")
	act := func(where map[string]any, input map[string]any) map[string]any {
		return map[string]any{"where": where, "input": input}
	}
	onAgent := map[string]any{"agentId": agent.ID}

	e.expectError(409, "plugin_not_enabled", "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{"note": "x"}))
	e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	e.expectError(409, "plugin_not_enabled", "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{"note": "x"}))

	journal := e.startFakeHost("bridge")
	res := decode[map[string]any](t, e.expect(200, "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{"note": "再发一次"})))
	if res["notice"] != "已执行 resend" || res["url"] != "https://example.test/resend" {
		t.Fatalf("操作结果：%v", res)
	}
	got := journalEntries(t, journal, "action")
	if len(got) != 1 || got[0]["where"].(map[string]any)["agentId"] != agent.ID.String() || got[0]["input"].(map[string]any)["note"] != "再发一次" {
		t.Fatalf("交给插件的操作：%v", got)
	}
	ident, _ := e.identity("ci-bot")
	e.expect(200, "POST", "/plugins/bridge/actions/resend", act(map[string]any{"identityId": ident.ID}, map[string]any{"note": "x"}))
	e.expectError(422, "validation_failed", "POST", "/plugins/bridge/actions/resend", act(map[string]any{"projectId": project}, map[string]any{"note": "x"}))
	e.expectError(422, "validation_failed", "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{}))
	e.expectError(404, "not_found", "POST", "/plugins/bridge/actions/nope", act(onAgent, map[string]any{"note": "x"}))
	e.expectError(404, "not_found", "POST", "/plugins/bridge/actions/resend", act(map[string]any{"agentId": "01a17d40-0000-7000-8000-000000000000"}, map[string]any{"note": "x"}))
	e.expectError(502, "plugin_failed", "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{"note": "x", "mode": "fail"}))
	e.api.Plugins.RequestTimeout = 300 * time.Millisecond
	e.expectError(504, "plugin_timeout", "POST", "/plugins/bridge/actions/resend", act(onAgent, map[string]any{"note": "x", "mode": "hang"}))
}

// HTTP 入口：对外端口的 /hooks/<id>/… 不要令牌，回环的 /api/v1/plugins/<id>/http/… 由控制台或该插件的令牌调用；
// 方法、路径、查询、头与正文交给插件（不带 Authorization 与 Cookie），插件的状态码、头与正文原样返回（去掉逐跳头）。
// 正文超过 1 MiB 返回 413，没有 HTTP 入口的插件 404，插件报错 502，每个插件每秒超过 20 次返回 429。
func TestPluginHTTPIngress(t *testing.T) {
	e := newEnv(t)
	dir := writePluginDir(t, "bridge")
	patchManifest(t, dir, func(ln map[string]any) { ln["http"] = true })
	e.installPlugin(dir)
	e.installPlugin(writePluginDir(t, "plain"))
	journal := e.startFakeHost("bridge", "plain")
	e.external("") // 启动对外端口
	send := func(method, url string, body []byte, hdr ...string) (*http.Response, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		b, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(b, &out)
		return resp, out
	}

	resp, echo := send("POST", e.ext.URL+"/hooks/bridge/callback/lark?x=1", []byte("hello"), "X-Signature", "abc", "Authorization", "Bearer secret", "Cookie", "a=b")
	if resp.StatusCode != 202 || resp.Header.Get("X-Plugin") != "bridge" || resp.Header.Get("Connection") == "close" {
		t.Fatalf("对外入口回应：%d %v", resp.StatusCode, resp.Header)
	}
	if echo["via"] != "external" || echo["method"] != "POST" || echo["path"] != "/callback/lark" || echo["query"] != "x=1" || echo["body"] != "hello" ||
		echo["authorization"] != nil || echo["signature"].([]any)[0] != "abc" {
		t.Fatalf("交给插件的请求：%v", echo)
	}
	if h := journalEntries(t, journal, "http")[0]["headers"].(map[string]any); h["cookie"] != nil || h["x-signature"] == nil {
		t.Fatalf("请求头：%v", h)
	}
	if resp, echo = send("GET", e.srv.URL+"/api/v1/plugins/bridge/http", nil); resp.StatusCode != 202 || echo["via"] != "console" || echo["path"] != "/" {
		t.Fatalf("回环入口：%d %v", resp.StatusCode, echo)
	}
	tok := e.pluginTokenFor("bridge")
	if resp, _ = send("GET", e.srv.URL+"/api/v1/plugins/bridge/http/x", nil, "Authorization", "Bearer "+tok); resp.StatusCode != 202 {
		t.Fatalf("插件令牌调用自己的入口：%d", resp.StatusCode)
	}
	if resp, _ = send("GET", e.srv.URL+"/api/v1/plugins/plain/http/x", nil, "Authorization", "Bearer "+tok); resp.StatusCode != 403 {
		t.Fatalf("插件令牌调用别的插件的入口：%d", resp.StatusCode)
	}
	for _, c := range []struct {
		url    string
		body   []byte
		status int
		code   string
	}{
		{"/hooks/plain/x", nil, 404, "not_found"},
		{"/hooks/nobody/x", nil, 404, "not_found"},
		{"/hooks/bridge/fail", nil, 502, "plugin_failed"},
		{"/hooks/bridge/big", bytes.Repeat([]byte("a"), 1<<20+1), 413, "payload_too_large"},
	} {
		if resp, out := send("POST", e.ext.URL+c.url, c.body); resp.StatusCode != c.status || out["error"].(map[string]any)["code"] != c.code {
			t.Fatalf("%s：%d %v", c.url, resp.StatusCode, out)
		}
	}
	limited := 0
	for range 60 {
		if resp, out := send("POST", e.ext.URL+"/hooks/bridge/burst", nil); resp.StatusCode == 429 {
			if out["error"].(map[string]any)["code"] != "rate_limited" {
				t.Fatalf("429 的错误码：%v", out)
			}
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("每秒超过 20 次应返回 429")
	}
}

// pluginTokenFor 返回某个插件的新令牌（与宿主加载时的做法相同）。
func (e *env) pluginTokenFor(id string) string {
	e.t.Helper()
	specs, err := e.api.Plugins.Specs(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, s := range specs {
		if s.ID == id {
			raw, err := s.Prepare(context.Background())
			if err != nil {
				e.t.Fatal(err)
			}
			var p struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(raw, &p)
			return p.Token
		}
	}
	e.t.Fatalf("没有插件 %s", id)
	return ""
}

// 市场：汇总官方市场与设置里的市场，标出已装版本与可更新；读取失败的市场只在自己的 error 里说明。
// 从市场预览按条目的来源获取，官方市场的条目免确认，记下市场地址；更新时重新读取市场。
func TestPluginMarkets(t *testing.T) {
	e := newEnv(t)
	repo := t.TempDir()
	pkg, _ := json.Marshal(map[string]any{"name": "@test/gitty", "version": "2.0.0", "pi": map[string]any{"extensions": []any{"./agent.ts"}}, "loopNow": map[string]any{"id": "gitty"}})
	for name, content := range map[string]string{"package.json": string(pkg), "agent.ts": ""} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v：%v %s", args, err, out)
		}
	}
	files := map[string]any{
		"/official.json": map[string]any{"name": "官方", "plugins": []any{map[string]any{"id": "gitty", "source": "git:file://" + repo + "@main",
			"displayName": "Gitty", "description": "示例", "verified": false, "version": "2.0.0"}}},
		"/other.json": map[string]any{"plugins": []any{map[string]any{"id": "bridge", "source": "npm:@test/bridge", "displayName": "Bridge",
			"description": "", "verified": true, "version": "1.1.0"}}},
	}
	market := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f, ok := files[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(f)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(market.Close)
	e.api.Plugins.OfficialMarket, e.api.Plugins.MarketClient = market.URL+"/official.json", market.Client()
	e.installPlugin(writePluginDir(t, "bridge"))
	e.expect(200, "PUT", "/plugin-settings", map[string]any{"disabled": false, "markets": []any{market.URL + "/other.json", market.URL + "/missing.json", market.URL + "/official.json"}})

	list := decode[map[string][]map[string]any](t, e.expect(200, "GET", "/plugin-markets", nil))["markets"]
	if len(list) != 3 || list[0]["official"] != true || list[0]["name"] != "官方" || list[2]["error"] == nil || len(list[2]["plugins"].([]any)) != 0 {
		t.Fatalf("市场列表：%v", list)
	}
	bridge := list[1]["plugins"].([]any)[0].(map[string]any)
	gitty := list[0]["plugins"].([]any)[0].(map[string]any)
	if bridge["installedVersion"] != "1.0.0" || bridge["updateAvailable"] != true || gitty["installedVersion"] != nil || gitty["updateAvailable"] != false {
		t.Fatalf("已装与可更新：%v %v", bridge, gitty)
	}

	pv := decode[map[string]any](t, e.expect(201, "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "market", "market": market.URL + "/official.json", "id": "gitty"}}))
	src := pv["source"].(map[string]any)
	if pv["trusted"] != true || src["kind"] != "git" || src["market"] != market.URL+"/official.json" || src["commit"] == nil {
		t.Fatalf("市场预览：%v", pv)
	}
	installed := decode[map[string]any](t, e.expect(201, "POST", "/plugins", map[string]any{"previewId": pv["previewId"], "confirm": false}))
	if installed["version"] != "2.0.0" {
		t.Fatalf("从市场安装：%+v", installed)
	}
	e.expectError(404, "not_found", "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "market", "market": market.URL + "/official.json", "id": "nope"}})
	e.expectError(422, "validation_failed", "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "git"}})
	update := decode[map[string]any](t, e.expect(201, "POST", "/plugin-previews", map[string]any{"pluginId": "gitty"}))
	if update["source"].(map[string]any)["market"] != market.URL+"/official.json" || update["installedVersion"] != "2.0.0" {
		t.Fatalf("更新时重新读取市场：%v", update)
	}
	if !strings.HasPrefix(market.URL, "https://") {
		t.Fatal("测试市场应为 https")
	}
}

// 宿主的 accounts_list（经假宿主的 probe-accounts 操作驱动）：已加载、声明了 accounts.read 与该种类、在项目里启用了的插件
// 读到这个项目里该种类未删除的账号，按添加先后，values 含 secret 字段；其余情况回应 ok:false 与原因。
func TestPluginAccountsList(t *testing.T) {
	e := newEnv(t)
	project := e.dc.Project.ID.String()
	probe := map[string]any{"id": "probe-accounts", "label": "探查", "where": []any{"global"}}
	for id, perms := range map[string][]string{"bridge": {"accounts.read"}, "plain": nil} {
		dir := writePluginDir(t, id, perms...)
		patchManifest(t, dir, func(ln map[string]any) { ln["actions"] = []any{probe} })
		e.installPlugin(dir)
	}
	e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	accounts := "/projects/" + project + "/plugins/bridge/accounts"
	for _, name := range []string{"值班", "已删除", "备份"} {
		acc := decode[map[string]any](t, e.expect(201, "POST", accounts, map[string]any{"kind": "mail", "name": name, "values": map[string]any{"addr": name + "@x"}, "secrets": map[string]any{"password": "pw-" + name}}))
		if name == "已删除" {
			e.expect(204, "DELETE", accounts+"/"+acc["id"].(string), nil)
		}
	}
	other := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "别的项目"}))
	e.startFakeHost("bridge", "plain")
	ask := func(plugin, projectID, kind string) map[string]any {
		t.Helper()
		res := decode[map[string]any](t, e.expect(200, "POST", "/plugins/"+plugin+"/actions/probe-accounts", map[string]any{"where": map[string]any{}, "input": map[string]any{"projectId": projectID, "kind": kind}}))
		var reply map[string]any
		if err := json.Unmarshal([]byte(res["notice"].(string)), &reply); err != nil {
			t.Fatalf("回应：%v", res)
		}
		return reply
	}
	got := ask("bridge", project, "mail")
	list, _ := got["value"].([]any)
	if got["ok"] != true || len(list) != 2 {
		t.Fatalf("账号：%v", got)
	}
	first, second := list[0].(map[string]any), list[1].(map[string]any)
	if first["name"] != "值班" || second["name"] != "备份" || first["kind"] != "mail" || first["values"].(map[string]any)["password"] != "pw-值班" || first["values"].(map[string]any)["addr"] != "值班@x" {
		t.Fatalf("按添加先后，values 含 secret 字段：%v", list)
	}
	for _, c := range []struct{ plugin, project, kind, want string }{
		{"bridge", other.ID.String(), "mail", "插件没有在这个项目里启用"},
		{"bridge", project, "sms", "清单没有声明账号种类 sms"},
		{"bridge", "x", "mail", "projectId 无效"},
		{"plain", project, "mail", "清单没有声明 accounts.read 权限"},
	} {
		if got := ask(c.plugin, c.project, c.kind); got["ok"] != false || got["error"] != c.want {
			t.Fatalf("%+v：%v", c, got)
		}
	}
}

// 宿主拿到的 config.projects 只含启用了插件、未删除的项目（没有项目级配置时为空配置）；项目启用或停用插件时发 config_changed，
// 带最新的项目列表。只存了配置、没有启用的项目不在其中。
func TestPluginConfigProjects(t *testing.T) {
	e := newEnv(t)
	e.installPlugin(writePluginDir(t, "bridge"))
	journal := e.startFakeHost("bridge")
	other := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "别的项目"}))
	project := e.dc.Project.ID.String()
	last := func(typ string) []any {
		t.Helper()
		entries := journalEntries(t, journal, typ)
		if len(entries) == 0 {
			t.Fatalf("没有 %s", typ)
		}
		got, _ := entries[len(entries)-1]["projects"].([]any)
		return got
	}
	if got := last("load"); len(got) != 0 {
		t.Fatalf("没有启用的项目时为空：%v", got)
	}
	on := decode[map[string]any](t, e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0}))
	if got := last("config_changed"); len(got) != 1 || got[0] != project {
		t.Fatalf("启用后：%v", got)
	}
	e.expect(200, "PUT", "/projects/"+other.ID.String()+"/plugins/bridge", map[string]any{"enabled": false, "values": map[string]any{"channel": "c"}, "expectedRevision": 0})
	if got := last("config_changed"); len(got) != 1 || got[0] != project {
		t.Fatalf("只存配置不启用的项目不在其中：%v", got)
	}
	e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": false, "values": map[string]any{}, "expectedRevision": on["config"].(map[string]any)["revision"]})
	if got := last("config_changed"); len(got) != 0 {
		t.Fatalf("停用后：%v", got)
	}
}
