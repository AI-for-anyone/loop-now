// Package copyworkspace makes, inspects and removes the copy of a folder that a copy-mode task workspace works in.
// The original folder is only read; every write is inside the copy or beside it under the task's own folder.
package copyworkspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"loop-now/internal/server/folders"
)

// ListingName is the file beside the copy that records the copy as made; it is never inside the copy.
const ListingName = ".loop-now-start-listing.json"

// Entry is one file of the copy as made: a regular file (Size, ModTime) or a symbolic link (Link).
type Entry struct {
	Size    int64  `json:"size,omitempty"`
	ModTime int64  `json:"modTime,omitempty"`
	Link    string `json:"link,omitempty"`
}

// Listing is the start listing: every file of the copy right after it was made, by slash-separated relative path.
type Listing struct {
	Files   int              `json:"files"`
	Bytes   int64            `json:"bytes"`
	Left    int              `json:"left"`
	Entries map[string]Entry `json:"entries"`
}

// ListingPath is where the listing of the copy at path is kept: beside it, in the task's own folder.
func ListingPath(path string) string { return filepath.Join(filepath.Dir(path), ListingName) }

// Make copies folder to path and records the start listing; it returns the listing. Running it again for a path
// whose listing exists adopts that copy (a restart after it was made); otherwise a partial copy left by an earlier
// attempt is removed and the copy is redone. The copy keeps regular files, folders and the symbolic links that stay
// inside it; links that point outside it, git links to another repository's data (a .git file), sockets, pipes and
// devices are left out, so nothing a step does in the copy reaches the original or anything else outside it.
func Make(folder, path string) (Listing, error) {
	if l, err := ReadListing(path); err == nil {
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			return l, nil
		}
	}
	if err := removeTree(path); err != nil {
		return Listing{}, err
	}
	_ = os.Remove(ListingPath(path))
	if matches, err := filepath.Glob(path + ".copying-*"); err == nil {
		for _, m := range matches {
			_ = removeTree(m)
		}
	}
	src, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return Listing{}, fmt.Errorf("cannot read the folder: %w", err)
	}
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return Listing{}, errors.New("the folder is gone or is no longer a folder")
	}
	if _, err := folders.CopyTree(src, path); err != nil {
		return Listing{}, explain(src, err)
	}
	left, err := trim(path)
	if err != nil {
		_ = removeTree(path)
		return Listing{}, err
	}
	l, err := list(path)
	if err != nil {
		_ = removeTree(path)
		return Listing{}, err
	}
	l.Left = left
	data, _ := json.Marshal(l)
	tmp := ListingPath(path) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return Listing{}, err
	}
	if err := os.Rename(tmp, ListingPath(path)); err != nil {
		return Listing{}, err
	}
	return l, nil
}

// ReadListing reads the start listing of the copy at path.
func ReadListing(path string) (Listing, error) {
	data, err := os.ReadFile(ListingPath(path))
	if err != nil {
		return Listing{}, err
	}
	var l Listing
	if err := json.Unmarshal(data, &l); err != nil {
		return Listing{}, err
	}
	return l, nil
}

// Changed counts the files of the copy at path that were added or changed since the start listing (a regular file
// with another size or modification time, or a link with another target); deleted files do not count, since the
// original still has them. missing is true when the copy is gone.
func Changed(path string) (changed int, missing bool, err error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return 0, true, nil
	}
	start, err := ReadListing(path)
	if err != nil {
		return 0, false, fmt.Errorf("cannot read the copy's start listing: %w", err)
	}
	now, err := list(path)
	if err != nil {
		return 0, false, err
	}
	for rel, e := range now.Entries {
		if s, ok := start.Entries[rel]; !ok || s != e {
			changed++
		}
	}
	return changed, false, nil
}

// Remove deletes the copy at path and its listing, then the task's folder holding them when it is empty. A missing
// copy is not an error.
func Remove(path string) error {
	if err := removeTree(path); err != nil {
		return err
	}
	if err := os.Remove(ListingPath(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_ = os.Remove(filepath.Dir(path))
	return nil
}

// removeTree removes a tree even when some of its folders are read-only (a copy keeps the original's permissions).
// Symbolic links are removed, never followed.
func removeTree(path string) error {
	if err := os.RemoveAll(path); err == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		} else if err != nil && d != nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// trim removes from the copy what must not be in it and returns how many entries it left out.
func trim(root string) (int, error) {
	left := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		t := d.Type()
		switch {
		case t.IsDir():
			return nil
		case t&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) && inside(root, filepath.Join(filepath.Dir(p), target)) {
				return nil
			}
		case t.IsRegular():
			if d.Name() != ".git" {
				return nil
			}
		}
		left++
		return os.Remove(p)
	})
	return left, err
}

// list records every regular file and link of the copy.
func list(root string) (Listing, error) {
	l := Listing{Entries: map[string]Entry{}}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			l.Entries[rel] = Entry{Link: target}
			return nil
		}
		if !d.Type().IsRegular() {
			l.Entries[rel] = Entry{Link: "special:" + d.Type().String()}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		l.Entries[rel] = Entry{Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		l.Files++
		l.Bytes += info.Size()
		return nil
	})
	return l, err
}

// explain turns a failed copy into a reason a person can act on: for a permission error, the first entry of the
// folder that cannot be read.
func explain(src string, err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot copy the folder: %w", err)
	}
	var first string
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, werr error) error {
		if first != "" {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(src, p)
		if werr != nil {
			first = rel
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				first = rel
				return filepath.SkipAll
			}
			f.Close()
		}
		return nil
	})
	if first == "" {
		return fmt.Errorf("cannot copy the folder: %w", err)
	}
	return fmt.Errorf("cannot copy the folder: %s cannot be read (permission denied); make it readable or move it out of the folder", filepath.ToSlash(first))
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
