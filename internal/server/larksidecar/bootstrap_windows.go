//go:build windows

package larksidecar

import (
	"errors"
	"os"
	"os/exec"
)

// execLauncher: Windows has no exec(2). As the next best thing, it runs the sandbox command with the inherited standard
// handles, waits for it to finish and exits this process with the same exit code, so the caller (Pi's shell tool)
// still sees one process and one exit code. The sandbox command itself is macOS's sandbox-exec, which is already
// rejected at the Command stage on Windows; this only keeps the behavior fully defined. It does not return on success.
func execLauncher(cmd *exec.Cmd) error {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
