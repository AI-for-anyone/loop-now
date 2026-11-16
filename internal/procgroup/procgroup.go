// Package procgroup manages a child process together with all the descendants it starts as one group: on Unix it
// is a process group (Setpgid plus kill(-pid)), on Windows a Job Object (KILL_ON_JOB_CLOSE plus
// TerminateJobObject). Callers write the same code on both platforms: Configure before Start, Started after Start,
// and afterwards query or end the whole group by the group leader's PID.
package procgroup

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
)

// ErrNotFound means there is no process left in the group (ESRCH on Unix).
var ErrNotFound = errNotFound

// Configure makes cmd start as the leader of a new group. It must be called before cmd.Start.
func Configure(cmd *exec.Cmd) { configure(cmd) }

// Started registers a cmd that has been started successfully: on Windows this is where the Job Object is created
// and the process put into it; on Unix there is nothing to do.
// An error means the process could not be brought under group management, and the caller should end it.
func Started(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("procgroup: 进程尚未启动")
	}
	return started(cmd)
}

// Kill immediately ends the whole group whose leader is pid. It returns ErrNotFound when the group no longer
// exists.
func Kill(pid int) error { return kill(pid) }

// Terminate asks the whole group to stop: on Unix it sends SIGTERM; Windows has no termination signal that can be
// delivered to an arbitrary process, so there it is the same as Kill.
func Terminate(pid int) error { return terminate(pid) }

// Probe checks whether the group still has a process: nil if it has, ErrNotFound if not; any other error means it
// could not be determined.
func Probe(pid int) error { return probe(pid) }

// Alive reports whether the group still has a process.
func Alive(pid int) bool { return probe(pid) == nil }

// ProcessAlive reports whether the process pid itself still exists (its group is not considered).
func ProcessAlive(pid int) bool { return processAlive(pid) }

// Cancel returns a function suitable for cmd.Cancel: it ends the whole group, and treats a group that no longer
// exists as a process that has already finished.
func Cancel(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := kill(cmd.Process.Pid)
		if errors.Is(err, ErrNotFound) {
			return os.ErrProcessDone
		}
		return err
	}
}

// Start combines the three steps Configure, cmd.Start and Started: it returns an error when the start fails or the
// process cannot be brought under group management (in the latter case the process has already been ended).
// For a cmd built with exec.CommandContext the caller sets cmd.Cancel = Cancel(cmd) itself (a cmd without a
// Context may not set Cancel).
func Start(cmd *exec.Cmd) error {
	configure(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := started(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return nil
}

// Run is like cmd.Run, but the process is started as a group.
func Run(cmd *exec.Cmd) error {
	if err := Start(cmd); err != nil {
		return err
	}
	return cmd.Wait()
}

// Output is like cmd.Output (when standard error is not set, it is returned with the *exec.ExitError), but the
// process is started as a group.
func Output(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil {
		return nil, errors.New("procgroup: Stdout 已经设置")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr *bytes.Buffer
	if cmd.Stderr == nil {
		stderr = &bytes.Buffer{}
		cmd.Stderr = stderr
	}
	err := Run(cmd)
	if err != nil && stderr != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exit.Stderr = stderr.Bytes()
		}
	}
	return stdout.Bytes(), err
}

// CombinedOutput is like cmd.CombinedOutput, but the process is started as a group.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	if cmd.Stdout != nil || cmd.Stderr != nil {
		return nil, errors.New("procgroup: Stdout 或 Stderr 已经设置")
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := Run(cmd)
	return out.Bytes(), err
}
