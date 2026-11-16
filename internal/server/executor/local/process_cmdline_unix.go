//go:build unix

package local

import (
	"os/exec"
	"strconv"
)

// processCommandLine returns the command line of pid (via ps), used to check old pi.pid files that have no recorded
// start time.
func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
