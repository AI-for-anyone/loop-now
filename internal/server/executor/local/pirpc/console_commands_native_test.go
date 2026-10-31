package pirpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/preflight"
)

// Real installed Pi 1.4 export_html RPC with a synthetic session. No Prompt,
// compact/model request, credentials, user HOME or network is used.
func TestInstalledPi14ExportHTML(t *testing.T) {
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("本机没有 pi")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root := t.TempDir()
	r, err := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(root, "work"), HomeDirs: []string{"/nonexistent"}})
	if err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(root, "deny.mjs")
	const networkGuard = `import net from 'node:net';import tls from 'node:tls';import dgram from 'node:dgram';import {writeFileSync} from 'node:fs';let denied=0;const reject=()=>{denied++;throw Error('network denied by export fixture')};net.Socket.prototype.connect=reject;tls.connect=reject;dgram.createSocket=reject;globalThis.fetch=reject;process.on('exit',()=>writeFileSync(process.env.EXPORT_NETWORK_REPORT,JSON.stringify({denied})));`
	if err = os.WriteFile(preload, []byte(networkGuard), 0600); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(root, "session.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var raw []byte
	for _, entry := range []any{
		map[string]any{"type": "session", "version": 3, "id": uuid.NewString(), "timestamp": now, "cwd": root},
		map[string]any{"type": "message", "id": "user-entry", "parentId": nil, "timestamp": now, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "LOOP_NOW_EXPORT_SENTINEL; no provider call"}}, "timestamp": time.Now().UnixMilli()}},
	} {
		line, _ := json.Marshal(entry)
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	if err = os.WriteFile(session, raw, 0600); err != nil {
		t.Fatal(err)
	}
	network := filepath.Join(root, "network.json")
	args := []string{"--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--mode", "rpc", "--offline", "--session", session, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--provider", "openai-codex", "--model", "gpt-6.1-sol"}
	cmd := exec.CommandContext(ctx, r.NodeBin, args...)
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "EXPORT_NETWORK_REPORT=" + network}
	client, err := Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = client.CloseStdin()
		select {
		case <-client.Done():
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-client.Done()
		}
		b, readErr := os.ReadFile(network)
		if readErr != nil || string(b) != `{"denied":0}` {
			t.Error("network control", string(b), readErr)
		}
	}()
	state, err := client.GetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID == "" || filepath.Clean(state.SessionFile) != filepath.Clean(session) {
		t.Fatalf("synthetic session was not loaded: %+v", state)
	}
	output := filepath.Join(root, "export.html")
	got, err := client.ExportHTML(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(output) {
		t.Fatalf("export path = %q, want %q", got, output)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "LOOP_NOW_EXPORT_SENTINEL") {
		t.Fatalf("exported HTML does not contain the synthetic message (%d bytes)", len(content))
	}
}
