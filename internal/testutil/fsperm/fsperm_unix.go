//go:build unix

package fsperm

import "os"

func open(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o777)
	}
	return os.Chmod(path, 0o666)
}
