// Package filelock provides process-level exclusive file locks: flock on Unix, LockFileEx on Windows. Both are tied
// to the open file description, so opening the same file again in the same process also conflicts, and the system
// releases the lock when the process exits.
package filelock

import (
	"errors"
	"os"
)

// ErrWouldBlock means the lock is held by another file description.
var ErrWouldBlock = errors.New("filelock: 锁正被持有")

// TryLock takes an exclusive lock on f without waiting; it returns ErrWouldBlock when the lock is already held.
func TryLock(f *os.File) error { return tryLock(f) }

// Unlock releases the lock on f.
func Unlock(f *os.File) error { return unlock(f) }
