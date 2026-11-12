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

// scopeRoot 是经 os.Root 打开的目录：Windows 上 os.Root 以 O_NOFOLLOW_ANY 打开，路径中任何一段是符号链接或 junction 都失败。
// 根目录自身的路径按普通方式解析后，再逐段核对没有 reparse point。
type scopeRoot struct {
	path string
	root *os.Root
}

func (r *scopeRoot) Close() error { return r.root.Close() }

// private 报告目录是否只有当前用户能访问（受保护的 DACL 只含当前用户）。
func (r *scopeRoot) private() bool {
	f, e := r.root.Open(".")
	if e != nil {
		return false
	}
	defer f.Close()
	return privatefs.IsPrivateFile(f)
}

// openScopeRoot 从卷根开始逐级经 os.Root 打开 root 的每一段（每级都拒绝 reparse point），最后核对目录属于当前用户。
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

// openInputAt 在 root 之下打开 rel（os.Root 拒绝路径中任何符号链接），必须是普通文件。
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

// privateDescriptor 核对已打开的启动描述文件：属于当前用户、只有当前用户能访问、只有一个硬链接、大小在 (0, limit]。
func privateDescriptor(f *os.File, limit int64) bool {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) != nil {
		return false
	}
	size := int64(info.FileSizeHigh)<<32 | int64(info.FileSizeLow)
	return privatefs.OwnedByCurrentUser(f) && privatefs.IsPrivateFile(f) && info.NumberOfLinks == 1 && size > 0 && size <= limit
}
