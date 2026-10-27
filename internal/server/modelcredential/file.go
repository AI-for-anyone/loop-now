package modelcredential

import (
	"errors"
	"github.com/google/uuid"
	"os"
)

var ErrFile = errors.New("模型凭证缓存写入失败")

// WriteAuthFile atomically seeds/rebuilds a runtime cache. Pi's native later
// writes are not claimed to be atomic; PG remains the credential authority.
func WriteAuthFile(dir string, data []byte) error {
	if len(data) > MaxBytes+1024 {
		return ErrFile
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrFile
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return ErrFile
	}
	defer root.Close()
	if e = root.Chmod(".", 0700); e != nil {
		return ErrFile
	}
	name := ".auth-" + uuid.NewString() + ".tmp"
	defer root.Remove(name)
	file, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return ErrFile
	}
	if _, e = file.Write(data); e == nil {
		e = file.Sync()
	}
	closeError := file.Close()
	if e != nil || closeError != nil {
		return ErrFile
	}
	if e = root.Rename(name, "auth.json"); e != nil {
		return ErrFile
	}
	return nil
}
