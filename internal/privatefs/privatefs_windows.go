//go:build windows

package privatefs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	noFollow = 0
	nonBlock = 0
)

const (
	errorDirectory     syscall.Errno = 267  // ERROR_DIRECTORY: an element of the path is not a directory
	errorNotSameDevice syscall.Errno = 0x11 // ERROR_NOT_SAME_DEVICE
)

// openNoFollow opens the last element through os.Root: on Windows os.Root opens with O_NOFOLLOW_ANY, so it fails
// when the last element is a symbolic link or a junction.
// The parent directories are resolved as an ordinary path, matching Unix O_NOFOLLOW, which only covers the last
// element.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	dir, base := filepath.Split(filepath.Clean(path))
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenFile(base, flag, perm)
}

var currentUser struct {
	once sync.Once
	sid  *windows.SID
	err  error
}

// currentUserSID returns the user SID of this process's token (looked up only once).
func currentUserSID() (*windows.SID, error) {
	currentUser.once.Do(func() {
		token, err := windows.OpenCurrentProcessToken()
		if err != nil {
			currentUser.err = err
			return
		}
		defer token.Close()
		user, err := token.GetTokenUser()
		if err != nil {
			currentUser.err = err
			return
		}
		currentUser.sid, currentUser.err = user.User.Sid.Copy()
	})
	return currentUser.sid, currentUser.err
}

// makePrivate gives path a protected DACL (no longer inheriting the parent's ACEs) containing a single ACE that
// allows the current user full access; on a directory the ACE is marked inheritable, so files and subdirectories
// created in it later are automatically accessible only to the current user.
func makePrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(sid)},
	}
	if info.IsDir() {
		entry.Inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{entry}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// onlyCurrentUser reports whether the DACL of the security descriptor grants access only to the current user. No
// DACL (everyone has access), an ACE granting access to someone else, or an ACE type this package does not know all
// count as no. Deny ACEs can only reduce access and are ignored.
// ACEs for SYSTEM and BUILTIN\Administrators do not count as "someone else": Windows user directories (including
// %TEMP% and %LOCALAPPDATA%) naturally carry these two inherited ACEs, and they correspond to root on Unix, which
// bypasses the permission bits anyway; OpenSSH for Windows uses the same rule in its "permissions too open" check.
func onlyCurrentUser(sd *windows.SECURITY_DESCRIPTOR) bool {
	me, err := currentUserSID()
	if err != nil {
		return false
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	adminsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.Equals(me) && !sid.Equals(systemSID) && !sid.Equals(adminsSID) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func isPrivate(path string, _ fs.FileInfo) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	return err == nil && onlyCurrentUser(sd)
}

func isPrivateFile(f *os.File) bool {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	return err == nil && onlyCurrentUser(sd)
}

func writableByOthersFile(f *os.File) bool { return !isPrivateFile(f) }
func worldWritableFile(f *os.File) bool    { return !isPrivateFile(f) }

func ownerIsCurrentUser(sd *windows.SECURITY_DESCRIPTOR) bool {
	me, err := currentUserSID()
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	return err == nil && owner != nil && owner.Equals(me)
}

func ownedByCurrentUser(f *os.File) bool {
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	return err == nil && ownerIsCurrentUser(sd)
}

func ownedByCurrentUserPath(path string, _ fs.FileInfo) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	return err == nil && ownerIsCurrentUser(sd)
}

func isExecutable(path string, info fs.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".cmd", ".bat", ".com":
		return true
	}
	return false
}

func isNotDir(err error) bool {
	return errors.Is(err, syscall.ENOTDIR) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, errorDirectory)
}

func isCrossDevice(err error) bool { return errors.Is(err, errorNotSameDevice) }
