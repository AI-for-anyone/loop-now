package pluginmanifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/pluginmanifest"
)

// writePlugin 在临时目录写一个插件：package.json 加若干文件。
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
		"name": "@example/loop-now-webhook", "version": "1.2.0", "description": "收件箱有新消息时 POST",
		"pi": map[string]any{"extensions": []any{"./agent/index.ts"}, "skills": []any{"./skills"}},
		"loopNow": map[string]any{
			"id": "webhook", "displayName": "Webhook 通知", "minPlatformVersion": "2026.11", "platform": "./platform/index.ts",
			"events": []any{"inbox.received", "node.before_submit"}, "permissions": []any{"inbox.read", "state"}, "failClosed": true,
			"configSchema": map[string]any{"type": "object", "properties": map[string]any{
				"url": map[string]any{"type": "string"}, "token": map[string]any{"type": "string", "x-secret": true},
				"channel": map[string]any{"type": "string", "x-scope": "project"}}},
			"accounts":   []any{map[string]any{"kind": "lark", "label": "飞书账号", "fields": map[string]any{"type": "object"}, "multiple": true}},
			"mcpServers": map[string]any{"browser": map[string]any{"command": "node", "args": []any{"./mcp/server.js"}}},
			"bin":        []any{"./bin"},
			"actions":    []any{map[string]any{"id": "resend", "label": "重发", "where": []any{"identity"}}},
			"http":       true,
			"schedule":   []any{map[string]any{"id": "sweep", "cron": "*/10 * * * *"}},
			"channels":   []any{map[string]any{"id": "lark", "label": "飞书"}},
		},
	}
}

// 两半齐全的清单读出全部字段与组成；只有 pi 段的标准 Pi 包用 npm 名得到 id。
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
		t.Fatalf("清单：%+v", m)
	}
	if s := pluginmanifest.SecretFields(m.ConfigSchema); len(s) != 1 || s[0] != "token" {
		t.Fatalf("secret 字段：%v", s)
	}
	if p := pluginmanifest.ProjectFields(m.ConfigSchema); len(p) != 1 || p[0] != "channel" {
		t.Fatalf("项目字段：%v", p)
	}
	if b, _ := json.Marshal(m.Public()); strings.Contains(string(b), "platform/index.ts") || strings.Contains(string(b), "mcp/server.js") {
		t.Fatalf("接口里的清单不应带路径：%s", b)
	}
	if _, err := pluginmanifest.ReadManifest(dir, "dev-abc123"); err != nil {
		t.Fatalf("开发版不检查 minPlatformVersion：%v", err)
	}

	pi := writePlugin(t, map[string]any{"name": "@scope/Pi_Tools.X", "version": "0.1.0", "pi": map[string]any{"skills": []any{"./skills"}}}, "skills/")
	m, err = pluginmanifest.ReadManifest(pi, "2026.11")
	if err != nil || m.ID != "pi-tools-x" || m.DisplayName.String() != "@scope/Pi_Tools.X" || m.Platform != "" || len(m.Events) != 0 {
		t.Fatalf("标准 Pi 包：%+v %v", m, err)
	}
}

