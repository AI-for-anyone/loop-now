package app_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
)

// requirePi 在本机没有 pi 时跳过：这些测试要读真实 Pi 的模型目录（CI 不装 Pi）。
func requirePi(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("本机没有 pi")
	}
}

// isolatedWorld uses only its own data directory, with no
// stale namespace sweeps, real auth, model, native CLI install or business task.
func isolatedWorld(t *testing.T) *world {
	t.Helper()
	requirePi(t)
	ctx := context.Background()
	node := filepath.Join(os.Getenv("HOME"), ".local/node-v24.21.0/bin/node")
	if _, e := os.Stat(node); e != nil {
		node = "node" // 没有固定版本的 Node 时用 PATH 里的
	}
	real, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: node, WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent"}})
	if e != nil {
		t.Fatal("offline Pi/catalog unavailable", e)
	}
	fake := fakepibin.Path(t)
	wrapper := filepath.Join(t.TempDir(), "pi")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 1.4.0; exit 0; fi\nexec " + fake + " \"$@\"\n"
	if e = os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	return &world{t: t, piAIDir: real.PiAIDir, cfg: config.Config{
		DataDir: t.TempDir(), ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://127.0.0.1:4342"}, DefaultOrgName: "org", DefaultProjectName: "project", WorkRoot: filepath.Join(t.TempDir(), "work"), PiBin: wrapper, PiVersion: "1.4.0", PiAIDir: real.PiAIDir, NodeBin: node, PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second, PiStopGrace: time.Second, AbortTimeout: time.Second, GitCloneTimeout: time.Minute, CLIInstallTimeout: time.Minute, ReconcileInterval: 100 * time.Millisecond, SSEKeepalive: time.Second, EventStreamMaxLen: 1000, ShutdownTimeout: 10 * time.Second, MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100, TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, LoopctlDistDir: loopctlfake.Dir(t), TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 100 * time.Millisecond, TaskRecordTimeout: time.Second}}
}
