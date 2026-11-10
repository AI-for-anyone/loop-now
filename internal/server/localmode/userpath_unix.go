//go:build unix

package localmode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"loop-now/internal/procgroup"
)

const (
	pathBegin = "__LOOP_NOW_USER_PATH_BEGIN__"
	pathEnd   = "__LOOP_NOW_USER_PATH_END__"
)

// loginShellPath 以交互式登录 shell 运行 printenv PATH，读到用户在 .zprofile、.zshrc 等里设置的 PATH。
// rc 文件可能输出别的内容，所以用前后标记截取；标准输入是空设备，超时后结束整个进程组。
func loginShellPath(ctx context.Context) (string, string, error) {
	shell := os.Getenv("SHELL")
	if !filepath.IsAbs(shell) {
		shell = "/bin/sh"
		if runtime.GOOS == "darwin" {
			shell = "/bin/zsh"
		}
	}
	script := "echo " + pathBegin + "; printenv PATH; echo " + pathEnd
	args := []string{"-l", "-i", "-c", script}
	if base := filepath.Base(shell); base == "csh" || base == "tcsh" {
		args = []string{"-c", script} // csh 的 -l 只能单独使用；.cshrc 对所有 csh 都会读取
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Env = os.Environ()
	cmd.Dir = os.TempDir()
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = time.Second
	out, err := procgroup.Output(cmd)
	if ctx.Err() != nil {
		return "", shell, errors.New("读取登录 shell 的 PATH 超时")
	}
	raw, ok := between(string(out), pathBegin+"\n", "\n"+pathEnd)
	if !ok {
		if err == nil {
			err = errors.New("登录 shell 没有输出 PATH")
		}
		return "", shell, err
	}
	// 读到了就用，即使 shell 因为 rc 里的其他命令以非零状态退出。
	return strings.TrimSpace(raw), shell, nil
}

func between(s, begin, end string) (string, bool) {
	i := strings.LastIndex(s, begin)
	if i < 0 {
		return "", false
	}
	s = s[i+len(begin):]
	j := strings.Index(s, end)
	if j < 0 {
		return "", false
	}
	return s[:j], true
}
