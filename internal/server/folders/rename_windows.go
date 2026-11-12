//go:build windows

package folders

import (
	"os"
	"path/filepath"
)

// renameAt 在 Windows 上没有 renameat：按两个已打开目录的路径拼出源与目标改名（MoveFileEx）。
// 两个目录都是经 os.Root 或 OpenNoFollow 打开并核对过的，最后一段若是符号链接，MoveFileEx 移动的是链接本身而不是目标。
func renameAt(srcParent *os.File, srcName string, dstParent *os.File, dstName string) error {
	return os.Rename(filepath.Join(srcParent.Name(), srcName), filepath.Join(dstParent.Name(), dstName))
}
