package modelcatalog_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
)

// fakeExtension 是一个最小的“订阅登录”Pi 扩展：从 Pi 的包里导入（验证扩展加载器的模块别名），
// 加载时在 HOME 下写一个文件（验证副作用只落在临时目录），注册一个 OAuth 提供方、覆盖一个内置提供方，另有一个扩展加载失败。
const fakeExtension = `import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { lazyStream } from '@earendil-works/pi-ai';
import { type ExtensionAPI, readStoredCredential } from '@earendil-works/pi-coding-agent';

export default function register(pi: ExtensionAPI) {
  const dir = join(process.env.HOME ?? '', '.pi', 'fake-oauth');
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'loaded.json'), JSON.stringify({ stored: readStoredCredential('fake-oauth') ?? null, home: process.env.HOME }));
  pi.registerProvider('fake-oauth', {
    name: 'Fake OAuth',
    baseUrl: 'https://fake.invalid/v1',
    api: 'openai-responses',
    oauth: {
      name: 'Fake OAuth',
      async login() { return { access: 'marker', refresh: 'marker', expires: Number.MAX_SAFE_INTEGER }; },
      async refreshToken(c) { return c; },
      getApiKey(c) { return c.access; },
    },
    models: [
      { id: 'fake-1', name: 'Fake One', reasoning: true, thinkingLevelMap: { minimal: 'low', low: 'low', medium: 'medium', high: 'high' }, input: ['text', 'image'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 100000, maxTokens: 8000 },
      { id: 'fake-2', name: 'Fake Two', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 50000, maxTokens: 4000 },
    ],
    streamSimple(model) { return lazyStream(model, async () => { throw new Error('offline'); }); },
  });
  pi.registerProvider('anthropic', { baseUrl: 'https://proxy.invalid' });
}
`

func TestCaptureProvidersWithPiLoader(t *testing.T) {
	options := catalogtest.Options(t)
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json":  `{"name":"pi-fake-oauth","version":"0.1.0","type":"module","pi":{"extensions":["./src/index.ts","./src/broken.ts"]}}`,
		"src/index.ts":  fakeExtension,
		"src/broken.ts": "throw new Error('broken on purpose');\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c, err := modelcatalog.CaptureProviders(context.Background(), options, dir, []string{"./src/index.ts", "./src/broken.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Errors) != 1 || c.Errors[0].Path != "./src/broken.ts" {
		t.Fatalf("加载失败的扩展：%+v", c.Errors)
	}
	byID := map[string]modelcatalog.CapturedProvider{}
	for _, p := range c.Providers {
		byID[p.ID] = p
	}
	fake := byID["fake-oauth"]
	if !fake.OAuth || fake.Name != "Fake OAuth" || fake.Extension != "./src/index.ts" || fake.Error != nil || !fake.CatalogEligible() || len(fake.Models) != 2 {
		t.Fatalf("OAuth 提供方：%+v", fake)
	}
	one := fake.Models[0]
	if one.ID != "fake-1" || !one.Reasoning || !slices.Contains(one.ThinkingLevels, "high") || one.DefaultThinkingLevel != "medium" || !slices.Contains(one.Input, "image") || one.ContextWindow != 100000 {
		t.Fatalf("模型与思考等级：%+v", one)
	}
	if two := fake.Models[1]; !slices.Equal(two.ThinkingLevels, []string{"off"}) || two.DefaultThinkingLevel != "off" {
		t.Fatalf("不推理的模型：%+v", two)
	}
	if a := byID["anthropic"]; !a.OverridesBuiltin || a.CatalogEligible() || len(a.Models) != 0 {
		t.Fatalf("覆盖内置提供方：%+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pi")); !os.IsNotExist(err) {
		t.Fatal("扩展的副作用不能落在插件目录")
	}

	// 并入目录：带 OAuth 的新提供方以 plugin 来源出现，Pi 目录部分不变。
	base, err := modelcatalog.Load(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	reg := modelcatalog.NewRegistry(base)
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "pi-fake-oauth", Provider: fake}, {PluginID: "pi-fake-oauth", Provider: byID["anthropic"]}})
	p, ok := reg.Provider("fake-oauth")
	if !ok || p.Source != "plugin" || *p.PluginID != "pi-fake-oauth" || !reg.SupportsImages("fake-oauth", "fake-1") {
		t.Fatalf("并入目录：%+v %v", p, ok)
	}
	if level, err := reg.ResolveThinking("fake-oauth", "fake-1", nil); err != nil || level != "medium" {
		t.Fatalf("默认思考等级：%q %v", level, err)
	}
	if a, _ := reg.Provider("anthropic"); a.Source != "builtin" {
		t.Fatalf("内置提供方不被插件替换：%+v", a)
	}
	// 发布的目录 = Pi 目录 + 本机 CLI 的 claude-code + 这一个插件提供方（与内置同 id 的 anthropic 不并入）。
	if len(reg.Snapshot().Providers) != len(base.Providers)+2 {
		t.Fatal("只多本机 CLI 与一个插件提供方")
	}
}
