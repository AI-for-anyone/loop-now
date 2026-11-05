package app

import (
	"errors"
	"syscall"

	"loop-now/internal/server/db"
)

// loop-server 启动失败时的退出码，桌面壳据此给出具体提示。配置有误为 2（见 cmd/loop-server），其他启动失败为 1。
const (
	ExitInstanceLocked = 3 // 数据目录或工作根正被另一个服务进程使用
	ExitAddrInUse      = 4 // 监听地址已被占用（控制台端口或对外端口）
)

// ExitCode 返回 Build 失败时应使用的退出码。
func ExitCode(err error) int {
	switch {
	case errors.Is(err, db.ErrLocked):
		return ExitInstanceLocked
	case errors.Is(err, syscall.EADDRINUSE):
		return ExitAddrInUse
	}
	return 1
}
