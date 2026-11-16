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

// One data directory serves only one server process: a second instance refuses to start and does not affect the first;
// after the first stops, it can start again.
func TestSecondInstanceOnSameDataDirRefused(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	if err := buildSecond(t, w.cfg); err == nil || !strings.Contains(err.Error(), "data directory") || !strings.Contains(err.Error(), "is already used by another server process") || app.ExitCode(err) != app.ExitInstanceLocked {
		t.Fatalf("第二个实例应拒绝启动：%v", err)
	}
	s.createAgent("仍可用")
	s.stop()
	w.start(nil).stop()
}

// One work root also serves only one server process: a second instance with a different data directory but the same
// work root also refuses to start (with the same exit code), neither touching the work root nor creating a database in
// its own data directory; after the first stops, it can start.
func TestSecondInstanceOnSameWorkRootRefused(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	other := w.cfg
	other.DataDir = filepath.Join(t.TempDir(), "other-data")
	if err := buildSecond(t, other); err == nil || !strings.Contains(err.Error(), "work root") || !strings.Contains(err.Error(), "is already used by another server process") || app.ExitCode(err) != app.ExitInstanceLocked {
		t.Fatalf("第二个实例应拒绝启动：%v", err)
	}
	// Each instance creates its own directory here at startup and cleans up empty directories left by other instances.
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
