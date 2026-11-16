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

// The fake host is in pluginhosttest; with LOOPNOW_FAKE_PLUGIN_HOST=1 only this test runs.
func TestFakeHostProcess(t *testing.T) {
	if os.Getenv(pluginhosttest.Env) != "1" {
		t.Skip("只在作为假宿主进程运行时执行")
	}
	pluginhosttest.Run()
	os.Exit(0)
}

// patchManifest changes loopNow in the package.json of a plugin directory.
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

// startFakeHost starts the plugin host with the fake host, waits until all ids are loaded, and returns the fake host's
// journal file.
func (e *env) startFakeHost(ids ...string) string {
	e.t.Helper()
	journal := filepath.Join(e.t.TempDir(), "journal.jsonl")
	e.startHost([]string{os.Args[0], "-test.run=^TestFakeHostProcess$"}, append(os.Environ(), pluginhosttest.Env+"=1", "FAKE_HOST_JOURNAL="+journal), ids...)
	return journal
}

// startHost starts the plugin host with command (tests have no event notifications, so delivery relies on
// short-interval polling) and waits until all ids are loaded.
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

// journalEntries reads the messages of one kind from the fake host's journal.
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

// Action buttons: an action in the manifest runs on the pages its where allows, input is validated against fields, the
// project must have the plugin enabled, and the plugin must be in the host; the plugin's response is returned as a
// notice; a plugin error gives 502, a timeout 504.
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
	if res["notice"] != "Ran resend" || res["url"] != "https://example.test/resend" {
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

// HTTP entry point: /hooks/<id>/… on the external port needs no token; loopback /api/v1/plugins/<id>/http/… is called
// with the console or the plugin's own token; method, path, query, headers and body go to the plugin (without
// Authorization and Cookie), and the plugin's status code, headers and body are returned as is (minus hop-by-hop
// headers). A body over 1 MiB gives 413, a plugin without an HTTP entry point 404, a plugin error 502, and more than 20
// calls per second per plugin 429.
func TestPluginHTTPIngress(t *testing.T) {
	e := newEnv(t)
	dir := writePluginDir(t, "bridge")
	patchManifest(t, dir, func(ln map[string]any) { ln["http"] = true })
	e.installPlugin(dir)
	e.installPlugin(writePluginDir(t, "plain"))
	journal := e.startFakeHost("bridge", "plain")
	e.external("") // start the external port
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

// pluginTokenFor returns a new token for a plugin (the same way the host does when loading it).
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

// Markets: the official market and the markets in the settings are merged, with installed versions and available
// updates marked; a market that fails to load only reports it in its own error. A preview from a market is fetched from
// the entry's source; entries from the official market need no confirmation, and the market address is recorded; an
// update re-reads the market.
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
			"displayName": "Gitty", "description": "示例", "verified": false, "version": "2.0.0",
			"providerState": map[string]any{"gitty-oauth": map[string]any{"paths": []any{".pi/gitty"}}}}}},
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
	if state, _ := json.Marshal(gitty["providerState"]); string(state) != `{"gitty-oauth":{"paths":[".pi/gitty"]}}` {
		t.Fatalf("市场条目的 providerState：%s", state)
	}
	if state, _ := json.Marshal(bridge["providerState"]); string(state) != `{}` {
		t.Fatalf("没有声明时为空对象：%s", state)
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
	// The package does not set loopNow.displayName: the market entry's display name is used, not the npm package name.
	if installed["displayName"] != "Gitty" {
		t.Fatalf("显示名应取市场条目：%v", installed["displayName"])
	}
	detail := decode[map[string]any](t, e.expect(200, "GET", "/plugins/gitty", nil))
	if state, _ := json.Marshal(detail["providerState"]); string(state) != `{"gitty-oauth":{"paths":[".pi/gitty"]}}` {
		t.Fatalf("安装后按市场条目保存 providerState：%s", state)
	}
	if paths, err := e.api.Plugins.ProviderStatePaths(context.Background(), "gitty", "gitty-oauth"); err != nil || len(paths) != 1 || paths[0] != ".pi/gitty" {
		t.Fatalf("ProviderStatePaths：%v %v", paths, err)
	}
	e.expectError(404, "not_found", "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "market", "market": market.URL + "/official.json", "id": "nope"}})
	e.expectError(422, "validation_failed", "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "git"}})
	files["/official.json"].(map[string]any)["plugins"].([]any)[0].(map[string]any)["displayName"] = map[string]any{"en-US": "Gitty 2", "zh-CN": "吉蒂"}
	update := decode[map[string]any](t, e.expect(201, "POST", "/plugin-previews", map[string]any{"pluginId": "gitty"}))
	if name, _ := json.Marshal(update["manifest"].(map[string]any)["displayName"]); string(name) != `{"en-US":"Gitty 2","zh-CN":"吉蒂"}` {
		t.Fatalf("更新时按新的市场条目重取显示名：%s", name)
	}
	if update["source"].(map[string]any)["market"] != market.URL+"/official.json" || update["installedVersion"] != "2.0.0" {
		t.Fatalf("更新时重新读取市场：%v", update)
	}
	if !strings.HasPrefix(market.URL, "https://") {
		t.Fatal("测试市场应为 https")
	}
}

// The host's accounts_list (driven by the fake host's probe-accounts action): a plugin that is loaded, declares
// accounts.read and that kind, and is enabled in the project reads the non-deleted accounts of that kind in this
// project, in the order they were added, with values including secret fields; any other case gets ok:false and a
// reason.
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
		{"bridge", other.ID.String(), "mail", "the plugin is not enabled in this project"},
		{"bridge", project, "sms", "the manifest does not declare account kind sms"},
		{"bridge", "x", "mail", "projectId is invalid"},
		{"plain", project, "mail", "the manifest does not declare the accounts.read permission"},
	} {
		if got := ask(c.plugin, c.project, c.kind); got["ok"] != false || got["error"] != c.want {
			t.Fatalf("%+v：%v", c, got)
		}
	}
}

// The config.projects the host gets only contain non-deleted projects that have the plugin enabled (an empty config
// when there is no project-level config); enabling or disabling the plugin in a project sends config_changed with the
// latest project list. Projects that only stored a config without enabling the plugin are not included.
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
