// Package fakeclaudebin 为测试构建 fakeclaude 二进制（每个测试进程只构建一次）。
package fakeclaudebin

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

// Path 返回构建好的 fakeclaude 路径；构建失败时让测试失败。
func Path(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		var dir string
		dir, err = os.MkdirTemp("", "loopnow-fakeclaude-")
		if err != nil {
			return
		}
		path = testenv.Exe(filepath.Join(dir, "fakeclaude"))
		cmd := exec.Command("go", "build", "-o", path, "./internal/server/executor/local/fakeclaude")
		cmd.Dir = testenv.RepoRoot()
		if out, e := cmd.CombinedOutput(); e != nil {
			err = fmt.Errorf("构建 fakeclaude 失败：%v\n%s", e, out)
			return
		}
		// 新构建的二进制第一次执行时，系统会先检查它（macOS 上空闲时比之后慢 10～25 倍，并行跑全部测试时可达数秒）。
		// 构建后先执行一次，让这笔一次性开销落在这里，而不是算进第一个启动 Claude Code 的测试的就绪时限。
		if out, e := exec.Command(path, "--version").CombinedOutput(); e != nil {
			err = fmt.Errorf("预先执行 fakeclaude 失败：%v\n%s", e, out)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}
