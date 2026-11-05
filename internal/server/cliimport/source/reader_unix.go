//go:build unix

package source

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// sourceRoot 是按 NOFOLLOW 打开的来源 HOME 目录描述符。
type sourceRoot struct{ f *os.File }

func (r *sourceRoot) Close() error { return r.f.Close() }

// id 返回来源根目录的设备号／inode，作为来源标识的一部分。
func (r *sourceRoot) id() (string, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(r.f.Fd()), &st); err != nil {
		return "", err
	}
	return fmt.Sprint(st.Dev, "/", st.Ino), nil
}

func openSourceRoot(home string) (*sourceRoot, error) {
	fd, e := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, fail("source_unreadable", "root")
	}
	return &sourceRoot{f: os.NewFile(uintptr(fd), "local-source-root")}, nil
}

// open traverses every path component with NOFOLLOW, including intermediate
// directories. Native error paths are intentionally discarded.
func (r *sourceRoot) open(name string, directory bool) (*os.File, error) {
	fd, e := unix.Dup(int(r.f.Fd()))
	if e != nil {
		return nil, fail("source_unreadable", "source")
	}
	parts := strings.Split(name, "/")
	for i, p := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 || directory {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if err != nil {
			if errors.Is(err, unix.ENOENT) {
				return nil, os.ErrNotExist
			}
			return nil, fail("source_unreadable", "source")
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "local-source"), nil
}
