package modelauth

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	authhelper "loop-now/auth-helper"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcatalog/catalogtest"
)

// The real login helper process: the bundled Pi's extension loader loads a fake extension, questions travel back and
// forth over a private pipe, the extension's state is written into HOME, and the return value is only in the result.
const loginExtension = `import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { readStoredCredential } from '@earendil-works/pi-coding-agent';
export default function register(pi) {
  pi.registerProvider('fake-oauth', {
    name: 'Fake OAuth', baseUrl: 'https://fake.invalid/v1', api: 'openai-responses',
    oauth: {
      name: 'Fake OAuth',
      async login(cb) {
        const method = await cb.onSelect({ message: 'Pick a method', options: [{ id: 'browser', label: 'Browser' }, { id: 'device', label: 'Device' }] });
        if (method !== 'device') throw new Error('wrong method');
        cb.onDeviceCode({ userCode: 'WXYZ-1234', verificationUri: 'https://accounts.example.test/device', expiresInSeconds: 300 });
        cb.onProgress('polling with secret-progress');
        const dir = join(process.env.HOME, '.pi', 'fake-oauth');
        mkdirSync(dir, { recursive: true });
        writeFileSync(join(dir, 'accounts.json'), JSON.stringify({ stored: readStoredCredential('fake-oauth') ?? null }));
        return { access: 'vault-marker', refresh: 'vault-marker', expires: Number.MAX_SAFE_INTEGER };
      },
      async refreshToken(c) { return c; },
      getApiKey(c) { return c.access; },
    },
    models: [{ id: 'fake-1', name: 'Fake One', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }],
  });
}
`

type recordingUI struct {
	mu      sync.Mutex
	device  string
	prompts []cm.LoginPrompt
	replies chan cm.LoginInput
}

func (u *recordingUI) AuthURL(string) error { return nil }
func (u *recordingUI) DeviceCode(code, uri string, _ time.Duration) error {
	u.mu.Lock()
	u.device = code + " " + uri
	u.mu.Unlock()
	return nil
}
func (u *recordingUI) Prompt(q cm.LoginPrompt) {
	u.mu.Lock()
	u.prompts = append(u.prompts, q)
	u.mu.Unlock()
	u.replies <- cm.LoginInput{PromptID: q.ID, Value: "device"}
}
func (u *recordingUI) PromptClosed(string) {}

func TestPluginHelperWithPiLoader(t *testing.T) {
	o := catalogtest.Options(t)
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The install directory and its parent must both be 0700.
	if err := os.Chmod(work, 0o700); err != nil {
		t.Fatal(err)
	}
	entry, _, err := authhelper.Install(filepath.Join(work, "auth-helper"))
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Join(t.TempDir(), "index.ts")
	if err := os.WriteFile(ext, []byte(loginExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(work, "state")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &PluginHelper{Options: PluginHelperOptions{NodeBin: o.NodeBin, Entry: filepath.Join(filepath.Dir(entry), "provider-main.mjs"), WorkRoot: work, BasePath: o.Path,
		PiPackageRoot: o.PiPackageDir, PiVersion: "1.4.0", Timeout: 30 * time.Second, StopGrace: time.Second}}
	ui := &recordingUI{replies: make(chan cm.LoginInput, 4)}
	got, err := h.Login(context.Background(), PluginLoginRequest{OperationID: [16]byte{1}, Provider: "fake-oauth", Extensions: []string{ext}, Home: home}, ui, ui.replies)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"access":"vault-marker","refresh":"vault-marker","expires":9007199254740991,"type":"oauth"}` {
		t.Fatalf("auth entry: %s", got)
	}
	if len(ui.prompts) != 1 || ui.prompts[0].Kind != "select" || ui.prompts[0].Message != "Pick a method" || ui.device != "WXYZ-1234 https://accounts.example.test/device" {
		t.Fatalf("interaction: %+v %q", ui.prompts, ui.device)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".pi", "fake-oauth", "accounts.json")); err != nil || string(b) != `{"stored":null}` {
		t.Fatalf("the extension state should be written under HOME: %s %v", b, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 1 {
		t.Fatalf("HOME should hold only the extension's own files: %v", entries)
	}

	// This provider is not registered: extension_load_failed.
	if _, err := h.Login(context.Background(), PluginLoginRequest{OperationID: [16]byte{2}, Provider: "other", Extensions: []string{ext}, Home: home}, ui, ui.replies); err != ErrExtensionLoad {
		t.Fatalf("unknown provider: %v", err)
	}
}
