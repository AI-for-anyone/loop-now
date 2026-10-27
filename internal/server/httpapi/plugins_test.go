package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/server/apitypes"
)

// writePluginDir 写一个本地插件目录；permissions 为平台半边的权限范围。
func writePluginDir(t *testing.T, id string, permissions ...string) string {
	t.Helper()
	dir := t.TempDir()
	pkg := map[string]any{"name": "@test/" + id, "version": "1.0.0",
		"pi": map[string]any{"extensions": []any{"./agent.ts"}},
		"loopNow": map[string]any{"id": id, "platform": "./platform.ts", "events": []any{"agent.created"}, "permissions": permissions,
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "x-secret": true},
				"channel": map[string]any{"type": "string", "x-scope": "project"}}},
			"accounts": []any{map[string]any{"kind": "mail", "label": "邮箱", "multiple": true, "fields": map[string]any{"type": "object",
				"properties": map[string]any{"addr": map[string]any{"type": "string"}, "password": map[string]any{"type": "string", "x-secret": true}}}}}}}
	b, _ := json.Marshal(pkg)
	for name, content := range map[string]string{"package.json": string(b), "agent.ts": "", "platform.ts": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// installPlugin 经接口预览并确认安装本地插件。
func (e *env) installPlugin(dir string) map[string]any {
	e.t.Helper()
	pv := decode[map[string]any](e.t, e.expect(201, "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "local", "location": dir}}))
	return decode[map[string]any](e.t, e.expect(201, "POST", "/plugins", map[string]any{"previewId": pv["previewId"], "confirm": true}))
}

// 插件管理接口走一遍：预览与确认安装、详情、全局与项目配置、账号、Agent 授权、日志、投递、状态、宿主与全局设置、卸载；
// 请求与响应都经契约校验。
func TestPluginsAPI(t *testing.T) {
	e := newEnv(t)
	project := e.dc.Project.ID.String()
	dir := writePluginDir(t, "notify", "state")
	pv := decode[map[string]any](t, e.expect(201, "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "local", "location": dir}}))
	if pv["trusted"] != false || pv["manifest"].(map[string]any)["id"] != "notify" {
		t.Fatalf("预览：%v", pv)
	}
	e.expect(409, "POST", "/plugins", map[string]any{"previewId": pv["previewId"]})
	p := decode[map[string]any](t, e.expect(201, "POST", "/plugins", map[string]any{"previewId": pv["previewId"], "confirm": true}))
	if p["id"] != "notify" || p["status"] != "installed" || p["hasPlatform"] != true {
		t.Fatalf("安装：%v", p)
	}
	if l := decode[map[string]any](t, e.expect(200, "GET", "/plugins", nil)); len(l["plugins"].([]any)) != 1 {
		t.Fatalf("列表：%v", l)
	}
	if d := decode[map[string]any](t, e.expect(200, "GET", "/plugins/notify", nil)); d["installDir"] != dir || len(d["versions"].([]any)) != 1 {
		t.Fatalf("详情：%v", d)
	}
	e.expect(404, "GET", "/plugins/missing", nil)

	c := decode[map[string]any](t, e.expect(200, "PUT", "/plugins/notify/config", map[string]any{"values": map[string]any{"url": "https://hook"}, "secrets": map[string]any{"token": "t0k"}, "expectedRevision": 0}))
	if c["revision"] != float64(1) || c["secretsSet"].([]any)[0] != "token" {
		t.Fatalf("全局配置：%v", c)
	}
	e.expect(412, "PUT", "/plugins/notify/config", map[string]any{"values": map[string]any{}, "expectedRevision": 0})
	if c = decode[map[string]any](t, e.expect(200, "GET", "/plugins/notify/config", nil)); c["values"].(map[string]any)["token"] != nil {
		t.Fatalf("secret 不回显：%v", c)
	}
	pp := decode[map[string]any](t, e.expect(200, "PUT", "/projects/"+project+"/plugins/notify", map[string]any{"enabled": true, "values": map[string]any{"channel": "#ops"}, "expectedRevision": 0}))
	if pp["enabled"] != true || pp["projectFields"].([]any)[0] != "channel" {
		t.Fatalf("项目启用：%v", pp)
	}
	e.expect(422, "PUT", "/projects/"+project+"/plugins/notify", map[string]any{"enabled": true, "values": map[string]any{"url": "x"}, "expectedRevision": 1})
	if l := decode[map[string]any](t, e.expect(200, "GET", "/projects/"+project+"/plugins", nil)); l["plugins"].([]any)[0].(map[string]any)["enabled"] != true {
		t.Fatalf("项目插件列表：%v", l)
	}

	acc := decode[map[string]any](t, e.expect(201, "POST", "/projects/"+project+"/plugins/notify/accounts", map[string]any{"kind": "mail", "name": "值班", "values": map[string]any{"addr": "a@x"}, "secrets": map[string]any{"password": "pw"}}))
	if acc["secretsSet"].([]any)[0] != "password" || acc["values"].(map[string]any)["password"] != nil {
		t.Fatalf("账号：%v", acc)
	}
	accPath := "/projects/" + project + "/plugins/notify/accounts/" + acc["id"].(string)
	acc = decode[map[string]any](t, e.expect(200, "PUT", accPath, map[string]any{"name": "值班2", "values": map[string]any{"addr": "b@x"}, "expectedRevision": 1}))
	e.expect(412, "PUT", accPath, map[string]any{"name": "x", "values": map[string]any{}, "expectedRevision": 1})
	_, agent := e.newAgent("通知员")
	agentID := strings.TrimPrefix(agent, "/agents/")
	g := decode[map[string]any](t, e.expect(200, "PUT", agent+"/plugins/notify", map[string]any{"granted": true, "accountIds": []any{acc["id"]}}))
	if g["granted"] != true || g["available"] != true || len(g["accountIds"].([]any)) != 1 {
		t.Fatalf("授权：%v", g)
	}
	if l := decode[map[string]any](t, e.expect(200, "GET", agent+"/plugins", nil)); len(l["plugins"].([]any)) != 1 {
		t.Fatalf("Agent 插件：%v", l)
	}
	e.expect(204, "DELETE", accPath, nil)
	if l := decode[map[string]any](t, e.expect(200, "GET", "/projects/"+project+"/plugins/notify/accounts", nil)); len(l["accounts"].([]any)) != 0 {
		t.Fatalf("删除账号：%v", l)
	}

	// 投递：项目启用后新建的两个 Agent（通知员、新来的）各产生一条 agent.created 给这个插件。
	e.newAgent("新来的")
	dl := decode[map[string]any](t, e.expect(200, "GET", "/plugins/notify/deliveries?projectId="+project, nil))
	items := dl["deliveries"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["eventType"] != "agent.created" || items[0].(map[string]any)["status"] != "pending" || items[0].(map[string]any)["durationMs"] != nil {
		t.Fatalf("投递：%v", dl)
	}
	e.expect(200, "GET", "/plugins/notify/deliveries?status=failed&limit=5", nil)
	e.expect(422, "GET", "/plugins/notify/logs?limit=0", nil, "X-Contract-Invalid-Request", "1")
	if l := decode[map[string]any](t, e.expect(200, "GET", "/plugins/notify/logs", nil)); len(l["entries"].([]any)) != 0 || l["nextBefore"] != nil {
		t.Fatalf("日志：%v", l)
	}
	if err := e.st.PluginStateSet(context.Background(), "notify", nil, "deliveries/1", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if st := decode[map[string]any](t, e.expect(200, "GET", "/plugins/notify/state?prefix=deliveries/", nil)); len(st["entries"].([]any)) != 1 {
		t.Fatalf("状态：%v", st)
	}

	host := decode[map[string]any](t, e.expect(200, "GET", "/plugin-host", nil))
	if host["configured"] != false || host["today"].(map[string]any)["delivered"] != float64(0) {
		t.Fatalf("宿主：%v", host)
	}
	e.expectError(409, "plugin_not_enabled", "POST", "/plugin-host/restart", nil)
	if s := decode[map[string]any](t, e.expect(200, "PUT", "/plugin-settings", map[string]any{"disabled": true, "markets": []any{"https://example.com/market.git"}})); s["disabled"] != true {
		t.Fatalf("设置：%v", s)
	}
	if s := decode[map[string]any](t, e.expect(200, "GET", "/plugin-settings", nil)); len(s["markets"].([]any)) != 1 {
		t.Fatalf("设置：%v", s)
	}
	if b := decode[map[string]any](t, e.expect(200, "GET", "/plugin-sources/builtin", nil)); len(b["sources"].([]any)) != 0 {
		t.Fatalf("官方插件：%v", b)
	}
	e.expect(204, "DELETE", "/plugins/notify?deleteData=true", nil)
	e.expect(404, "GET", "/plugins/notify", nil)
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Fatalf("本地来源的目录不能删：%v", err)
	}
	_ = agentID
}

// 插件令牌只能调用标了 x-plugin-scope 且在范围内的操作；路径里的项目、Agent、任务须属于启用了插件的项目（平台 Agent 除外）；
// 拒绝记入插件日志；写操作的审计操作者为 plugin；吊销的令牌 401；插件令牌在对外端口上无效，其他令牌在控制台上照旧 403。
func TestPluginTokenScopes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.installPlugin(writePluginDir(t, "watch", "agents.read", "agents.write"))
	e.expect(200, "PUT", "/projects/"+e.dc.Project.ID.String()+"/plugins/watch", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	other := decode[apitypes.Project](t, e.expect(201, "POST", "/projects", map[string]any{"name": "别的项目"}))
	_, inProject := e.newAgent("项目内")
	outside := decode[apitypes.Agent](t, e.expect(201, "POST", "/projects/"+other.ID.String()+"/agents", map[string]any{"name": "项目外", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	platform := e.platformAgent("平台")
	token := func() string {
		specs, err := e.api.Plugins.Specs(ctx)
		if err != nil || len(specs) != 1 {
			t.Fatal(specs, err)
		}
		raw, err := specs[0].Prepare(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(raw, &p)
		return p.Token
	}
	tok := token()
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }
	e.expect(200, "GET", inProject, nil, bearer(tok)...)
	e.expect(200, "GET", "/agents/"+platform.ID.String(), nil, bearer(tok)...)
	e.console().expectError(403, "forbidden", "GET", "/agents/"+outside.ID.String(), nil, bearer(tok)...)
	e.console().expectError(403, "forbidden", "GET", "/plugins", nil, bearer(tok)...)
	e.console().expectError(403, "forbidden", "GET", "/external-identities", nil, bearer(tok)...)
	e.expect(200, "PATCH", inProject, map[string]any{"name": "改名"}, bearer(tok)...)
	var actor string
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_kind||'|'||actor_address FROM audit_log WHERE operation='updateAgent' ORDER BY at DESC LIMIT 1`).Scan(&actor); err != nil || actor != "plugin|plugin:watch" {
		t.Fatalf("审计：%q %v", actor, err)
	}
	var logged int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM plugin_host_log WHERE plugin_id='watch' AND line LIKE '调用%被拒绝%'`).Scan(&logged); err != nil || logged != 3 {
		t.Fatalf("拒绝应记入插件日志：%d %v", logged, err)
	}
	fresh := token()
	e.console().expectError(401, "unauthenticated", "GET", inProject, nil, bearer(tok)...)
	e.expect(200, "GET", inProject, nil, bearer(fresh)...)
	e.external(fresh).expectError(401, "unauthenticated", "GET", inProject, nil)
	_, ext := e.identity("ci-bot")
	e.console().expectError(403, "forbidden", "GET", "/context", nil, bearer(ext)...)
}

// 清单里 schema 的属性先后就是表单字段的先后：configSchema、accounts[].fields、actions[].fields 经安装、存储与各接口都保持清单的写法，
// projectFields 也按这个先后。
func TestPluginSchemaOrder(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	pkg := `{"name":"@test/ordered","version":"1.0.0","pi":{"extensions":["./agent.ts"]},"loopNow":{"id":"ordered","platform":"./platform.ts",
 "configSchema":{"type":"object","properties":{"url":{"type":"string","x-scope":"project"},"secret":{"type":"string","x-secret":true,"x-scope":"project"},
  "recipients":{"type":"string","x-scope":"project"},"limit":{"type":"integer"}}},
 "accounts":[{"kind":"mail","label":"邮箱","fields":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"string"}}}}],
 "actions":[{"id":"resend","label":"重发","where":["agent"],"fields":{"type":"object","properties":{"note":{"type":"string"},"after":{"type":"string"}}}}]}}`
	for name, content := range map[string]string{"package.json": pkg, "agent.ts": "", "platform.ts": ""} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inOrder := func(what string, body []byte, keys ...string) {
		t.Helper()
		last := -1
		for _, k := range keys {
			i := strings.Index(string(body), `"`+k+`":`)
			if i <= last {
				t.Fatalf("%s 中 %v 应按清单的先后：%s", what, keys, body)
			}
			last = i
		}
	}
	pv := e.expect(201, "POST", "/plugin-previews", map[string]any{"source": map[string]any{"kind": "local", "location": dir}})
	inOrder("预览", pv, "url", "secret", "recipients", "limit")
	inOrder("预览", pv, "zeta", "alpha")
	inOrder("预览", pv, "note", "after")
	e.expect(201, "POST", "/plugins", map[string]any{"previewId": decode[map[string]any](t, pv)["previewId"], "confirm": true})
	got := e.expect(200, "GET", "/plugins/ordered", nil)
	inOrder("插件详情", got, "url", "secret", "recipients", "limit")
	inOrder("插件详情", got, "zeta", "alpha")
	inOrder("插件详情", got, "note", "after")
	project := e.dc.Project.ID.String()
	list := decode[map[string][]map[string]any](t, e.expect(200, "GET", "/projects/"+project+"/plugins", nil))["plugins"]
	if len(list) != 1 || fmt.Sprint(list[0]["projectFields"]) != "[url secret recipients]" {
		t.Fatalf("projectFields 按清单的先后：%v", list)
	}
}
