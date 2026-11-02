package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// LockFile 是数据目录里的实例锁文件名。
const LockFile = "loop-now.lock"

// ErrLocked 表示另一个进程正持有数据目录的实例锁。
var ErrLocked = errors.New("db: 数据目录正被另一个 Loop Now 进程使用")

// DirLock 是数据目录的实例锁：服务和导入在整个运行期间持有，保证同一数据目录只有一个进程在调度执行、
// 迁移和导入。进程退出时操作系统自动释放，不需要过期时间。管理子命令只做短写，不取这把锁。
type DirLock struct {
	f *os.File
}

// LockDir 以不等待的方式取得 dir 的实例锁，并把本进程 PID 写进锁文件（只供诊断）。
// 已被其他进程（或本进程另一次 LockDir）持有时返回 ErrLocked。
func LockDir(dir string) (*DirLock, error) {
	f, err := os.OpenFile(filepath.Join(dir, LockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("db: 打开实例锁文件失败：%w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errWouldBlock) {
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
	err := errors.Join(unlockFile(l.f), l.f.Close())
	l.f = nil
	return err
}
