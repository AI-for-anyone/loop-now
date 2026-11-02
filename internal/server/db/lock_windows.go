//go:build windows

package db

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

var errWouldBlock = errors.New("lock held")

// LockFileEx 的排他锁对同一进程的另一个句柄同样冲突；进程退出时由系统释放。
func lockFile(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
