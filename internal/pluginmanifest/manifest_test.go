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
	if err != nil || m.ID != "pi-tools-x" || m.DisplayName != "@scope/Pi_Tools.X" || m.Platform != "" || len(m.Events) != 0 {
		t.Fatalf("标准 Pi 包：%+v %v", m, err)
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
