package app_test

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"loop-now/internal/server/app"
	"loop-now/internal/server/db"
)

// A locked data directory or work root and a listen address in use each have a fixed exit code; other failures are 1.
func TestExitCode(t *testing.T) {
	dir := t.TempDir()
	held, err := db.LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	_, locked := db.LockDir(dir)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, inUse := net.Listen("tcp", ln.Addr().String())
	for err, want := range map[error]int{
		fmt.Errorf("数据目录 x 已被另一个服务进程使用：%w", locked): app.ExitInstanceLocked,
		fmt.Errorf("监听 x 失败：%w", inUse):             app.ExitAddrInUse,
		errors.New("其他启动失败"):                        1,
	} {
		if got := app.ExitCode(err); got != want {
			t.Errorf("%v：退出码 %d，期望 %d", err, got, want)
		}
	}
}
