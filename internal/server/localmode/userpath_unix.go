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

// loginShellPath runs printenv PATH in an interactive login shell to get the PATH the user set in .zprofile, .zshrc and
// the like. rc files may print other output, so the value is cut out between begin and end markers; standard input is
// the null device, and the whole process group is ended on timeout.
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
		args = []string{"-c", script} // csh's -l can only be used on its own; .cshrc is read by every csh anyway
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
	// If it was read, use it, even if the shell exited non-zero because of other commands in the rc files.
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
