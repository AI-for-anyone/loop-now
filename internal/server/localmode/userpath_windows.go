//go:build windows

package localmode

import (
	"context"
	"os"
)

// loginShellPath 在 Windows 上取服务端进程的 PATH：桌面壳由资源管理器启动，进程环境里就是登录时系统与用户 PATH 的合并结果。
func loginShellPath(context.Context) (string, string, error) {
	return os.Getenv("PATH"), "process", nil
}
