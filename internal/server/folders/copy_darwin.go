package folders

import (
	"errors"

	"golang.org/x/sys/unix"
)

// clonefile can be replaced in tests to cover the fallback path to a plain copy.
var clonefile = unix.Clonefile

// cloneTree clones a directory recursively with APFS clonefile (copy-on-write); returns errCloneUnsupported when that
// is not supported.
func cloneTree(src, dst string) error {
	err := clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
		return errCloneUnsupported
	}
	return err
}
