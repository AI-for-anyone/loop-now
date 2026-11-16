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

// fakeExtension is a minimal "subscription login" Pi extension: it imports from Pi's packages (checking the module
// aliases of the extension loader), writes a file under HOME when loaded (checking that side effects land only in the
// temporary directory), registers one OAuth provider and overrides one built-in provider; one more extension fails to
// load.
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
		t.Fatalf("extensions that failed to load: %+v", c.Errors)
	}
	byID := map[string]modelcatalog.CapturedProvider{}
	for _, p := range c.Providers {
		byID[p.ID] = p
	}
	fake := byID["fake-oauth"]
	if !fake.OAuth || fake.Name != "Fake OAuth" || fake.Extension != "./src/index.ts" || fake.Error != nil || !fake.CatalogEligible() || len(fake.Models) != 2 {
		t.Fatalf("OAuth provider: %+v", fake)
	}
	one := fake.Models[0]
	if one.ID != "fake-1" || !one.Reasoning || !slices.Contains(one.ThinkingLevels, "high") || one.DefaultThinkingLevel != "medium" || !slices.Contains(one.Input, "image") || one.ContextWindow != 100000 {
		t.Fatalf("model and thinking levels: %+v", one)
	}
	if two := fake.Models[1]; !slices.Equal(two.ThinkingLevels, []string{"off"}) || two.DefaultThinkingLevel != "off" {
		t.Fatalf("non-reasoning model: %+v", two)
	}
	if a := byID["anthropic"]; !a.OverridesBuiltin || a.CatalogEligible() || len(a.Models) != 0 {
		t.Fatalf("provider overriding a built-in one: %+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, ".pi")); !os.IsNotExist(err) {
		t.Fatal("extension side effects must not land in the plugin directory")
	}

	// Merged into the catalog: the new provider with OAuth appears with source plugin, and the Pi catalog part is
	// unchanged.
	base, err := modelcatalog.Load(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	reg := modelcatalog.NewRegistry(base)
	reg.SetPluginProviders([]modelcatalog.PluginProvider{{PluginID: "pi-fake-oauth", Provider: fake}, {PluginID: "pi-fake-oauth", Provider: byID["anthropic"]}})
	p, ok := reg.Provider("fake-oauth")
	if !ok || p.Source != "plugin" || *p.PluginID != "pi-fake-oauth" || !reg.SupportsImages("fake-oauth", "fake-1") {
		t.Fatalf("merged into the catalog: %+v %v", p, ok)
	}
	if level, err := reg.ResolveThinking("fake-oauth", "fake-1", nil); err != nil || level != "medium" {
		t.Fatalf("default thinking level: %q %v", level, err)
	}
	if a, _ := reg.Provider("anthropic"); a.Source != "builtin" {
		t.Fatalf("a built-in provider should not be replaced by a plugin: %+v", a)
	}
	// Published catalog = Pi catalog + the local CLI claude-code + this one plugin provider (anthropic, which has the
	// same id as a built-in provider, is not merged).
	if len(reg.Snapshot().Providers) != len(base.Providers)+2 {
		t.Fatal("should add only the local CLI and one plugin provider")
	}
}
