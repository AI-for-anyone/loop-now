package app

import (
	"errors"

	"loop-now/internal/server/db"
)

// Exit codes of loop-server when startup fails; the desktop shell shows a specific hint based on them. A config error
// is 2 (see cmd/loop-server); any other startup failure is 1.
const (
	ExitInstanceLocked = 3 // the data directory or the work root is in use by another server process
	ExitAddrInUse      = 4 // the listen address is already in use (console port or external port)
)

// ExitCode returns the exit code to use when Build fails.
func ExitCode(err error) int {
	switch {
	case errors.Is(err, db.ErrLocked):
		return ExitInstanceLocked
	case addrInUse(err):
		return ExitAddrInUse
	}
	return 1
}
