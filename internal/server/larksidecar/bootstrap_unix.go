//go:build unix

package larksidecar

import (
	"os/exec"
	"syscall"
)

// execLauncher replaces this process with the sandbox command via exec: the Pi/bash process group and standard
// descriptors are kept, and there is no extra supervising child process. It does not return on success.
func execLauncher(cmd *exec.Cmd) error { return syscall.Exec(cmd.Path, cmd.Args, cmd.Env) }
