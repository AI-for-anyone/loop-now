//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// bundledNode 是随包 Node 在发布目录里的相对路径（官方 zip 包解开后 node.exe 直接在顶层）。
const bundledNode = "runtime/node/node.exe"

// defaultDataDir：%LOCALAPPDATA%\Loop Now\data（本机、不漫游的用户数据目录，和桌面壳的目录风格一致）。
// LOOP_NOW_DATA_DIR 环境变量仍然优先。取不到 LOCALAPPDATA 时退回 os.UserCacheDir（Windows 上同样是 LocalAppData）。
func defaultDataDir(getenv func(string) string) string {
	base := getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserCacheDir()
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "Loop Now", "data")
}

// defaultWorkRoot：%ProgramData%\loop-now。工作根必须在用户配置目录之外（Pi 会向上加载工作目录所有上级目录里的
// AGENTS.md/CLAUDE.md，LOCALAPPDATA 在用户配置目录之内），ProgramData 是 /var/lib 与 /Users/Shared 在 Windows 上的对应，
// 普通用户可以在其中建目录，建好后由启动检查设为只有当前用户可访问。
func defaultWorkRoot() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "loop-now")
}
