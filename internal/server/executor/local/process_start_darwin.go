//go:build darwin

package local

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// processStart returns the process start time recorded by the kernel (to the microsecond), which together with the PID
// confirms the PID has not been reused by another process.
func processStart(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(info.Proc.P_pid) != pid {
		return "", fmt.Errorf("process %d does not exist", pid)
	}
	return fmt.Sprintf("%d.%06d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
