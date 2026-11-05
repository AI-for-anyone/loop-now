package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"loop-now/internal/filelock"
)

// LockFile 是数据目录里的实例锁文件名。
const LockFile = "loop-now.lock"

// ErrLocked 表示另一个进程正持有这把实例锁（数据目录或工作根的）。
var ErrLocked = errors.New("db: 实例锁正被另一个 Loop Now 进程持有")

// DirLock 是一个目录的实例锁：服务和导入在整个运行期间持有，保证同一数据目录（服务还有工作根）只有一个进程在
// 调度执行、迁移和导入。进程退出时操作系统自动释放，不需要过期时间。管理子命令只做短写，不取这把锁。
type DirLock struct {
	f *os.File
}

// LockDir 以不等待的方式取得数据目录 dir 的实例锁，见 LockPath。
func LockDir(dir string) (*DirLock, error) {
	return LockPath(filepath.Join(dir, LockFile))
}

// LockPath 以不等待的方式取得锁文件 path（不存在时创建）上的实例锁，并把本进程 PID 写进锁文件（只供诊断）。
// 已被其他进程（或本进程另一次取锁）持有时返回 ErrLocked。
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

// Release 释放实例锁。锁文件保留，下次直接复用。
func (l *DirLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := errors.Join(filelock.Unlock(l.f), l.f.Close())
	l.f = nil
	return err
}
