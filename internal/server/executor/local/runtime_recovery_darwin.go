//go:build darwin

package local

import (
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func recoveryBootID() (string, error) {
	id, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || strings.TrimSpace(id) == "" {
		return "", ErrRuntimeStopUnconfirmed
	}
	return strings.TrimSpace(id), nil
}
func readRecoveryProcess(pid int) (RuntimeProcessIdentity, error) {
	var out RuntimeProcessIdentity
	if pid <= 1 {
		return out, ErrRuntimeStopUnconfirmed
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return out, syscall.ESRCH
		}
		return out, err
	}
	if int(info.Proc.P_pid) != pid {
		return out, ErrRuntimeStopUnconfirmed
	}
	out = RuntimeProcessIdentity{PID: pid, PGID: int(info.Eproc.Pgid), UID: info.Eproc.Ucred.Uid, StartSeconds: info.Proc.P_starttime.Sec, StartMicroseconds: int64(info.Proc.P_starttime.Usec)}
	return out, nil
}
