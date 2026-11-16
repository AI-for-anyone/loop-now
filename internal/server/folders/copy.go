package folders

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CopyResult records the method and duration of one folder copy.
type CopyResult struct {
	Method   string // clonefile / copy / empty
	Duration time.Duration
}

// CopyTree copies directory src as a whole to dst: the copy is completed under a temporary name next to dst and then
// renamed to dst on success; on failure the partial copy is deleted. A copy-on-write clone is preferred (APFS
// clonefile, instant and taking no extra space); when the file system does not support it, it falls back to a plain
// copy (keeping permissions and symlinks, skipping special files such as sockets and devices). When src does not exist,
// dst is an empty directory. Returns an error when dst already exists.
func CopyTree(src, dst string) (CopyResult, error) {
	begin := time.Now()
	if _, err := os.Lstat(dst); err == nil {
		return CopyResult{}, fmt.Errorf("target %s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return CopyResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return CopyResult{}, err
	}
	info, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return CopyResult{Method: "empty", Duration: time.Since(begin)}, os.Mkdir(dst, 0o700)
	}
	if err != nil {
		return CopyResult{}, err
	}
	if !info.IsDir() {
		return CopyResult{}, fmt.Errorf("source %s is not a directory", src)
	}
	tmp := dst + ".copying-" + randHex()
	method := "clonefile"
	err = cloneTree(src, tmp)
	if errors.Is(err, errCloneUnsupported) {
		method = "copy"
		err = plainCopy(src, tmp)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return CopyResult{}, err
	}
	return CopyResult{Method: method, Duration: time.Since(begin)}, nil
}

var errCloneUnsupported = errors.New("the file system does not support cloning")

// plainCopy copies a directory recursively: it keeps permissions and symlinks (copying the link contents as is) and
// skips special files such as sockets and devices.
func plainCopy(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode.IsDir():
			return os.Mkdir(target, mode.Perm()|0o700)
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case mode.IsRegular():
			return copyFile(p, target, mode.Perm())
		}
		return nil
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
