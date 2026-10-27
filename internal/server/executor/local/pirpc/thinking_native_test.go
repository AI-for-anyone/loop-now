package pirpc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/preflight"
)

// Real installed Pi 1.4 RPC, builtin model capabilities and synthetic session;
// no Prompt/model request, credentials, user HOME or network is used.
func TestInstalledPi14ThinkingResumeDefaultAndClampVerification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	root := t.TempDir()
	r, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(root, "work"), HomeDirs: []string{"/nonexistent"}})
	if e != nil {
		t.Fatal(e)
	}
	preload := filepath.Join(root, "deny.mjs")
	if e = os.WriteFile(preload, []byte(`import net from 'node:net';import tls from 'node:tls';import dgram from 'node:dgram';import {writeFileSync} from 'node:fs';let denied=0;const reject=()=>{denied++;throw Error('network denied by thinking fixture')};net.Socket.prototype.connect=reject;tls.connect=reject;dgram.createSocket=reject;globalThis.fetch=reject;process.on('exit',()=>writeFileSync(process.env.THINKING_NETWORK_REPORT,JSON.stringify({denied})));`), 0600); e != nil {
		t.Fatal(e)
	}
	session := filepath.Join(root, "session.jsonl")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var raw []byte
	for _, entry := range []any{
		map[string]any{"type": "session", "version": 3, "id": uuid.NewString(), "timestamp": now, "cwd": root},
		map[string]any{"type": "thinking_level_change", "id": "thinking-entry", "parentId": nil, "timestamp": now, "thinkingLevel": "high"},
		map[string]any{"type": "message", "id": "user-entry", "parentId": "thinking-entry", "timestamp": now, "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "TEST synthetic session; no provider call"}}, "timestamp": time.Now().UnixMilli()}},
	} {
		line, _ := json.Marshal(entry)
		raw = append(raw, line...)
		raw = append(raw, '\n')
	}
	if e = os.WriteFile(session, raw, 0600); e != nil {
		t.Fatal(e)
	}
	start := func(label string, extra ...string) (*Client, func()) {
		t.Helper()
		network := filepath.Join(root, label+"-network.json")
		args := []string{"--import", preload, filepath.Join(r.PiPackageDir, "dist/bundle/cli.js"), "--mode", "rpc", "--offline", "--session", session, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--provider", "openai-codex", "--model", "gpt-6.1-sol"}
		args = append(args, extra...)
		cmd := exec.CommandContext(ctx, r.NodeBin, args...)
		cmd.Dir = root
		cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "PI_CODING_AGENT_DIR=" + filepath.Join(root, "agent"), "PI_OFFLINE=1", "PI_TELEMETRY=0", "THINKING_NETWORK_REPORT=" + network}
		client, e := Start(cmd)
		if e != nil {
			t.Fatal(e)
		}
		close := func() {
			_ = client.CloseStdin()
			select {
			case <-client.Done():
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
				<-client.Done()
			}
			b, e := os.ReadFile(network)
			if e != nil || string(b) != `{"denied":0}` {
				t.Error("network control", string(b), e)
			}
		}
		return client, close
	}
	first, closeFirst := start("resume")
	func() {
		defer closeFirst()
		state, e := first.GetState(ctx)
		if e != nil || state.ThinkingLevel != "high" {
			t.Fatal("session resume positive control", state, e)
		}
		levels, e := first.Call(ctx, "get_available_thinking_levels", nil)
		if e != nil {
			t.Fatal(e)
		}
		var available struct{ Levels []string }
		if json.Unmarshal(levels, &available) != nil || !slices.Contains(available.Levels, "max") || slices.Contains(available.Levels, "off") {
			t.Fatal("actual model levels", string(levels))
		}
		if e = first.SetThinkingLevel(ctx, "medium"); e != nil {
			t.Fatal("same-process default reset", e)
		}
		if e = first.SetThinkingLevel(ctx, "off"); e == nil {
			t.Fatal("RPC clamp incorrectly reported exact success")
		}
		if e = first.SetThinkingLevel(ctx, "high"); e != nil {
			t.Fatal(e)
		}
	}()
	second, closeSecond := start("explicit-default", "--thinking", "medium")
	defer closeSecond()
	state, e := second.GetState(ctx)
	if e != nil || state.ThinkingLevel != "medium" {
		t.Fatal("explicit default failed to override resumed high", state, e)
	}
}
