//go:build unix

package larksidecar

import (
	"os/exec"
	"syscall"
)

// execLauncher 用 exec 把本进程替换为沙箱命令：保留 Pi/bash 的进程组与标准描述符，没有多出来的监督子进程。成功不返回。
func execLauncher(cmd *exec.Cmd) error { return syscall.Exec(cmd.Path, cmd.Args, cmd.Env) }
