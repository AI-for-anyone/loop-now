//go:build windows

package larksidecar

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"loop-now/internal/privatefs"
)

// scopeRoot is a directory opened through os.Root: on Windows os.Root opens with O_NOFOLLOW_ANY, so it fails if any
// segment of the path is a symlink or junction.
// The root directory's own path is resolved the normal way, and then every segment is checked for reparse points.
type scopeRoot struct {
	path string
	root *os.Root
}

func (r *scopeRoot) Close() error { return r.root.Close() }

// private reports whether only the current user can access the directory (a protected DACL containing only the
// current user).
func (r *scopeRoot) private() bool {
	f, e := r.root.Open(".")
	if e != nil {
		return false
	}
	defer f.Close()
	return privatefs.IsPrivateFile(f)
}

// openScopeRoot opens every segment of root through os.Root level by level starting from the volume root (rejecting
// reparse points at every level), and finally verifies that the directory belongs to the current user.
func openScopeRoot(root string) (*scopeRoot, error) {
	vol := filepath.VolumeName(root)
	if vol == "" || !filepath.IsAbs(root) {
		return nil, errors.New("input root unavailable")
	}
	cur, e := os.OpenRoot(vol + string(filepath.Separator))
	if e != nil {
		return nil, errors.New("input root unavailable")
	}
	for _, part := range strings.Split(strings.TrimPrefix(root, vol+string(filepath.Separator)), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		if part == "." || part == ".." {
			cur.Close()
			return nil, errors.New("input root unavailable")
		}
		next, err := cur.OpenRoot(part)
		cur.Close()
		if err != nil {
			return nil, errors.New("input root unavailable")
		}
		cur = next
	}
	dir, e := cur.Open(".")
	if e != nil {
		cur.Close()
		return nil, errors.New("input root unavailable")
	}
	owned := privatefs.OwnedByCurrentUser(dir)
	dir.Close()
	if !owned {
		cur.Close()
		return nil, errors.New("input root unavailable")
	}
	return &scopeRoot{path: root, root: cur}, nil
}

// openInputAt opens rel under root (os.Root rejects any symlink in the path); it must be a regular file.
func openInputAt(root *scopeRoot, rel string) (*os.File, error) {
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("input unavailable")
		}
	}
	f, e := root.root.OpenFile(rel, os.O_RDONLY, 0)
	if e != nil {
		return nil, errors.New("input unavailable")
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("input unavailable")
	}
	return f, nil
}

// privateDescriptor checks the opened launch descriptor file: owned by the current user, accessible only by the
// current user, a single hard link, and size in (0, limit].
func privateDescriptor(f *os.File, limit int64) bool {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) != nil {
		return false
	}
	size := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return privatefs.OwnedByCurrentUser(f) && privatefs.IsPrivateFile(f) && info.NumberOfLinks == 1 && size > 0 && size <= limit
}
