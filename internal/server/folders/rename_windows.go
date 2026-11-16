//go:build windows

package folders

import (
	"os"
	"path/filepath"
)

// renameAt: Windows has no renameat, so the source and target are built from the paths of the two open directories and
// renamed (MoveFileEx). Both directories were opened and verified through os.Root or OpenNoFollow; if the last segment
// is a symlink, MoveFileEx moves the link itself, not its target.
func renameAt(srcParent *os.File, srcName string, dstParent *os.File, dstName string) error {
	return os.Rename(filepath.Join(srcParent.Name(), srcName), filepath.Join(dstParent.Name(), dstName))
}
