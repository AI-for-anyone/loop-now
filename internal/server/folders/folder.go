// Package folders implements the folders people can work with directly: Agent folders (an Agent's working directory,
// which is also its own context) and project and global context folders; plus one-time pulls from Git, copying the
// Agent folder when a task starts, and the one-time replay of retired folder sources.
//
// All path operations go through os.Root, anchored at the folder root: relative paths may not contain .. or start
// with a slash, and symlinks pointing outside the folder are not followed. There is no lock: when a person and an
// Agent change something at the same time, the last write wins; writes go to a temporary file in the same directory
// and are then renamed, so readers never see a half-written file.
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

	"loop-now/internal/privatefs"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
)

// Folder is a folder and its trash. The trash is on the same volume as the folder but outside it; a deletion first
// renames the item into the trash and then deletes it in the background.
type Folder struct {
	Dir   string // absolute path of the folder
	Trash string // absolute path of the trash
}

// Entry is one item in a folder.
type Entry struct {
	Name          string
	Path          string
	Type          string // file / dir / symlink / other
	Size          *int64
	ModifiedAt    time.Time
	SymlinkTarget *string
}

// Page is one listed level.
type Page struct {
	Path       string
	Entries    []Entry
	NextCursor *string
}

const (
	// DefaultPageSize and MaxPageSize are the default and the maximum number of entries per list page.
	DefaultPageSize = 500
	MaxPageSize     = 2000
	tempPrefix      = ".loop-now-tmp-"
)

// cleanPath validates and returns a relative path inside the folder; with allowRoot, an empty string means the folder
// root (".").
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

func notFound() error { return apperr.NotFound("File or directory") }

// mapErr turns file system errors into API errors: not found is 404, escaping the folder (through a symlink or ..) is
// 422.
func mapErr(field string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return notFound()
	case privatefs.IsNotDir(err):
		return apperr.Invalid(field, "A segment of the path is not a directory.")
	case strings.Contains(err.Error(), "path escapes from parent"):
		return apperr.Invalid(field, "The path leads outside the folder through a symlink.")
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

// sortKey puts directories first and sorts by name within each kind; the cursor is the key of the last item on the
// previous page.
func sortKey(isDir bool, name string) string {
	if isDir {
		return "0/" + name
	}
	return "1/" + name
}

// List lists the level dir (not recursive, no directory sizes).
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
			return Page{}, apperr.Invalid("cursor", "The cursor is invalid.")
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
		return Page{}, apperr.Invalid("path", "The path is not a directory.")
	}
	list, err := d.ReadDir(-1)
	if err != nil {
		return Page{}, err
	}
	type item struct{ key, name string }
	items := make([]item, 0, len(list))
	for _, de := range list {
		if strings.HasPrefix(de.Name(), tempPrefix) {
			continue // a temporary file being written
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
			continue // deleted after listing
		}
		page.Entries = append(page.Entries, entryOf(root, p, info))
	}
	return page, nil
}

// Open opens a file for download; a directory or special file returns 422. The caller closes the returned file.
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
		err = apperr.Invalid("path", "Only files can be downloaded.")
	}
	if err != nil {
		h.Close()
		return nil, nil, err
	}
	return h, info, nil
}

// Write creates or overwrites a file with the contents of body: it streams into a temporary file in the same directory
// and then renames it; missing parent directories are created automatically. With create, an existing target returns
// path_conflict; a target that is a directory or a symlink returns 422; more than max bytes returns 413. Overwriting
// keeps the original file's permissions.
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
			return Entry{}, apperr.Conflict(apperr.CodePathConflict, "A file or directory with the same name already exists.")
		case info.Mode()&fs.ModeSymlink != 0:
			return Entry{}, apperr.Invalid("path", "The target is a symlink and cannot be written.")
		case !info.Mode().IsRegular():
			return Entry{}, apperr.Invalid("path", "The target is not a regular file.")
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
		err = apperr.TooLarge("The file exceeds the upload size limit.")
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
		if errors.Is(err, fs.ErrExist) || privatefs.IsNotDir(err) {
			return apperr.Conflict(apperr.CodePathConflict, "A file with the same name exists in the parent path.")
		}
		return mapErr("path", err)
	}
	return nil
}

// Mkdir creates a directory (including missing parents); returns it as is when it is already a directory, and returns
// path_conflict when a file with the same name exists.
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
		return Entry{}, apperr.Conflict(apperr.CodePathConflict, "A file with the same name already exists.")
	}
	if err := mkdirAll(root, rel); err != nil {
		return Entry{}, err
	}
	return f.stat(root, "path", rel)
}

// Move renames or moves within the folder; an existing target returns path_conflict and is not overwritten; an item
// cannot be moved under itself. Missing parent directories are created automatically.
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
		return Entry{}, apperr.Invalid("to", "An item cannot be moved onto itself or under itself.")
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
		return Entry{}, apperr.Conflict(apperr.CodePathConflict, "The target already exists.")
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

// Delete deletes a file or directory (for a symlink, only the link itself): it is first renamed into the trash
// (returning immediately) and then deleted in the background; the folder root cannot be deleted. When the trash and the
// folder are not on the same volume, it falls back to deleting directly.
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
	// The parent directory is opened through os.Root (symlinks along the way can only point inside the folder), then
	// the item is moved by its last name segment, without following the last segment.
	parent, err := root.Open(path.Dir(rel))
	if err != nil {
		return mapErr("path", err)
	}
	defer parent.Close()
	if err := os.MkdirAll(f.Trash, 0o700); err != nil {
		return err
	}
	trash, err := privatefs.OpenNoFollow(f.Trash, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer trash.Close()
	name := randHex()
	err = renameAt(parent, path.Base(rel), trash, name)
	if privatefs.IsCrossDevice(err) {
		return mapErr("path", root.RemoveAll(rel))
	}
	if err != nil {
		return mapErr("path", err)
	}
	trashed := filepath.Join(f.Trash, name)
	go func() { _ = os.RemoveAll(trashed) }()
	return nil
}

// EmptyTrash deletes entries left in the trash (those the previous run did not finish deleting).
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
