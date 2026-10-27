package larksidecar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Roots come from captured runtime metadata, never argv, env, profile or CWD.
// Every component is opened beneath an fd with NOFOLLOW; JSON is read before
// sandbox exec, so a path check followed by a pathname open is insufficient.
func openScopeInput(path, base string, roots []string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if !filepath.IsAbs(root) || root != filepath.Clean(root) {
			continue
		}
		rel, e := filepath.Rel(root, path)
		if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		fd, e := openScopeRoot(root)
		if e != nil {
			return nil, e
		}
		f, e := openInputAt(fd, rel)
		unix.Close(fd)
		return f, e
	}
	return nil, errors.New("input outside scope")
}
func openScopeRoot(root string) (int, error) {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return -1, errors.New("input root unavailable")
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, "/"), "/") {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			unix.Close(fd)
			return -1, errors.New("input root unavailable")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return -1, errors.New("input root unavailable")
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || int(st.Uid) != os.Geteuid() {
		unix.Close(fd)
		return -1, errors.New("input root unavailable")
	}
	return fd, nil
}
func openInputAt(rootfd int, rel string) (*os.File, error) {
	fd, e := unix.Openat(rootfd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
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
