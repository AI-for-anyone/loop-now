package modelcatalog_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
)

func TestLoadFromLocalPi(t *testing.T) {
	// 断网也能读：子进程只拿到 PATH 与临时 HOME，脚本只读本地文件。
	c, err := modelcatalog.Load(context.Background(), catalogtest.Options(t))
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
	options := catalogtest.Options(t)
	options.PiVersion = "9.9.9"
	_, err := modelcatalog.Load(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("版本不符应报错：%v", err)
	}
}

func TestLoadDoesNotTouchAgentDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	before, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if _, err := modelcatalog.Load(context.Background(), catalogtest.Options(t)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if before != nil && after != nil && !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("读取目录不应改动宿主的 Pi 配置目录")
	}
}
