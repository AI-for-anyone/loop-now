// Package fakepibin 为测试构建 fakepi 二进制（每个测试进程只构建一次）。
package fakepibin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"loop-now/internal/testutil/testenv"
)

var (
	once sync.Once
	path string
	err  error
)

// Path 返回构建好的 fakepi 路径；构建失败时让测试失败。
func Path(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		var dir string
		dir, err = os.MkdirTemp("", "loopnow-fakepi-")
		if err != nil {
			return
		}
		path = filepath.Join(dir, "fakepi")
		cmd := exec.Command("go", "build", "-o", path, "./internal/server/executor/local/fakepi")
		cmd.Dir = testenv.RepoRoot()
		if out, e := cmd.CombinedOutput(); e != nil {
			err = fmt.Errorf("构建 fakepi 失败：%v\n%s", e, out)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}
