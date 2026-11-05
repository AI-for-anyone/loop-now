//go:build unix

package folders

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameAt 按两个已打开的目录描述符改名（renameat），不经由任何路径重新解析：源与目标的最后一段都不跟随符号链接。
func renameAt(srcParent *os.File, srcName string, dstParent *os.File, dstName string) error {
	return unix.Renameat(int(srcParent.Fd()), srcName, int(dstParent.Fd()), dstName)
}
