// Package catalogtest provides the pinned local Pi fixture shared by model tests.
package catalogtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/preflight"
)

// Options resolves the pinned Pi 1.4.0 runtime or skips when it is unavailable.
func Options(t testing.TB) modelcatalog.Options {
	t.Helper()
	if root := os.Getenv("LOOP_NOW_TEST_PI_PACKAGE_DIR"); root != "" {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skipf("本机没有 Node：%v", err)
		}
		piAIDir := os.Getenv("LOOP_NOW_TEST_PI_AI_DIR")
		if piAIDir == "" {
			piAIDir = filepath.Join(root, "node_modules", "@earendil-works", "pi-ai")
		}
		return modelcatalog.Options{NodeBin: node, PiAIDir: piAIDir, PiPackageDir: root, PiVersion: "1.4.0", Path: os.Getenv("PATH")}
	}
	r, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(t.TempDir(), "root"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.4.0：%v", err)
	}
	return modelcatalog.Options{NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiPackageDir: r.PiPackageDir, PiVersion: "1.4.0", Path: r.ExecBasePath}
}
