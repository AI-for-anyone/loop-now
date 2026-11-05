//go:build unix

package larksidecar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// scopeRoot 是一个按 NOFOLLOW 逐级打开、已核对归属的目录描述符。
type scopeRoot struct{ fd int }

func (r *scopeRoot) Close() error { return unix.Close(r.fd) }

// private 报告目录本身是否只有所有者能访问。
func (r *scopeRoot) private() bool {
	var st unix.Stat_t
	return unix.Fstat(r.fd, &st) == nil && st.Mode&0077 == 0
}

// openScopeRoot 从 / 开始逐级 openat，每一段都不跟随符号链接，最后核对目录属于当前用户。
func openScopeRoot(root string) (*scopeRoot, error) {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errors.New("input root unavailable")
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			unix.Close(fd)
			return nil, errors.New("input root unavailable")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return nil, errors.New("input root unavailable")
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != os.Geteuid() {
		unix.Close(fd)
		return nil, errors.New("input root unavailable")
	}
	return &scopeRoot{fd: fd}, nil
}

// openInputAt 在 root 之下逐级 openat 打开 rel，每一段都不跟随符号链接；最后一段以 O_NONBLOCK 打开（FIFO 不阻塞），且必须是普通文件。
func openInputAt(root *scopeRoot, rel string) (*os.File, error) {
	fd, e := unix.Openat(root.fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errors.New("input unavailable")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			unix.Close(fd)
			return nil, errors.New("input unavailable")
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		} else {
			flags |= unix.O_NONBLOCK
		}
		next, err := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if err != nil {
			return nil, errors.New("input unavailable")
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, errors.New("input unavailable")
	}
	return os.NewFile(uintptr(fd), "scope-input"), nil
}

// privateDescriptor 核对已打开的启动描述文件：属于当前用户、模式恰为 0600、只有一个硬链接、大小在 (0, limit]。
func privateDescriptor(f *os.File, limit int64) bool {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		return false
	}
	return int(st.Uid) == os.Geteuid() && st.Mode&07777 == 0600 && st.Nlink == 1 && st.Size > 0 && st.Size <= limit
}
