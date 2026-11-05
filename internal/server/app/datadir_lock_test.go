package app_test

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/server/db"
)

func buildSecond(t *testing.T, cfg config.Config) error {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, cfg.LogFormat, cfg.LogLevel),
		app.Options{Listener: ln, HomeDirs: []string{"/nonexistent-home"}})
	return err
}

// 一个数据目录只给一个服务进程：第二个实例拒绝启动且不影响第一个；第一个停止后可以再启动。
func TestSecondInstanceOnSameDataDirRefused(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	if err := buildSecond(t, w.cfg); err == nil || !strings.Contains(err.Error(), "数据目录") || !strings.Contains(err.Error(), "已被另一个服务进程使用") || app.ExitCode(err) != app.ExitInstanceLocked {
		t.Fatalf("第二个实例应拒绝启动：%v", err)
	}
	s.createAgent("仍可用")
	s.stop()
	w.start(nil).stop()
}

// 一个工作根也只给一个服务进程：数据目录不同、工作根相同的第二个实例同样拒绝启动（退出码相同），
// 既不动工作根，也不在自己的数据目录里建库；第一个停止后它可以启动。
func TestSecondInstanceOnSameWorkRootRefused(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	other := w.cfg
	other.DataDir = filepath.Join(t.TempDir(), "other-data")
	if err := buildSecond(t, other); err == nil || !strings.Contains(err.Error(), "工作根") || !strings.Contains(err.Error(), "已被另一个服务进程使用") || app.ExitCode(err) != app.ExitInstanceLocked {
		t.Fatalf("第二个实例应拒绝启动：%v", err)
	}
	// 每个实例启动时在这里建自己的目录，并清理别的实例留下的空目录。
	if instances, _ := filepath.Glob(filepath.Join(w.cfg.WorkRoot, ".cli-verification", "*")); len(instances) != 1 {
		t.Fatalf("被拒绝的实例不应动工作根：%v", instances)
	}
	if _, err := os.Stat(filepath.Join(config.DBDir(other.DataDir), db.DataFile)); !os.IsNotExist(err) {
		t.Fatalf("被拒绝的实例不应建库：%v", err)
	}
	s.createAgent("仍可用")
	s.stop()
	w.start(func(c *config.Config) { c.DataDir = other.DataDir }).stop()
}