// displayName 与 description 可以是字符串，或以语言标签为键的对象：原样返回（对象保留键的顺序），
// 服务端需要单个字符串时依次取 zh-CN、en-US、第一项；其余写法拒绝。
func TestLocalizedText(t *testing.T) {
	pkg := full()
	pkg["description"] = map[string]any{"en-US": "POST on new inbox messages", "zh-CN": "收件箱有新消息时 POST"}
	pkg["loopNow"].(map[string]any)["displayName"] = map[string]any{"en-US": "Webhook", "zh-CN": "Webhook 通知"}
	dir := writePlugin(t, pkg, "agent/index.ts", "skills/", "platform/index.ts", "bin/")
	m, err := pluginmanifest.ReadManifest(dir, "2026.11")
	if err != nil || m.DisplayName.String() != "Webhook 通知" || m.Description.String() != "收件箱有新消息时 POST" {
		t.Fatalf("对象形式：%v %v", m.DisplayName, err)
	}
	if b, _ := json.Marshal(m.Public()); !strings.Contains(string(b), `"displayName":{"en-US":"Webhook","zh-CN":"Webhook 通知"}`) || !strings.Contains(string(b), `"description":{"en-US":`) {
		t.Fatalf("接口里原样返回：%s", b)
	}
	var stored pluginmanifest.Manifest
	if b, _ := json.Marshal(m); json.Unmarshal(b, &stored) != nil || stored.DisplayName.String() != "Webhook 通知" {
		t.Fatalf("保存后读回：%s", b)
	}
	for _, c := range []struct {
		text any
		want string
	}{
		{"Webhook", "Webhook"},
		{map[string]any{"en-US": "Lark", "ja": "ラーク"}, "Lark"},
		{json.RawMessage(`{"ja": "ラーク", "fr": "Lark"}`), "ラーク"},
		{map[string]any{"zh-Hant-TW": "飛書"}, "飛書"},
	} {
		p := full()
		p["loopNow"].(map[string]any)["displayName"] = c.text
		m, err := pluginmanifest.ReadManifest(writePlugin(t, p, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
		if err != nil || m.DisplayName.String() != c.want {
			t.Fatalf("%v：%q %v", c.text, m.DisplayName.String(), err)
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
			if err == nil || !strings.Contains(err.Error(), field+" 须是字符串，或以语言标签") {
				t.Fatalf("%s=%v 应拒绝：%v", field, bad, err)
			}
		}
	}
	p := full()
	p["loopNow"].(map[string]any)["displayName"] = ""
	delete(p, "description")
	m, err = pluginmanifest.ReadManifest(writePlugin(t, p, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
	if b, _ := json.Marshal(m.Public()); err != nil || m.DisplayName.String() != "@example/loop-now-webhook" || !strings.Contains(string(b), `"description":""`) {
		t.Fatalf("空的显示名回落到 npm 名，缺省的描述为空字符串：%s %v", b, err)
	}
}

// 不合规则的清单逐项拒绝。
func TestReadManifestRejects(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(pkg map[string]any)
		files []string
		want  string
	}{
		{"没有两段", func(p map[string]any) { delete(p, "pi"); delete(p, "loopNow") }, nil, "不是插件"},
		{"id", func(p map[string]any) { p["loopNow"].(map[string]any)["id"] = "Web_Hook" }, nil, "loopNow.id"},
		{"version", func(p map[string]any) { p["version"] = "" }, nil, "version"},
		{"事件", func(p map[string]any) { p["loopNow"].(map[string]any)["events"] = []any{"inbox.recieved"} }, nil, "不是平台的事件"},
		{"权限", func(p map[string]any) { p["loopNow"].(map[string]any)["permissions"] = []any{"db.write"} }, nil, "不是可用的范围"},
		{"越界路径", func(p map[string]any) { p["pi"].(map[string]any)["extensions"] = []any{"../evil.ts"} }, nil, "相对路径"},
		{"不存在的文件", func(p map[string]any) { p["pi"].(map[string]any)["skills"] = []any{"./missing"} }, nil, "不存在"},
		{"订阅却没有平台半边", func(p map[string]any) { delete(p["loopNow"].(map[string]any), "platform") }, nil, "loopNow.platform"},
		{"平台版本", func(p map[string]any) { p["loopNow"].(map[string]any)["minPlatformVersion"] = "2027.1" }, nil, "不低于 2027.1"},
		{"配置 schema", func(p map[string]any) {
			p["loopNow"].(map[string]any)["configSchema"] = map[string]any{"type": "object", "properties": 3}
		}, nil, "configSchema"},
		{"账号种类", func(p map[string]any) {
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
				t.Fatalf("应因 %q 拒绝：%v", c.want, err)
			}
			if !asError(err, &e) {
				t.Fatalf("错误类型：%T", err)
			}
		})
	}
	if _, err := pluginmanifest.ReadManifest(t.TempDir(), "2026.11"); err == nil || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("没有清单文件：%v", err)
	}
}

// mcpServers 的条目与 Pi mcp.json 相同：带非空 command 的 stdio 条目，或带 http/https url 的 streamable HTTP 条目；
// type 可省，只能是 stdio、http 或 streamable-http，SSE 拒绝。两者都有又没有 type 时按 url（与 Pi 相同）。
func TestMCPServerEntries(t *testing.T) {
	cases := []struct {
		name  string
		entry any
		want  string // 空表示接受
	}{
		{"stdio", map[string]any{"command": "node", "args": []any{"./mcp/server.js"}}, ""},
		{"stdio 带 type", map[string]any{"type": "stdio", "command": "node"}, ""},
		{"url", map[string]any{"url": "http://127.0.0.1:3845/mcp"}, ""},
		{"url 带 headers", map[string]any{"type": "http", "url": "https://mcp.figma.com/mcp", "headers": map[string]any{"Authorization": "Bearer ${FIGMA_TOKEN}"}}, ""},
		{"streamable-http", map[string]any{"type": "streamable-http", "url": "https://example.com/mcp"}, ""},
		{"两者都有按 url", map[string]any{"command": "node", "url": "https://example.com/mcp"}, ""},
		{"type stdio 按 command", map[string]any{"type": "stdio", "command": "node", "url": "ftp://x"}, ""},
		{"sse", map[string]any{"type": "sse", "url": "https://example.com/sse"}, "SSE"},
		{"未知 type", map[string]any{"type": "websocket", "url": "https://example.com/mcp"}, "type 只能是"},
		{"type 不是字符串", map[string]any{"type": nil, "command": "node"}, "type 只能是"},
		{"url 不是 http", map[string]any{"url": "ftp://example.com/mcp"}, "须是带 command"},
		{"url 大写协议", map[string]any{"url": "HTTPS://mcp.figma.com/mcp"}, ""},
		{"url 没有主机", map[string]any{"url": "http:foo"}, "须是带 command"},
		{"url 没有主机 2", map[string]any{"url": "http:///mcp"}, "须是带 command"},
		{"url 端口越界", map[string]any{"url": "http://127.0.0.1:99999/mcp"}, "须是带 command"},
		{"http 却只有 command", map[string]any{"type": "http", "command": "node"}, "须是带 command"},
		{"stdio 却只有 url", map[string]any{"type": "stdio", "url": "https://example.com/mcp"}, "须是带 command"},
		{"空 command", map[string]any{"command": ""}, "须是带 command"},
		{"都没有", map[string]any{"args": []any{}}, "须是带 command"},
		{"不是对象", "node", "须是带 command"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pkg := full()
			pkg["loopNow"].(map[string]any)["mcpServers"] = map[string]any{"x": c.entry}
			dir := writePlugin(t, pkg, "agent/index.ts", "skills/", "platform/index.ts", "bin/")
			_, err := pluginmanifest.ReadManifest(dir, "2026.11")
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("期望 %q：%v", c.want, err)
			}
		})
	}
}

func asError(err error, target **pluginmanifest.Error) bool {
	e, ok := err.(*pluginmanifest.Error)
	*target = e
	return ok
}

// 配置按 schema 校验：不填全时（不查 required）可以保存，类型不对时拒绝；账号字段查 required。
func TestValidate(t *testing.T) {
	schema := map[string]any{"type": "object", "required": []any{"url"}, "properties": map[string]any{"url": map[string]any{"type": "string", "format": "uri"}, "n": map[string]any{"type": "integer"}}}
	if err := pluginmanifest.Validate(schema, false, map[string]any{"n": 3}); err != nil {
		t.Fatalf("不查 required：%v", err)
	}
	if err := pluginmanifest.Validate(schema, true, map[string]any{"n": 3}); err == nil {
		t.Fatal("查 required 时应拒绝")
	}
	if err := pluginmanifest.Validate(schema, false, map[string]any{"n": "x"}); err == nil || !strings.Contains(err.Error(), "不符合") {
		t.Fatalf("类型不对：%v", err)
	}
}
