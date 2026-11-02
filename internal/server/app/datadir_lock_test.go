package app_test

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
)

// 一个数据目录只给一个服务进程：第二个实例拒绝启动且不影响第一个；第一个停止后可以再启动。
func TestSecondInstanceOnSameDataDirRefused(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	cfg := w.cfg
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, err = app.Build(context.Background(), &cfg, config.NewLogger(io.Discard, cfg.LogFormat, cfg.LogLevel),
		app.Options{Listener: ln, HomeDirs: []string{"/nonexistent-home"}}); err == nil || !strings.Contains(err.Error(), "已被另一个服务进程使用") || app.ExitCode(err) != app.ExitDataDirLocked {
		t.Fatalf("第二个实例应拒绝启动：%v", err)
	}
	s.createAgent("仍可用")
	s.stop()
	w.start(nil).stop()
}
