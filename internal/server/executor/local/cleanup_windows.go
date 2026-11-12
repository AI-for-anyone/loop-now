//go:build windows

package local

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
)

// releaseHome is anchored at the configured work root. Windows has no openat:
// every directory on the way is opened through os.Root (which refuses symlinks
// and junctions anywhere in the path) and its owner is checked on the opened
// handle before anything beneath it is removed. Workspace and sessions are
// never traversed.
func releaseHome(workRoot string, id uuid.UUID) error {
	if !filepath.IsAbs(workRoot) || filepath.Clean(workRoot) != workRoot || workRoot == string(filepath.Separator) || id == uuid.Nil {
		return errors.New("invalid execution cleanup root")
	}
	root, err := os.OpenRoot(workRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open execution cleanup root: %w", err)
	}
	defer root.Close()
	if err = checkDirectoryOwner(root); err != nil {
		return err
	}
	agents, err := ownedDirectoryAt(root, "agents")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer agents.Close()
	runtime, err := ownedDirectoryAt(agents, id.String())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err = runtime.Remove("pi.pid"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove execution pid file: %w", err)
	}
	return removeOwnedDirectoryAt(runtime, "home")
}

func checkDirectoryOwner(dir *os.Root) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || !privatefs.OwnedByCurrentUser(f) {
		return errors.New("execution cleanup directory owner mismatch")
	}
	return nil
}

func ownedDirectoryAt(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("open execution cleanup directory: not a directory")
	}
	dir, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("open execution cleanup directory: %w", err)
	}
	if err = checkDirectoryOwner(dir); err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

func removeOwnedDirectoryAt(parent *os.Root, name string) error {
	dir, err := ownedDirectoryAt(parent, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.Open(".")
	if err != nil {
		return err
	}
	list, err := entries.ReadDir(-1)
	entries.Close()
	if err != nil {
		return err
	}
	for _, entry := range list {
		info, err := dir.Lstat(entry.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
			if err = removeOwnedDirectoryAt(dir, entry.Name()); err != nil {
				return err
			}
			continue
		}
		if !entryOwned(dir, entry.Name()) {
			return errors.New("execution cleanup entry owner mismatch")
		}
		if err = dir.Remove(entry.Name()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			// Go's module cache marks files read-only; clear the attribute through the root and retry once.
			if cerr := dir.Chmod(entry.Name(), 0o600); cerr == nil {
				err = dir.Remove(entry.Name())
			}
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove execution cleanup entry: %w", err)
			}
		}
	}
	// The handle in dir keeps the original directory; the final Remove goes through parent by name,
	// and os.Root refuses to remove a replacement that is a symlink or junction.
	dir.Close()
	if err = parent.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// entryOwned 核对 parent 下 name（文件或符号链接本身）属于当前用户。
func entryOwned(parent *os.Root, name string) bool {
	f, err := parent.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		// 符号链接经 os.Root 打不开（O_NOFOLLOW_ANY）：按路径核对所有者。
		info, lerr := parent.Lstat(name)
		return lerr == nil && privatefs.OwnedByCurrentUserPath(filepath.Join(parent.Name(), name), info)
	}
	defer f.Close()
	return privatefs.OwnedByCurrentUser(f)
}
