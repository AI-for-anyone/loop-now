//go:build !windows

package local

import (
	"io/fs"
	"os"
)

// linkDir 与 linkFile 在 Unix 上都是软链接（绝对路径）。
func linkDir(target, link string) error  { return os.Symlink(target, link) }
func linkFile(target, link string) error { return os.Symlink(target, link) }

// isLink 报告 path 是不是链接本身。
func isLink(_ string, info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }
