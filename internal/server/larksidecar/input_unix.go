//go:build unix

package larksidecar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// scopeRoot is a directory descriptor opened level by level with NOFOLLOW, with its ownership verified.
type scopeRoot struct{ fd int }

func (r *scopeRoot) Close() error { return unix.Close(r.fd) }

// private reports whether only the owner can access the directory itself.
func (r *scopeRoot) private() bool {
	var st unix.Stat_t
	return unix.Fstat(r.fd, &st) == nil && st.Mode&0077 == 0
}

// openScopeRoot calls openat level by level starting from /, never following a symlink in any segment, and finally
// verifies that the directory belongs to the current user.
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

// openInputAt opens rel under root with openat level by level, never following a symlink in any segment; the last
// segment is opened with O_NONBLOCK (so a FIFO does not block) and must be a regular file.
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

// privateDescriptor checks the opened launch descriptor file: owned by the current user, mode exactly 0600, a single
// hard link, and size in (0, limit].
func privateDescriptor(f *os.File, limit int64) bool {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		return false
	}
	return int(st.Uid) == os.Geteuid() && st.Mode&07777 == 0600 && st.Nlink == 1 && st.Size > 0 && st.Size <= limit
}
