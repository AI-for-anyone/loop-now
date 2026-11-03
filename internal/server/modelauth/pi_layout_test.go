package modelauth

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	authhelper "loop-now/auth-helper"
)

// fakePiTree writes the package files the helper reads, laid out as
// `npm install --prefix <prefix>` leaves them: pi-ai hoisted beside
// pi-coding-agent instead of inside it.
func fakePiTree(t *testing.T) (pkg, ai string) {
	t.Helper()
	modules := filepath.Join(t.TempDir(), "node_modules", "@earendil-works")
	pkg, ai = filepath.Join(modules, "pi-coding-agent"), filepath.Join(modules, "pi-ai")
	files := map[string]string{
		filepath.Join(pkg, "package.json"):                        `{"name":"@earendil-works/pi-coding-agent","version":"1.4.0"}`,
		filepath.Join(ai, "package.json"):                         `{"name":"@earendil-works/pi-ai","version":"1.4.0","type":"module","exports":{"./providers/*":{"import":"./dist/providers/*.js"}}}`,
		filepath.Join(ai, "dist", "providers", "openai-codex.js"): `export function openaiCodexProvider(){return {id:'openai-codex',auth:{oauth:{login(){},refresh(){}}},getModels(){return [{id:'fixture'}]}};}`,
		filepath.Join(ai, "dist", "providers", "anthropic.js"):    `export function anthropicProvider(){return {id:'anthropic',auth:{apiKey:{resolve(){}}}};}`,
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return pkg, ai
}

// The helper's Codex provider and the legacy Anthropic extension load from the pi-ai
// directory the server passes, including the hoisted layout of a release
// bundle; guessing pi-ai inside pi-coding-agent fails there.
func TestProvidersLoadFromHoistedPiLayout(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("requires node")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helper, _, err := authhelper.Install(filepath.Join(base, "platform", "auth-helper"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, ai := fakePiTree(t)
	script := `import { pathToFileURL } from 'node:url';
const [assets, pkg, ai] = process.argv.slice(1);
const { loadPiProvider } = await import(pathToFileURL(assets + '/pi-provider.mjs').href);
const codex = await loadPiProvider(pkg, ai);
if (codex.version !== '1.4.0' || codex.models.length !== 1) throw new Error('codex provider not loaded');
let guessed = false;
try { await loadPiProvider(pkg, pkg + '/node_modules/@earendil-works/pi-ai'); guessed = true; } catch {}
if (guessed) throw new Error('the nested guess should not exist in this layout');
process.env.LOOP_NOW_PI_PACKAGE_ROOT = pkg;
process.env.LOOP_NOW_PI_AI_DIR = ai;
let registered;
await (await import(pathToFileURL(assets + '/legacy-anthropic.mjs').href)).default({ registerProvider: p => { registered = p; } });
if (registered?.id !== 'anthropic') throw new Error('legacy anthropic provider not registered');
console.log('loaded');`
	cmd := exec.Command(node, "--input-type=module", "-e", script, filepath.Dir(helper), pkg, ai)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LOOP_NOW_AUTH_PI_VERSION=1.4.0"}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "loaded" {
		t.Fatalf("providers did not load from the hoisted layout: %v\n%s", err, out)
	}
}
