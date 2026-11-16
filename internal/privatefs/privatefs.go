// Package privatefs turns two kinds of file system constraints, "only the current user can access it" and "do not
// follow symbolic links", into operations that behave the same on both platforms.
// On Unix they are the 0700/0600 permission bits, a uid check and O_NOFOLLOW; on Windows they are a protected DACL
// with a single ACE for the current user, an owner SID check and refusing reparse points (symbolic links,
// junctions). Callers only use this package's functions and never look at permission bits directly.
package privatefs

import (
	"io/fs"
	"os"
)

// NoFollow is the flag passed to os.OpenFile / os.Root.OpenFile: opening fails when the last path element is a
// symbolic link.
// On Unix it is O_NOFOLLOW; on Windows it is 0 - os.Root on Windows already refuses symbolic links anywhere in the
// path, and opens that do not go through os.Root should use OpenNoFollow.
const NoFollow = noFollow

// NonBlock is O_NONBLOCK (opening a FIFO does not block); Windows has no FIFOs, so there it is 0.
const NonBlock = nonBlock

// OpenNoFollow opens path and fails when the last element is a symbolic link.
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return openNoFollow(path, flag, perm)
}

// MakePrivate restricts path so that only the current user can access it: on Unix, chmod 0700 for a directory and
// 0600 for a file; on Windows, a protected DACL with a single allow ACE for the current user (the ACE on a directory
// is inheritable, so files created in it later are also accessible only to the current user).
func MakePrivate(path string) error { return makePrivate(path) }

// IsPrivate reports whether only the owner of path can access it (it does not check who the owner is; see
// OwnedByCurrentUser).
// info is the Lstat result the caller already has; on Unix only its permission bits are used, on Windows the DACL
// is read by path.
func IsPrivate(path string, info fs.FileInfo) bool { return isPrivate(path, info) }

// IsPrivateFile does the IsPrivate check on an open file (by descriptor / handle, without resolving the path
// again).
func IsPrivateFile(f *os.File) bool { return isPrivateFile(f) }

// WritableByOthersFile reports whether an open file can be written by anyone other than its owner: on Unix it looks
// at the group / other write bits; on Windows at whether the DACL grants access to anyone else.
func WritableByOthersFile(f *os.File) bool { return writableByOthersFile(f) }

// WorldWritableFile reports whether an open file is writable by everyone: on Unix it looks at the other-users write
// bit; on Windows it is the same as WritableByOthersFile.
func WorldWritableFile(f *os.File) bool { return worldWritableFile(f) }

// OwnedByCurrentUser reports whether an open file belongs to the current user: on Unix it compares the uid, on
// Windows the owner SID.
func OwnedByCurrentUser(f *os.File) bool { return ownedByCurrentUser(f) }

// OwnedByCurrentUserPath does the OwnedByCurrentUser check by path (without following symbolic links).
func OwnedByCurrentUserPath(path string, info fs.FileInfo) bool {
	return ownedByCurrentUserPath(path, info)
}

// IsExecutable reports whether path is executable: on Unix it looks at the execute bits; on Windows at the
// extension (.exe/.cmd/.bat/.com).
func IsExecutable(path string, info fs.FileInfo) bool { return isExecutable(path, info) }

// IsNotDir reports whether err means "an element of the path is not a directory" (Unix ENOTDIR; Windows
// ERROR_PATH_NOT_FOUND / ERROR_DIRECTORY).
func IsNotDir(err error) bool { return isNotDir(err) }

// IsCrossDevice reports whether err means "the rename crossed file systems" (Unix EXDEV; Windows
// ERROR_NOT_SAME_DEVICE).
func IsCrossDevice(err error) bool { return isCrossDevice(err) }
