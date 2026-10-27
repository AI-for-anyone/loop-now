// Package folders 实现人可以直接操作的文件夹：Agent 文件夹（它的工作目录，也是它自己的上下文）
// 与项目、全局上下文文件夹；以及从 Git 一次性拉取、任务开始时复制 Agent 文件夹、退役文件夹来源的一次性回放。
//
// 所有路径操作都经 os.Root，以文件夹根为锚：相对路径不能含 .. 或以 / 开头，指向文件夹之外的软链接不会被跟随。
// 没有锁，人和 Agent 同时修改时以最后一次写入为准；写入先写同目录的临时文件再改名，读取方不会看到写了一半的文件。
package folders

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
)

// Folder 是一个文件夹及其回收区。回收区与文件夹在同一卷上、位于文件夹之外，删除时先改名进去再在后台删除。
type Folder struct {
	Dir   string // 文件夹的绝对路径
	Trash string // 回收区的绝对路径
}

// Entry 是文件夹中的一项。
type Entry struct {
	Name          string
	Path          string
	Type          string // file / dir / symlink / other
	Size          *int64
	ModifiedAt    time.Time
	SymlinkTarget *string
}

// Page 是列出的一层。
type Page struct {
	Path       string
	Entries    []Entry
	NextCursor *string
}

const (
	// DefaultPageSize 与 MaxPageSize 是列表每页条数的默认值与上限。
	DefaultPageSize = 500
	MaxPageSize     = 2000
	tempPrefix      = ".loop-now-tmp-"
)

// cleanPath 校验并返回文件夹内的相对路径；allowRoot 时空串表示文件夹根（"."）。
func cleanPath(field, p string, allowRoot bool) (string, error) {
	if p == "" && allowRoot {
		return ".", nil
	}
	clean, err := content.CleanRelPath(p)
	if err != nil {
		return "", apperr.Invalid(field, err.Error())
	}
	return clean, nil
}

func (f Folder) open() (*os.Root, error) {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(f.Dir)
}

func notFound() error { return apperr.NotFound("文件或目录") }

// mapErr 把文件系统错误转为接口错误：不存在为 404，越界（经由软链接或 ..）为 422。
func mapErr(field string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return notFound()
	case errors.Is(err, unix.ENOTDIR):
		return apperr.Invalid(field, "路径中的某一段不是目录")
	case strings.Contains(err.Error(), "path escapes from parent"):
		return apperr.Invalid(field, "路径经由软链接指向了文件夹之外")
	}
	return err
}

func entryOf(root *os.Root, rel string, info fs.FileInfo) Entry {
	e := Entry{Name: path.Base(rel), Path: rel, ModifiedAt: info.ModTime().UTC()}
	if rel == "." {
		e.Name, e.Path = "", ""
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		e.Type = "symlink"
		if target, err := root.Readlink(rel); err == nil {
			e.SymlinkTarget = &target
		}
	case mode.IsDir():
		e.Type = "dir"
	case mode.IsRegular():
		e.Type = "file"
		size := info.Size()
		e.Size = &size
	default:
		e.Type = "other"
	}
	return e
}

func (f Folder) stat(root *os.Root, field, rel string) (Entry, error) {
	info, err := root.Lstat(rel)
	if err != nil {
		return Entry{}, mapErr(field, err)
	}
	return entryOf(root, rel, info), nil
}

// sortKey 让目录排在前面，同类按名称排序；游标就是上一页最后一项的键。
func sortKey(isDir bool, name string) string {
	if isDir {
		return "0/" + name
	}
	return "1/" + name
}

// List 列出 dir 这一层（不递归、不统计目录大小）。
func (f Folder) List(dir, cursor string, limit int) (Page, error) {
	rel, err := cleanPath("path", dir, true)
	if err != nil {
		return Page{}, err
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	after := ""
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return Page{}, apperr.Invalid("cursor", "游标无效")
		}
		after = string(b)
	}
	root, err := f.open()
	if err != nil {
		return Page{}, err
	}
	defer root.Close()
	d, err := root.Open(rel)
	if err != nil {
		return Page{}, mapErr("path", err)
	}
	defer d.Close()
	if info, err := d.Stat(); err != nil {
		return Page{}, err
	} else if !info.IsDir() {
		return Page{}, apperr.Invalid("path", "不是目录")
	}
	list, err := d.ReadDir(-1)
	if err != nil {
		return Page{}, err
	}
	type item struct{ key, name string }
	items := make([]item, 0, len(list))
	for _, de := range list {
		if strings.HasPrefix(de.Name(), tempPrefix) {
			continue // 正在写入的临时文件
		}
		k := sortKey(de.IsDir(), de.Name())
		if k > after {
			items = append(items, item{k, de.Name()})
		}
	}
	slices.SortFunc(items, func(a, b item) int { return strings.Compare(a.key, b.key) })
	page := Page{Path: rel, Entries: []Entry{}}
	if rel == "." {
		page.Path = ""
	}
	if len(items) > limit {
		next := base64.RawURLEncoding.EncodeToString([]byte(items[limit-1].key))
		page.NextCursor = &next
		items = items[:limit]
	}
	for _, it := range items {
		p := path.Join(rel, it.name)
		info, err := root.Lstat(p)
		if err != nil {
			continue // 列出后被删除
		}
		page.Entries = append(page.Entries, entryOf(root, p, info))
	}
	return page, nil
}

