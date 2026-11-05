// Package privatefs 把"只有当前用户能访问"和"不跟随符号链接"这两类文件系统约束做成两个平台一致的操作。
// Unix 上它们是 0700/0600 权限位、uid 核对与 O_NOFOLLOW；Windows 上是只含当前用户一条 ACE 的受保护 DACL、
// 所有者 SID 核对与拒绝 reparse point（符号链接、junction）。调用方只用本包的函数，不直接看权限位。
package privatefs

import (
	"io/fs"
	"os"
)

// NoFollow 是传给 os.OpenFile／os.Root.OpenFile 的标志：最后一段路径是符号链接时拒绝打开。
// Unix 上是 O_NOFOLLOW；Windows 上为 0——os.Root 在 Windows 上本来就拒绝路径中任何位置的符号链接，
// 而不经 os.Root 的打开请用 OpenNoFollow。
const NoFollow = noFollow

// NonBlock 是 O_NONBLOCK（打开 FIFO 不阻塞）；Windows 上没有 FIFO，为 0。
const NonBlock = nonBlock

// OpenNoFollow 打开 path，最后一段是符号链接时失败。
func OpenNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return openNoFollow(path, flag, perm)
}

// MakePrivate 把 path 限制为只有当前用户能访问：Unix 上目录 chmod 0700、文件 0600；
// Windows 上设置只含当前用户一条允许 ACE 的受保护 DACL（目录上的 ACE 可继承，之后在其中新建的文件也只有当前用户能访问）。
func MakePrivate(path string) error { return makePrivate(path) }

// IsPrivate 报告 path 是否只有它的所有者能访问（不核对所有者是谁，见 OwnedByCurrentUser）。
// info 是调用方已经取得的 Lstat 结果，Unix 只看它的权限位；Windows 按 path 读 DACL。
func IsPrivate(path string, info fs.FileInfo) bool { return isPrivate(path, info) }

// IsPrivateFile 对已打开的文件做 IsPrivate 的核对（按描述符／句柄，不再按路径解析）。
func IsPrivateFile(f *os.File) bool { return isPrivateFile(f) }

// WritableByOthersFile 报告已打开的文件是否能被所有者之外的人写：Unix 看组／其他的写位；Windows 看 DACL 是否授予了别人。
func WritableByOthersFile(f *os.File) bool { return writableByOthersFile(f) }

// WorldWritableFile 报告已打开的文件是否人人可写：Unix 看其他用户的写位；Windows 与 WritableByOthersFile 相同。
func WorldWritableFile(f *os.File) bool { return worldWritableFile(f) }

// OwnedByCurrentUser 报告已打开的文件是否属于当前用户：Unix 比 uid，Windows 比所有者 SID。
func OwnedByCurrentUser(f *os.File) bool { return ownedByCurrentUser(f) }

// OwnedByCurrentUserPath 按路径（不跟随符号链接）做 OwnedByCurrentUser 的核对。
func OwnedByCurrentUserPath(path string, info fs.FileInfo) bool {
	return ownedByCurrentUserPath(path, info)
}

// IsExecutable 报告 path 是否可执行：Unix 看执行位；Windows 看扩展名（.exe/.cmd/.bat/.com）。
func IsExecutable(path string, info fs.FileInfo) bool { return isExecutable(path, info) }

// IsNotDir 报告 err 是否表示"路径中的某一段不是目录"（Unix ENOTDIR；Windows ERROR_PATH_NOT_FOUND／ERROR_DIRECTORY）。
func IsNotDir(err error) bool { return isNotDir(err) }

// IsCrossDevice 报告 err 是否表示"改名跨了文件系统"（Unix EXDEV；Windows ERROR_NOT_SAME_DEVICE）。
func IsCrossDevice(err error) bool { return isCrossDevice(err) }
