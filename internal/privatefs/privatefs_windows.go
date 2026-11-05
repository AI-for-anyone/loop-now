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
	errorDirectory     syscall.Errno = 267  // ERROR_DIRECTORY：路径中的一段不是目录
	errorNotSameDevice syscall.Errno = 0x11 // ERROR_NOT_SAME_DEVICE
)

// openNoFollow 经 os.Root 打开最后一段：os.Root 在 Windows 上以 O_NOFOLLOW_ANY 打开，最后一段是符号链接或 junction 时失败。
// 上级目录按普通路径解析，与 Unix 的 O_NOFOLLOW 只管最后一段一致。
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

// currentUserSID 返回本进程令牌的用户 SID（只取一次）。
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

// makePrivate 给 path 设置受保护的 DACL（不再继承上级的 ACE），只含当前用户一条允许全部访问的 ACE；
// 目录上的 ACE 标记为可继承，之后在目录里新建的文件与子目录自动只有当前用户能访问。
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

// onlyCurrentUser 报告安全描述符的 DACL 是否只把访问授予当前用户：没有 DACL（人人可访问）、
// 有授予别人的 ACE、或有本包不认识的 ACE 类型都算不是。拒绝类 ACE 只会减少访问，忽略。
// SYSTEM 与 BUILTIN\Administrators 的 ACE 不算"别人"：Windows 用户目录（含 %TEMP%、%LOCALAPPDATA%）天然带着这两条继承 ACE，
// 它们对应 Unix 上本来就绕过权限位的 root；OpenSSH for Windows 的"权限过宽"检查也是同样的口径。
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