// Open 打开一个文件用于下载；目录或特殊文件返回 422。调用方关闭返回的文件。
func (f Folder) Open(file string) (*os.File, fs.FileInfo, error) {
	rel, err := cleanPath("path", file, false)
	if err != nil {
		return nil, nil, err
	}
	root, err := f.open()
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	h, err := root.Open(rel)
	if err != nil {
		return nil, nil, mapErr("path", err)
	}
	info, err := h.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = apperr.Invalid("path", "只能下载文件")
	}
	if err != nil {
		h.Close()
		return nil, nil, err
	}
	return h, info, nil
}

// Write 用 body 的内容新建或覆盖一个文件：流式写入同目录的临时文件后改名；缺少的上级目录自动创建。
// create 时目标已存在返回 path_conflict；目标是目录或软链接返回 422；超过 max 字节返回 413。覆盖时保留原文件的权限。
func (f Folder) Write(file string, body io.Reader, max int64, create bool) (Entry, error) {
	rel, err := cleanPath("path", file, false)
	if err != nil {
		return Entry{}, err
	}
	root, err := f.open()
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	mode := fs.FileMode(0o644)
	if info, err := root.Lstat(rel); err == nil {
		switch {
		case create:
			return Entry{}, apperr.Conflict(apperr.CodePathConflict, "已存在同名文件或目录")
		case info.Mode()&fs.ModeSymlink != 0:
			return Entry{}, apperr.Invalid("path", "目标是软链接，不能写入")
		case !info.Mode().IsRegular():
			return Entry{}, apperr.Invalid("path", "目标不是普通文件")
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, mapErr("path", err)
	}
	dir := path.Dir(rel)
	if err := mkdirAll(root, dir); err != nil {
		return Entry{}, err
	}
	tmp := path.Join(dir, tempPrefix+randHex())
	out, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return Entry{}, mapErr("path", err)
	}
	n, err := io.Copy(out, io.LimitReader(body, max+1))
	if err == nil && n > max {
		err = apperr.TooLarge("文件超过上传上限")
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Chmod(tmp, mode)
	}
	if err == nil {
		err = root.Rename(tmp, rel)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return Entry{}, mapErr("path", err)
	}
	return f.stat(root, "path", rel)
}

func mkdirAll(root *os.Root, dir string) error {
	if dir == "." {
		return nil
	}
	if err := root.MkdirAll(dir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) || errors.Is(err, unix.ENOTDIR) {
			return apperr.Conflict(apperr.CodePathConflict, "上级路径中有同名文件")
		}
		return mapErr("path", err)
	}
	return nil
}

// Mkdir 新建目录（含缺少的上级目录）；已是目录时原样返回，已有同名文件时返回 path_conflict。
func (f Folder) Mkdir(dir string) (Entry, error) {
	rel, err := cleanPath("path", dir, false)
	if err != nil {
		return Entry{}, err
	}
	root, err := f.open()
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if info, err := root.Lstat(rel); err == nil && !info.IsDir() {
		return Entry{}, apperr.Conflict(apperr.CodePathConflict, "已存在同名文件")
	}
	if err := mkdirAll(root, rel); err != nil {
		return Entry{}, err
	}
	return f.stat(root, "path", rel)
}

// Move 在文件夹内改名或移动；目标已存在返回 path_conflict，不覆盖；不能移到自身之下。缺少的上级目录自动创建。
func (f Folder) Move(from, to string) (Entry, error) {
	src, err := cleanPath("from", from, false)
	if err != nil {
		return Entry{}, err
	}
	dst, err := cleanPath("to", to, false)
	if err != nil {
		return Entry{}, err
	}
	if dst == src || strings.HasPrefix(dst, src+"/") {
		return Entry{}, apperr.Invalid("to", "不能移到自身或自身之下")
	}
	root, err := f.open()
	if err != nil {
		return Entry{}, err
	}
	defer root.Close()
	if _, err := root.Lstat(src); err != nil {
		return Entry{}, mapErr("from", err)
	}
	if _, err := root.Lstat(dst); err == nil {
		return Entry{}, apperr.Conflict(apperr.CodePathConflict, "目标已存在")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, mapErr("to", err)
	}
	if err := mkdirAll(root, path.Dir(dst)); err != nil {
		return Entry{}, err
	}
	if err := root.Rename(src, dst); err != nil {
		return Entry{}, mapErr("to", err)
	}
	return f.stat(root, "to", dst)
}

// Delete 删除一个文件或目录（软链接只删链接本身）：先改名进回收区（立即返回），再在后台删除；不能删除文件夹根。
// 回收区与文件夹不在同一卷上时退回为直接删除。
func (f Folder) Delete(target string) error {
	rel, err := cleanPath("path", target, false)
	if err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(rel); err != nil {
		return mapErr("path", err)
	}
	// 上级目录经 os.Root 打开（中间的软链接只能指向文件夹内部），再按最后一段名称移动，不跟随最后一段。
	parent, err := root.Open(path.Dir(rel))
	if err != nil {
		return mapErr("path", err)
	}
	defer parent.Close()
	if err := os.MkdirAll(f.Trash, 0o700); err != nil {
		return err
	}
	trashFd, err := unix.Open(f.Trash, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(trashFd)
	name := randHex()
	err = unix.Renameat(int(parent.Fd()), path.Base(rel), trashFd, name)
	if errors.Is(err, unix.EXDEV) {
		return mapErr("path", root.RemoveAll(rel))
	}
	if err != nil {
		return mapErr("path", err)
	}
	trashed := filepath.Join(f.Trash, name)
	go func() { _ = os.RemoveAll(trashed) }()
	return nil
}

// EmptyTrash 删除回收区中残留的条目（上次运行未删完的）。
func (f Folder) EmptyTrash() error {
	err := os.RemoveAll(f.Trash)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func randHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
