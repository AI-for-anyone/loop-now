//go:build !windows

package local

import (
	"io/fs"
	"os"
)

// linkDir and linkFile both create symlinks (absolute paths) on Unix.
func linkDir(target, link string) error  { return os.Symlink(target, link) }
func linkFile(target, link string) error { return os.Symlink(target, link) }

// isLink reports whether path is the link itself.
func isLink(_ string, info fs.FileInfo) bool { return info.Mode()&fs.ModeSymlink != 0 }
