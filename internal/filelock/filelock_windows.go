//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// A LockFileEx exclusive lock also conflicts with another handle in the same process; the system releases it when
// the process exits.
// The lock covers 1 byte far beyond the end of the file (a locked range may extend past the file length): Windows
// byte-range locks are mandatory, so locking the beginning would keep other handles from reading the PID written
// there.
func tryLock(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRange())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrWouldBlock
	}
	return err
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRange())
}

// lockRange points at offset 2^62 (Offset/OffsetHigh of the Overlapped give the start of the locked range).
func lockRange() *windows.Overlapped { return &windows.Overlapped{Offset: 0, OffsetHigh: 1 << 30} }
