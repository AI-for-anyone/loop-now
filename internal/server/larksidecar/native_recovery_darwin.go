//go:build darwin

package larksidecar

import (
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func nativeBootID() (string, error) {
	id, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil || strings.TrimSpace(id) == "" {
		return "", ErrNativeRecoveryUnknown
	}
	return strings.TrimSpace(id), nil
}

func readNativeProcess(pid int) (NativeProcessIdentity, error) {
	var out NativeProcessIdentity
	if pid <= 1 {
		return out, ErrNativeRecoveryUnknown
	}
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return out, syscall.ESRCH
		}
		return out, ErrNativeRecoveryUnknown
	}
	if int(info.Proc.P_pid) != pid {
		return out, ErrNativeRecoveryUnknown
	}
	return NativeProcessIdentity{PID: pid, PGID: int(info.Eproc.Pgid), UID: info.Eproc.Ucred.Uid, StartSeconds: info.Proc.P_starttime.Sec, StartMicroseconds: int64(info.Proc.P_starttime.Usec)}, nil
}
