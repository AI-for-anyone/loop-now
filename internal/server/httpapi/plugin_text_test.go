package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// localizeManifest 把插件目录清单里的 displayName 与 description 改成按语言给出的对象。
func localizeManifest(t *testing.T, dir string, name, description map[string]any) {
	t.Helper()
	patchManifest(t, dir, func(ln map[string]any) { ln["displayName"] = name })
	path := filepath.Join(dir, "package.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pkg map[string]any
	if err = json.Unmarshal(b, &pkg); err != nil {
		t.Fatal(err)
	}
	pkg["description"] = description
	b, _ = json.Marshal(pkg)
	if err = os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 插件的显示名与描述可以按语言给出（plugins/SPEC.md §2）：安装结果、列表、详情、项目与 Agent 的插件列表、官方插件来源、
// 市场条目都原样返回对象（经契约校验），服务端不按语言翻译；插件表的显示名（插件发件人名的兜底）取 zh-CN。
// 显示名或描述不合规则的市场条目让那份市场报错。
func TestPluginLocalizedText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	name := map[string]any{"en-US": "Bridge", "zh-CN": "桥接"}
	description := map[string]any{"en-US": "Relays messages", "zh-CN": "转发消息"}
	dir := writePluginDir(t, "bridge")
	localizeManifest(t, dir, name, description)
	same := func(what string, got map[string]any) {
		t.Helper()
		if !reflect.DeepEqual(got["displayName"], name) {
			t.Fatalf("%s 的 displayName：%v", what, got["displayName"])
		}
		if d, ok := got["description"]; ok && !reflect.DeepEqual(d, description) {
			t.Fatalf("%s 的 description：%v", what, d)
		}
	}
	same("安装结果", e.installPlugin(dir))
	detail := decode[map[string]any](t, e.expect(200, "GET", "/plugins/bridge", nil))
	same("详情", detail["plugin"].(map[string]any))
	same("清单", detail["manifest"].(map[string]any))
	same("列表", decode[map[string]any](t, e.expect(200, "GET", "/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	project := e.dc.Project.ID.String()
	same("项目插件", decode[map[string]any](t, e.expect(200, "GET", "/projects/"+project+"/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	_, agent := e.newAgent("助手")
	e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	same("Agent 插件", decode[map[string]any](t, e.expect(200, "GET", agent+"/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	var stored string
	if err := e.st.Pool.QueryRow(ctx, `SELECT display_name FROM plugins WHERE id='bridge'`).Scan(&stored); err != nil || stored != "桥接" {
		t.Fatalf("插件表的显示名取 zh-CN：%q %v", stored, err)
	}

	official := t.TempDir()
	hello := writePluginDir(t, "hello")
	localizeManifest(t, hello, name, description)
	if err := os.Rename(hello, filepath.Join(official, "hello")); err != nil {
		t.Fatal(err)
	}
	e.api.Plugins.OfficialDir = official
	same("官方插件来源", decode[map[string]any](t, e.expect(200, "GET", "/plugin-sources/builtin", nil))["sources"].([]any)[0].(map[string]any))

	files := map[string]any{
		"/good.json": map[string]any{"plugins": []any{map[string]any{"id": "relay", "source": "npm:@test/relay", "displayName": name, "description": description, "verified": true}}},
		"/bad.json":  map[string]any{"plugins": []any{map[string]any{"id": "relay", "source": "npm:@test/relay", "displayName": map[string]any{"english": "Relay"}}}},
	}
	market := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(files[r.URL.Path]) }))
	t.Cleanup(market.Close)
	e.api.Plugins.OfficialMarket, e.api.Plugins.MarketClient = market.URL+"/good.json", market.Client()
	e.expect(200, "PUT", "/plugin-settings", map[string]any{"disabled": false, "markets": []any{market.URL + "/bad.json"}})
	list := decode[map[string][]map[string]any](t, e.expect(200, "GET", "/plugin-markets", nil))["markets"]
	same("市场条目", list[0]["plugins"].([]any)[0].(map[string]any))
	if msg, _ := list[1]["error"].(string); !strings.Contains(msg, "displayName 须是字符串，或以语言标签") {
		t.Fatalf("不合规则的市场条目：%v", list[1])
	}
}
