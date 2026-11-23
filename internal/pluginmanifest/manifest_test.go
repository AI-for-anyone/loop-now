package pluginmanifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/pluginmanifest"
)

// writePlugin writes a plugin into a temporary directory: package.json plus some files.
func writePlugin(t *testing.T, pkg map[string]any, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	b, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(dir, "package.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, f)
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(p, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func full() map[string]any {
	return map[string]any{
		"name": "@example/loop-now-webhook", "version": "1.2.0", "description": "POST on new inbox messages",
		"pi": map[string]any{"extensions": []any{"./agent/index.ts"}, "skills": []any{"./skills"}},
		"loopNow": map[string]any{
			"id": "webhook", "displayName": "Webhook notifications", "minPlatformVersion": "2026.11", "platform": "./platform/index.ts",
			"events": []any{"inbox.received", "node.before_submit"}, "permissions": []any{"inbox.read", "state"}, "failClosed": true,
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "x-secret": true},
				"channel": map[string]any{"type": "string", "x-scope": "project"}}},
			"accounts":   []any{map[string]any{"kind": "lark", "label": "Lark account", "fields": map[string]any{"type": "object"}, "multiple": true}},
			"mcpServers": map[string]any{"browser": map[string]any{"command": "node", "args": []any{"./mcp/server.js"}}},
			"bin":        []any{"./bin"},
			"actions":    []any{map[string]any{"id": "resend", "label": "Resend", "where": []any{"identity"}}},
			"http":       true,
			"schedule":   []any{map[string]any{"id": "sweep", "cron": "*/10 * * * *"}},
			"channels":   []any{map[string]any{"id": "lark", "label": "Lark"}},
		},
	}
}

// A manifest with both sides reads back every field and component; a standard Pi package with only a pi section
// gets its id from the npm name.
func TestReadManifest(t *testing.T) {
	dir := writePlugin(t, full(), "agent/index.ts", "skills/", "platform/index.ts", "bin/")
	m, err := pluginmanifest.ReadManifest(dir, "2026.11.3")
	if err != nil {
		t.Fatal(err)
	}
	c := m.Components
	if m.ID != "webhook" || m.Name != "@example/loop-now-webhook" || m.Version != "1.2.0" || !m.FailClosed || m.Platform != "./platform/index.ts" ||
		c.AgentExtensions != 1 || len(c.Skills) != 1 || len(c.MCPServers) != 1 || c.MCPServers[0] != "browser" || len(c.Bin) != 1 || !c.Platform ||
		len(c.Actions) != 1 || len(c.Channels) != 1 || !c.HTTP || len(c.Schedules) != 1 {
		t.Fatalf("manifest: %+v", m)
	}
	if s := pluginmanifest.SecretFields(m.ConfigSchema); len(s) != 1 || s[0] != "token" {
		t.Fatalf("secret fields: %v", s)
	}
	if p := pluginmanifest.ProjectFields(m.ConfigSchema); len(p) != 1 || p[0] != "channel" {
		t.Fatalf("project fields: %v", p)
	}
	if b, _ := json.Marshal(m.Public()); strings.Contains(string(b), "platform/index.ts") || strings.Contains(string(b), "mcp/server.js") {
		t.Fatalf("the manifest in the API should not carry paths: %s", b)
	}
	if _, err := pluginmanifest.ReadManifest(dir, "dev-abc123"); err != nil {
		t.Fatalf("a dev build should not check minPlatformVersion: %v", err)
	}

	pi := writePlugin(t, map[string]any{"name": "@scope/Pi_Tools.X", "version": "0.1.0", "pi": map[string]any{"skills": []any{"./skills"}}}, "skills/")
	m, err = pluginmanifest.ReadManifest(pi, "2026.11")
	if err != nil || m.ID != "pi-tools-x" || m.DisplayName.String() != "@scope/Pi_Tools.X" || m.Platform != "" || len(m.Events) != 0 {
		t.Fatalf("standard Pi package: %+v %v", m, err)
	}
}

