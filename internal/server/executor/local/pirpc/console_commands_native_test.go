package pirpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/preflight"
)

// Real installed Pi 1.4 `pi --offline --export <session> <out>` (what the platform's /export html runs) on a synthetic
// session. Pi embeds the session as base64 JSON in <script id="session-data">. No model request, credentials,
// user HOME or network is used.
func TestInstalledPi14ExportFile(t *testing.T) {
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
	network, output := filepath.Join(root, "network.json"), filepath.Join(root, "export.html")
	cmd := exec.CommandContext(ctx, r.NodeBin, "--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--offline", "--export", session, output)
	cmd.Dir = root
	cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "EXPORT_NETWORK_REPORT=" + network}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pi --export: %v %s", err, out)
	}
	if b, err := os.ReadFile(network); err != nil || string(b) != `{"denied":0}` {
		t.Error("network control", string(b), err)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<script id="session-data" type="application/json">([A-Za-z0-9+/=]+)</script>`).FindSubmatch(content)
	if m == nil {
		t.Fatalf("exported HTML has no session data (%d bytes)", len(content))
	}
	data, err := base64.StdEncoding.DecodeString(string(m[1]))
	if err != nil || !strings.Contains(string(data), "LOOP_NOW_EXPORT_SENTINEL") {
		t.Fatalf("exported session data does not contain the synthetic message: %v", err)
	}
}

// Real installed Pi 1.4 compact on synthetic sessions it refuses before any model call: a one-message session
// ("Nothing to compact") and one whose last entry is a compaction ("Already compacted"). Both are normal skipped
// outcomes, not errors. No model request, credentials, user HOME or network is used.
func TestInstalledPi14CompactSkips(t *testing.T) {
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("本机没有 pi")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	user := map[string]any{"type": "message", "id": "user-entry", "parentId": nil, "timestamp": now,
		"message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hello"}}, "timestamp": time.Now().UnixMilli()}}
	// Pi 打开已有会话时，缺少思考级别记录就补一条：先写上，压缩才是最后一条记录。
	model := map[string]any{"type": "model_change", "id": "model-entry", "parentId": "user-entry", "timestamp": now, "provider": "openai-codex", "modelId": "gpt-6.1-sol"}
	thinking := map[string]any{"type": "thinking_level_change", "id": "thinking-entry", "parentId": "model-entry", "timestamp": now, "thinkingLevel": "medium"}
	compaction := map[string]any{"type": "compaction", "id": "compaction-entry", "parentId": "thinking-entry", "timestamp": now,
		"summary": "earlier work", "firstKeptEntryId": "user-entry", "tokensBefore": 1000}
	for name, c := range map[string]struct {
		entries []any
		want    string
	}{
		"too small":         {[]any{user}, CompactTooSmall},
		"already compacted": {[]any{user, model, thinking, compaction}, CompactAlreadyDone},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			root := t.TempDir()
			r, err := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(root, "work"), HomeDirs: []string{"/nonexistent"}})
			if err != nil {
				t.Fatal(err)
			}
			preload := filepath.Join(root, "deny.mjs")
			const networkGuard = `import net from 'node:net';import tls from 'node:tls';import dgram from 'node:dgram';const reject=()=>{throw Error('network denied by compact fixture')};net.Socket.prototype.connect=reject;tls.connect=reject;dgram.createSocket=reject;globalThis.fetch=reject;`
			if err = os.WriteFile(preload, []byte(networkGuard), 0600); err != nil {
				t.Fatal(err)
			}
			session := filepath.Join(root, "session.jsonl")
			raw, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": uuid.NewString(), "timestamp": now, "cwd": root})
			raw = append(raw, '\n')
			for _, entry := range c.entries {
				line, _ := json.Marshal(entry)
				raw = append(append(raw, line...), '\n')
			}
			if err = os.WriteFile(session, raw, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--mode", "rpc", "--offline", "--session", session, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--provider", "openai-codex", "--model", "gpt-6.1-sol"}
			cmd := exec.CommandContext(ctx, r.NodeBin, args...)
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0"}
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
			}()
			res, err := client.Compact(ctx, "")
			if err != nil || res.Skipped != c.want {
				t.Fatalf("应是没有压缩（%s）：%+v %v", c.want, res, err)
			}
		})
	}
}
