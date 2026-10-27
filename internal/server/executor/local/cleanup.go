package local

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// releaseHome is anchored at the configured work root. No managed path is
// resolved through a symlink; workspace and sessions are never traversed.
func releaseHome(workRoot string, id uuid.UUID) error {
	if !filepath.IsAbs(workRoot) || filepath.Clean(workRoot) != workRoot || workRoot == string(filepath.Separator) || id == uuid.Nil {
		return errors.New("invalid execution cleanup root")
	}
	uid := uint32(os.Getuid())
	fd, err := unix.Open(workRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open execution cleanup root: %w", err)
	}
	root := os.NewFile(uintptr(fd), "cleanup-root")
	defer root.Close()
	if err = checkDirectoryOwner(fd, uid); err != nil {
		return err
	}
	agents, err := ownedDirectoryAt(fd, "agents", uid)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer agents.Close()
	runtime, err := ownedDirectoryAt(int(agents.Fd()), id.String(), uid)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err = unix.Unlinkat(int(runtime.Fd()), "pi.pid", 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("remove execution pid file: %w", err)
	}
	return removeOwnedDirectoryAt(int(runtime.Fd()), "home", uid)
}

func checkDirectoryOwner(fd int, uid uint32) error {
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Uid != uid {
		return errors.New("execution cleanup directory owner mismatch")
	}
	return nil
}

func ownedDirectoryAt(parent int, name string, uid uint32) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open execution cleanup directory: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	if err = checkDirectoryOwner(fd, uid); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func removeOwnedDirectoryAt(parent int, name string, uid uint32) error {
	f, err := ownedDirectoryAt(parent, name, uid)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	fd := int(f.Fd())
	var opened unix.Stat_t
	if err = unix.Fstat(fd, &opened); err != nil {
		return err
	}
	// Only directories are chmod'd, through their verified descriptor. Go's
	// module-cache files remain untouched until their directory entry is removed.
	if opened.Mode&0200 == 0 {
		if err = unix.Fchmod(fd, uint32(opened.Mode&07777)|0200); err != nil {
			return fmt.Errorf("make execution cleanup directory writable: %w", err)
		}
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var info unix.Stat_t
		if err = unix.Fstatat(fd, entry.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
			continue
		} else if err != nil {
			return err
		}
		if info.Uid != uid {
			return errors.New("execution cleanup entry owner mismatch")
		}
		if info.Mode&unix.S_IFMT == unix.S_IFDIR {
			if err = removeOwnedDirectoryAt(fd, entry.Name(), uid); err != nil {
				return err
			}
		} else if err = unix.Unlinkat(fd, entry.Name(), 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("remove execution cleanup entry: %w", err)
		}
	}
	// Do not remove a replacement inserted under the original directory name.
	var current unix.Stat_t
	if err = unix.Fstatat(parent, name, &current, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return nil
	} else if err != nil {
		return err
	}
	if current.Ino != opened.Ino || current.Dev != opened.Dev || current.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("execution cleanup directory changed")
	}
	return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}
