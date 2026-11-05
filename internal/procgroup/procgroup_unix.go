//go:build unix

package procgroup

import (
	"os/exec"
	"syscall"
)

var errNotFound error = syscall.ESRCH

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

func started(*exec.Cmd) error { return nil }

func kill(pid int) error      { return syscall.Kill(-pid, syscall.SIGKILL) }
func terminate(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }
func probe(pid int) error     { return syscall.Kill(-pid, 0) }

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
