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

// CopyResult 记下一次文件夹复制的方式与耗时。
type CopyResult struct {
	Method   string // clonefile / copy / empty
	Duration time.Duration
}

// CopyTree 把目录 src 整体复制为 dst：先在 dst 旁边的临时名下完成，成功后再改名为 dst，失败时删除半成品。
// 优先用写时复制的克隆（APFS clonefile，瞬间完成、不占额外空间），文件系统不支持时退回普通复制
// （保留权限与软链接，跳过 socket、设备等特殊文件）。src 不存在时 dst 为空目录。dst 已存在时返回错误。
func CopyTree(src, dst string) (CopyResult, error) {
	begin := time.Now()
	if _, err := os.Lstat(dst); err == nil {
		return CopyResult{}, fmt.Errorf("目标 %s 已存在", dst)
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
		return CopyResult{}, fmt.Errorf("来源 %s 不是目录", src)
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

var errCloneUnsupported = errors.New("文件系统不支持克隆")

// plainCopy 递归复制目录：保留权限与软链接（原样复制链接内容），跳过 socket、设备等特殊文件。
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
