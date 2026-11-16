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

// localizeManifest turns displayName and description in the manifest of a plugin directory into per-language objects.
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

// A plugin's display name and description can be given per language (plugins/SPEC.md §2): the install result, list,
// details, the plugin lists of projects and Agents, official plugin sources and market entries all return the object as
// is (validated against the contract), the server does not translate by language; the display name in the plugins table
// (the fallback for the plugin's sender name) takes zh-CN. A market entry whose display name or description breaks the
// rules makes that market report an error.
func TestPluginLocalizedText(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	name := map[string]any{"en-US": "Bridge", "zh-CN": "桥接"}                   // check-english: zh (zh-CN value of a language-tag object)
	description := map[string]any{"en-US": "Relays messages", "zh-CN": "转发消息"} // check-english: zh (zh-CN value of a language-tag object)
	dir := writePluginDir(t, "bridge")
	localizeManifest(t, dir, name, description)
	same := func(what string, got map[string]any) {
		t.Helper()
		if !reflect.DeepEqual(got["displayName"], name) {
			t.Fatalf("displayName of %s: %v", what, got["displayName"])
		}
		if d, ok := got["description"]; ok && !reflect.DeepEqual(d, description) {
			t.Fatalf("description of %s: %v", what, d)
		}
	}
	same("install result", e.installPlugin(dir))
	detail := decode[map[string]any](t, e.expect(200, "GET", "/plugins/bridge", nil))
	same("detail", detail["plugin"].(map[string]any))
	same("manifest", detail["manifest"].(map[string]any))
	same("list", decode[map[string]any](t, e.expect(200, "GET", "/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	project := e.dc.Project.ID.String()
	same("project plugins", decode[map[string]any](t, e.expect(200, "GET", "/projects/"+project+"/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	_, agent := e.newAgent("Assistant")
	e.expect(200, "PUT", "/projects/"+project+"/plugins/bridge", map[string]any{"enabled": true, "values": map[string]any{}, "expectedRevision": 0})
	same("Agent plugins", decode[map[string]any](t, e.expect(200, "GET", agent+"/plugins", nil))["plugins"].([]any)[0].(map[string]any))
	var stored string
	if err := e.st.Pool.QueryRow(ctx, `SELECT display_name FROM plugins WHERE id='bridge'`).Scan(&stored); err != nil || stored != "桥接" { // check-english: zh (the plugins table stores the zh-CN display name)
		t.Fatalf("the display name in the plugins table should take zh-CN: %q %v", stored, err)
	}

	official := t.TempDir()
	hello := writePluginDir(t, "hello")
	localizeManifest(t, hello, name, description)
	if err := os.Rename(hello, filepath.Join(official, "hello")); err != nil {
		t.Fatal(err)
	}
	e.api.Plugins.OfficialDir = official
	same("official plugin sources", decode[map[string]any](t, e.expect(200, "GET", "/plugin-sources/builtin", nil))["sources"].([]any)[0].(map[string]any))

	files := map[string]any{
		"/good.json": map[string]any{"plugins": []any{map[string]any{"id": "relay", "source": "npm:@test/relay", "displayName": name, "description": description, "verified": true}}},
		"/bad.json":  map[string]any{"plugins": []any{map[string]any{"id": "relay", "source": "npm:@test/relay", "displayName": map[string]any{"english": "Relay"}}}},
	}
	market := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(files[r.URL.Path]) }))
	t.Cleanup(market.Close)
	e.api.Plugins.OfficialMarket, e.api.Plugins.MarketClient = market.URL+"/good.json", market.Client()
	e.expect(200, "PUT", "/plugin-settings", map[string]any{"disabled": false, "markets": []any{market.URL + "/bad.json"}})
	list := decode[map[string][]map[string]any](t, e.expect(200, "GET", "/plugin-markets", nil))["markets"]
	same("market entry", list[0]["plugins"].([]any)[0].(map[string]any))
	if msg, _ := list[1]["error"].(string); !strings.Contains(msg, "displayName must be a string, or an object with language tags") {
		t.Fatalf("market entry breaking the rules: %v", list[1])
	}
}
