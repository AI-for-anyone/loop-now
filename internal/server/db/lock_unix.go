//go:build unix

package db

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var errWouldBlock = unix.EWOULDBLOCK

// flock 锁跟随打开的文件描述，同一进程再次打开同一文件也会冲突；进程退出时自动释放。
func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EAGAIN) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
