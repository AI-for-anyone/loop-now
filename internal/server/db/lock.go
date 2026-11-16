package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"loop-now/internal/filelock"
)

// LockFile is the name of the instance lock file in the data directory.
const LockFile = "loop-now.lock"

// ErrLocked means another process holds this instance lock (of the data directory or the work root).
var ErrLocked = errors.New("db: 实例锁正被另一个 Loop Now 进程持有")

// DirLock is a directory's instance lock: the server and the importer hold it for their whole lifetime, so that only
// one process schedules runs, migrates and imports on a given data directory (and, for the server, work root). The
// operating system releases it automatically when the process exits, so no expiry is needed. Admin subcommands only
// do short writes and do not take this lock.
type DirLock struct {
	f *os.File
}

// LockDir takes the instance lock of data directory dir without waiting; see LockPath.
func LockDir(dir string) (*DirLock, error) {
	return LockPath(filepath.Join(dir, LockFile))
}

// LockPath takes the instance lock on lock file path (created if missing) without waiting, and writes this
// process's PID into the lock file (for diagnostics only).
// It returns ErrLocked when another process (or another lock attempt by this process) holds it.
func LockPath(path string) (*DirLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("db: 打开实例锁文件失败：%w", err)
	}
	if err := filelock.TryLock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrWouldBlock) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("db: 取得实例锁失败：%w", err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &DirLock{f: f}, nil
}

// Release releases the instance lock. The lock file is kept and reused next time.
func (l *DirLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := errors.Join(filelock.Unlock(l.f), l.f.Close())
	l.f = nil
	return err
}