// displayName and description can be a string or an object keyed by language tag: they are returned as-is (an
// object keeps its key order), and when the server needs a single string it takes zh-CN, then en-US, then the first
// entry; any other shape is rejected.
func TestLocalizedText(t *testing.T) {
	pkg := full()
	pkg["description"] = map[string]any{"en-US": "POST on new inbox messages", "zh-CN": "收件箱有新消息时 POST"}       // check-english: zh (zh-CN value of a language-tag object, picked by String)
	pkg["loopNow"].(map[string]any)["displayName"] = map[string]any{"en-US": "Webhook", "zh-CN": "Webhook 通知"} // check-english: zh (zh-CN value of a language-tag object, picked by String)
	dir := writePlugin(t, pkg, "agent/index.ts", "skills/", "platform/index.ts", "bin/")
	m, err := pluginmanifest.ReadManifest(dir, "2026.11")
	if err != nil || m.DisplayName.String() != "Webhook 通知" || m.Description.String() != "收件箱有新消息时 POST" { // check-english: zh (zh-CN value of a language-tag object, picked by String)
		t.Fatalf("object form: %v %v", m.DisplayName, err)
	}
	if b, _ := json.Marshal(m.Public()); !strings.Contains(string(b), `"displayName":{"en-US":"Webhook","zh-CN":"Webhook 通知"}`) || !strings.Contains(string(b), `"description":{"en-US":`) { // check-english: zh (zh-CN value of a language-tag object, picked by String)
		t.Fatalf("the API should return it as-is: %s", b)
	}
	var stored pluginmanifest.Manifest
	if b, _ := json.Marshal(m); json.Unmarshal(b, &stored) != nil || stored.DisplayName.String() != "Webhook 通知" { // check-english: zh (zh-CN value of a language-tag object, picked by String)
		t.Fatalf("read back after saving: %s", b)
	}
	for _, c := range []struct {
		text any
		want string
	}{
		{"Webhook", "Webhook"},
		{map[string]any{"en-US": "Lark", "ja": "ラーク"}, "Lark"},
		{json.RawMessage(`{"ja": "ラーク", "fr": "Lark"}`), "ラーク"},
		{map[string]any{"zh-Hant-TW": "飛書"}, "飛書"}, // check-english: zh (Traditional Chinese value of a zh-Hant-TW tag)
	} {
		p := full()
		p["loopNow"].(map[string]any)["displayName"] = c.text
		m, err := pluginmanifest.ReadManifest(writePlugin(t, p, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
		if err != nil || m.DisplayName.String() != c.want {
			t.Fatalf("%v: %q %v", c.text, m.DisplayName.String(), err)
		}
	}
	for _, bad := range []any{
		map[string]any{}, map[string]any{"en-US": " "}, map[string]any{"en_US": "x"}, map[string]any{"english": "x"},
		map[string]any{"en-US": 3}, []any{"x"}, 3, nil, true,
	} {
		for _, field := range []string{"displayName", "description"} {
			p := full()
			if field == "description" {
				p["description"] = bad
			} else {
				p["loopNow"].(map[string]any)["displayName"] = bad
			}
			_, err := pluginmanifest.ReadManifest(writePlugin(t, p, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
			if err == nil || !strings.Contains(err.Error(), field+" must be a string, or an object with language tags") {
				t.Fatalf("%s=%v should be rejected: %v", field, bad, err)
			}
		}
	}
	p := full()
	p["loopNow"].(map[string]any)["displayName"] = ""
	delete(p, "description")
	m, err = pluginmanifest.ReadManifest(writePlugin(t, p, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
	if b, _ := json.Marshal(m.Public()); err != nil || m.DisplayName.String() != "@example/loop-now-webhook" || !strings.Contains(string(b), `"description":""`) {
		t.Fatalf("an empty display name should fall back to the npm name, and a missing description should be an empty string: %s %v", b, err)
	}
}

// Manifests that break the rules are rejected, one rule at a time.
func TestReadManifestRejects(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(pkg map[string]any)
		files []string
		want  string
	}{
		{"neither section", func(p map[string]any) { delete(p, "pi"); delete(p, "loopNow") }, nil, "not a plugin"},
		{"id", func(p map[string]any) { p["loopNow"].(map[string]any)["id"] = "Web_Hook" }, nil, "loopNow.id"},
		{"version", func(p map[string]any) { p["version"] = "" }, nil, "version"},
		{"events", func(p map[string]any) { p["loopNow"].(map[string]any)["events"] = []any{"inbox.recieved"} }, nil, "is not a platform event"},
		{"permissions", func(p map[string]any) { p["loopNow"].(map[string]any)["permissions"] = []any{"db.write"} }, nil, "is not an available scope"},
		{"path outside the plugin", func(p map[string]any) { p["pi"].(map[string]any)["extensions"] = []any{"../evil.ts"} }, nil, "relative path"},
		{"missing file", func(p map[string]any) { p["pi"].(map[string]any)["skills"] = []any{"./missing"} }, nil, "does not exist"},
		{"events without a platform side", func(p map[string]any) { delete(p["loopNow"].(map[string]any), "platform") }, nil, "loopNow.platform"},
		{"platform version", func(p map[string]any) { p["loopNow"].(map[string]any)["minPlatformVersion"] = "2027.1" }, nil, "2027.1 or later"},
		{"config schema", func(p map[string]any) {
			p["loopNow"].(map[string]any)["configSchema"] = map[string]any{"type": "object", "properties": 3}
		}, nil, "configSchema"},
		{"account kind", func(p map[string]any) {
			p["loopNow"].(map[string]any)["accounts"] = []any{map[string]any{"kind": "Lark", "label": "x", "fields": map[string]any{}}}
		}, nil, "kind"},
		{"MCP", func(p map[string]any) {
			p["loopNow"].(map[string]any)["mcpServers"] = map[string]any{"x": map[string]any{"args": []any{}}}
		}, nil, "command"},
		{"cron", func(p map[string]any) {
			p["loopNow"].(map[string]any)["schedule"] = []any{map[string]any{"id": "s", "cron": "* *"}}
		}, nil, "cron"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := full()
			c.edit(pkg)
			dir := writePlugin(t, pkg, append([]string{"agent/index.ts", "skills/", "platform/index.ts", "bin/"}, c.files...)...)
			_, err := pluginmanifest.ReadManifest(dir, "2026.11")
			var e *pluginmanifest.Error
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("should be rejected for %q: %v", c.want, err)
			}
			if !asError(err, &e) {
				t.Fatalf("error type: %T", err)
			}
		})
	}
	if _, err := pluginmanifest.ReadManifest(t.TempDir(), "2026.11"); err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("no manifest file: %v", err)
	}
}

// mcpServers entries are the same as in Pi's mcp.json: a stdio entry with a non-empty command, or a streamable HTTP
// entry with an http/https url; type may be omitted and can only be stdio, http or streamable-http, and SSE is
// rejected. An entry with both a command and a url but no type is treated as a url entry (the same as Pi).
func TestMCPServerEntries(t *testing.T) {
	cases := []struct {
		name  string
		entry any
		want  string // empty means accepted
	}{
		{"stdio", map[string]any{"command": "node", "args": []any{"./mcp/server.js"}}, ""},
		{"stdio with type", map[string]any{"type": "stdio", "command": "node"}, ""},
		{"url", map[string]any{"url": "http://127.0.0.1:3845/mcp"}, ""},
		{"url with headers", map[string]any{"type": "http", "url": "https://mcp.figma.com/mcp", "headers": map[string]any{"Authorization": "Bearer ${FIGMA_TOKEN}"}}, ""},
		{"streamable-http", map[string]any{"type": "streamable-http", "url": "https://example.com/mcp"}, ""},
		{"both given, treated as url", map[string]any{"command": "node", "url": "https://example.com/mcp"}, ""},
		{"type stdio uses command", map[string]any{"type": "stdio", "command": "node", "url": "ftp://x"}, ""},
		{"sse", map[string]any{"type": "sse", "url": "https://example.com/sse"}, "SSE"},
		{"unknown type", map[string]any{"type": "websocket", "url": "https://example.com/mcp"}, "must have type"},
		{"type not a string", map[string]any{"type": nil, "command": "node"}, "must have type"},
		{"url not http", map[string]any{"url": "ftp://example.com/mcp"}, "must be a stdio entry with a command"},
		{"url with upper-case scheme", map[string]any{"url": "HTTPS://mcp.figma.com/mcp"}, ""},
		{"url without host", map[string]any{"url": "http:foo"}, "must be a stdio entry with a command"},
		{"url without host 2", map[string]any{"url": "http:///mcp"}, "must be a stdio entry with a command"},
		{"url port out of range", map[string]any{"url": "http://127.0.0.1:99999/mcp"}, "must be a stdio entry with a command"},
		{"http with only command", map[string]any{"type": "http", "command": "node"}, "must be a stdio entry with a command"},
		{"stdio with only url", map[string]any{"type": "stdio", "url": "https://example.com/mcp"}, "must be a stdio entry with a command"},
		{"empty command", map[string]any{"command": ""}, "must be a stdio entry with a command"},
		{"neither", map[string]any{"args": []any{}}, "must be a stdio entry with a command"},
		{"not an object", "node", "must be a stdio entry with a command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := full()
			pkg["loopNow"].(map[string]any)["mcpServers"] = map[string]any{"x": c.entry}
			dir := writePlugin(t, pkg, "agent/index.ts", "skills/", "platform/index.ts", "bin/")
			_, err := pluginmanifest.ReadManifest(dir, "2026.11")
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

func asError(err error, target **pluginmanifest.Error) bool {
	e, ok := err.(*pluginmanifest.Error)
	*target = e
	return ok
}

// Config is validated against the schema: it can be saved when not fully filled in (required is not checked) and is
// rejected when a type is wrong; account fields do check required.
func TestValidate(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"url"}, "properties": map[string]any{"url": map[string]any{"type": "string", "format": "uri"}, "n": map[string]any{"type": "integer"}}}
	if err := pluginmanifest.Validate(schema, false, map[string]any{"n": 3}); err != nil {
		t.Fatalf("required should not be checked: %v", err)
	}
	if err := pluginmanifest.Validate(schema, true, map[string]any{"n": 3}); err == nil {
		t.Fatal("should be rejected when required is checked")
	}
	if err := pluginmanifest.Validate(schema, false, map[string]any{"n": "x"}); err == nil || !strings.Contains(err.Error(), "Does not match") {
		t.Fatalf("wrong type: %v", err)
	}
}

func TestOwnDisplayName(t *testing.T) {
	for _, c := range []struct {
		pkg string
		own bool
	}{
		{`{"name":"pi-x","version":"1.0.0","pi":{"extensions":["./a.ts"]}}`, false},
		{`{"name":"pi-x","version":"1.0.0","pi":{"extensions":["./a.ts"]},"loopNow":{"id":"x"}}`, false},
		{`{"name":"pi-x","version":"1.0.0","pi":{"extensions":["./a.ts"]},"loopNow":{"id":"x","displayName":"X"}}`, true},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(c.pkg), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := pluginmanifest.ReadManifest(dir, "dev")
		if err != nil || m.OwnDisplayName != c.own {
			t.Fatalf("%s: %v %v", c.pkg, m.OwnDisplayName, err)
		}
	}
}
