//go:build unix

package folders

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameAt renames through two open directory descriptors (renameat) without re-resolving any path: the last segment of
// neither source nor target follows symlinks.
func renameAt(srcParent *os.File, srcName string, dstParent *os.File, dstName string) error {
	return unix.Renameat(int(srcParent.Fd()), srcName, int(dstParent.Fd()), dstName)
}
