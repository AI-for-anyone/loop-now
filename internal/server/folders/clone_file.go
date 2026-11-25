package folders

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CloneFile copies the regular file src to dst, which must not exist: a copy-on-write clone where the file system
// supports it (APFS clonefile: instant, no extra space, the source untouched), else a plain copy. The copy is made under
// a temporary name next to dst and renamed on success. Returns the method used (clonefile / copy).
func CloneFile(src, dst string) (string, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source %s is not a regular file", src)
	}
	if _, err := os.Lstat(dst); err == nil {
		return "", fmt.Errorf("target %s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp := dst + ".copying-" + randHex()
	method := "clonefile"
	err = cloneTree(src, tmp)
	if errors.Is(err, errCloneUnsupported) {
		method = "copy"
		err = copyFile(src, tmp, 0o600)
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return method, nil
}
