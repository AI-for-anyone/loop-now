//go:build darwin

package local

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// processStart 返回内核记录的进程启动时刻（精确到微秒），与 PID 一起确认它没有被别的进程复用。
func processStart(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(info.Proc.P_pid) != pid {
		return "", fmt.Errorf("进程 %d 不存在", pid)
	}
	return fmt.Sprintf("%d.%06d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
