// Package filelock 是进程级的文件排他锁：Unix 用 flock，Windows 用 LockFileEx。两者都随打开的文件描述走，
// 同一进程再次打开同一文件也会冲突，进程退出时由系统释放。
package filelock

import (
	"errors"
	"os"
)

// ErrWouldBlock 表示锁正被别的描述持有。
var ErrWouldBlock = errors.New("filelock: 锁正被持有")

// TryLock 以不等待的方式取 f 上的排他锁；已被持有时返回 ErrWouldBlock。
func TryLock(f *os.File) error { return tryLock(f) }

// Unlock 释放 f 上的锁。
func Unlock(f *os.File) error { return unlock(f) }
