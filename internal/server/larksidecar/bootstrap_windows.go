//go:build windows

package larksidecar

import (
	"errors"
	"os"
	"os/exec"
)

// execLauncher：Windows 没有 exec(2)。退而求其次：以继承的标准句柄运行沙箱命令，等它结束后以同样的退出码退出本进程，
// 调用方（Pi 的 shell 工具）看到的仍是一个进程、一个退出码。沙箱命令本身是 macOS 的 sandbox-exec，Windows 上 Command 阶段就会拒绝，
// 这里只保证行为定义完整。成功不返回。
func execLauncher(cmd *exec.Cmd) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
