//go:build windows

package source

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// sourceRoot is the source HOME opened through os.Root: on Windows os.Root opens with O_NOFOLLOW_ANY, so it fails if
// any segment of the path is a symlink or junction, which matches the effect of per-segment NOFOLLOW on Unix.
type sourceRoot struct{ root *os.Root }

func (r *sourceRoot) Close() error { return r.root.Close() }

// id returns the volume serial number/file index of the source root directory (GetFileInformationByHandle), as part
// of the source identity.
func (r *sourceRoot) id() (string, error) {
	f, err := r.root.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprint(info.VolumeSerialNumber, "/", uint64(info.FileIndexHigh)<<32|uint64(info.FileIndexLow)), nil
}

func openSourceRoot(home string) (*sourceRoot, error) {
	info, e := os.Lstat(home)
	if e != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, fail("source_unreadable", "root")
	}
	root, e := os.OpenRoot(home)
	if e != nil {
		return nil, fail("source_unreadable", "root")
	}
	return &sourceRoot{root: root}, nil
}

func (r *sourceRoot) open(name string, directory bool) (*os.File, error) {
	f, e := r.root.OpenFile(filepath.FromSlash(name), os.O_RDONLY, 0)
	if e != nil {
		if errors.Is(e, fs.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fail("source_unreadable", "source")
	}
	// Intermediate segments must be directories, which os.Root enforces while resolving the path. The type of the last
	// segment is not checked here (directory is unused on Windows); Reader.openPath checks it with Stat after open. A
	// backslash would act as a separator on Windows, so names containing one are rejected.
	if strings.Contains(name, "\\") {
		f.Close()
		return nil, fail("unsupported", "source_reference")
	}
	_ = directory
	return f, nil
}
