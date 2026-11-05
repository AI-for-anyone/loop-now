//go:build unix

package privatefs

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

const (
	noFollow = syscall.O_NOFOLLOW
	nonBlock = syscall.O_NONBLOCK
)

func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

func makePrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}

func isPrivate(_ string, info fs.FileInfo) bool { return info.Mode().Perm()&0o077 == 0 }

func isPrivateFile(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode().Perm()&0o077 == 0
}

func writableByOthersFile(f *os.File) bool {
	info, err := f.Stat()
	return err != nil || info.Mode().Perm()&0o022 != 0
}

func worldWritableFile(f *os.File) bool {
	info, err := f.Stat()
	return err != nil || info.Mode().Perm()&0o002 != 0
}

func ownedByCurrentUser(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && ownedByCurrentUserPath("", info)
}

func ownedByCurrentUserPath(_ string, info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func isExecutable(_ string, info fs.FileInfo) bool { return info.Mode().Perm()&0o111 != 0 }

func isNotDir(err error) bool      { return errors.Is(err, syscall.ENOTDIR) }
func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
