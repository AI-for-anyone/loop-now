//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// LockFileEx 的排他锁对同一进程的另一个句柄同样冲突；进程退出时由系统释放。
// 锁的是文件末端之外很远的 1 字节（锁区可以超出文件长度）：Windows 的字节区锁是强制锁，锁住开头会让别的句柄读不到
// 写在那里的 PID。
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

// lockRange 指向偏移 2^62 处（Overlapped 的 Offset/OffsetHigh 给出锁区起点）。
func lockRange() *windows.Overlapped { return &windows.Overlapped{Offset: 0, OffsetHigh: 1 << 30} }
