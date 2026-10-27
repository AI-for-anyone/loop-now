package modelcatalog_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/preflight"
)

func localPi(t *testing.T) *preflight.Resolved {
	t.Helper()
	r, err := preflight.Resolve(context.Background(), preflight.Options{
		PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "root"), HomeDirs: []string{"/nonexistent-home"},
	})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.1.0：%v", err)
	}
	return r
}

func TestLoadFromLocalPi(t *testing.T) {
	r := localPi(t)
	// 断网也能读：子进程只拿到 PATH 与临时 HOME，脚本只读本地文件。
	c, err := modelcatalog.Load(context.Background(), modelcatalog.Options{
		NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiVersion: "1.1.0", Path: r.ExecBasePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) < 10 {
		t.Fatalf("提供方太少：%d", len(c.Providers))
	}
	anthropic, ok := c.Provider("anthropic")
	if !ok || len(anthropic.Models) == 0 {
		t.Fatal("目录中应有 anthropic 及其模型")
	}
	if !slices.Contains(anthropic.APIKeyEnvVars, "ANTHROPIC_API_KEY") {
		t.Fatalf("anthropic 应附带 ANTHROPIC_API_KEY：%v", anthropic.APIKeyEnvVars)
	}
	if !c.HasModel("anthropic", anthropic.Models[0].ID) || c.HasModel("anthropic", "no-such-model") {
		t.Fatal("HasModel 结果不对")
	}
	if c.GeneratedAt == nil {
		t.Fatal("应带目录生成时间")
	}
}

func TestLoadRejectsVersionMismatch(t *testing.T) {
	r := localPi(t)
	_, err := modelcatalog.Load(context.Background(), modelcatalog.Options{
		NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiVersion: "9.9.9", Path: r.ExecBasePath,
	})
	if err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("版本不符应报错：%v", err)
	}
}

func TestLoadDoesNotTouchAgentDir(t *testing.T) {
	r := localPi(t)
	home, _ := os.UserHomeDir()
	before, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if _, err := modelcatalog.Load(context.Background(), modelcatalog.Options{
		NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiVersion: "1.1.0", Path: r.ExecBasePath,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if before != nil && after != nil && !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("读取目录不应改动宿主的 Pi 配置目录")
	}
}
