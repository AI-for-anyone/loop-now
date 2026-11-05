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

// sourceRoot 是经 os.Root 打开的来源 HOME：Windows 上 os.Root 以 O_NOFOLLOW_ANY 打开，路径中任何一段是符号链接或 junction 都失败，
// 与 Unix 上逐段 NOFOLLOW 的效果一致。
type sourceRoot struct{ root *os.Root }

func (r *sourceRoot) Close() error { return r.root.Close() }

// id 返回来源根目录的卷序列号／文件索引（GetFileInformationByHandle），作为来源标识的一部分。
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
	// 中间各段必须是目录：os.Root 打开时已经按目录解析，这里只核对最后一段的类型由调用方完成。
	if strings.Contains(name, "\\") {
		f.Close()
		return nil, fail("unsupported", "source_reference")
	}
	_ = directory
	return f, nil
}
