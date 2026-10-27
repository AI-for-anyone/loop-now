package folders

import (
	"errors"

	"golang.org/x/sys/unix"
)

// clonefile 可在测试中替换，用来覆盖退回普通复制的路径。
var clonefile = unix.Clonefile

// cloneTree 用 APFS clonefile 递归克隆目录（写时复制）；不支持时返回 errCloneUnsupported。
func cloneTree(src, dst string) error {
	err := clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOSYS) {
		return errCloneUnsupported
	}
	return err
}
