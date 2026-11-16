//go:build windows

package localmode

import (
	"context"
	"os"
)

// loginShellPath on Windows takes the server process's PATH: the desktop shell is started by Explorer, so the process
// environment already holds the system and user PATH merged at login.
func loginShellPath(context.Context) (string, string, error) {
	return os.Getenv("PATH"), "process", nil
}
