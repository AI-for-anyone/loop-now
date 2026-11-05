//go:build unix

package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// bundledNode 是随包 Node 在发布目录里的相对路径（官方 tar 包的布局）。
const bundledNode = "runtime/node/bin/node"

// defaultDataDir：HOME 下的 .loop_now；取不到 HOME 时为空。
func defaultDataDir(getenv func(string) string) string {
	home := getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".loop_now")
}

// defaultWorkRoot：用户 home 之外的机器级目录（Pi 会向上加载工作目录所有上级目录里的 AGENTS.md/CLAUDE.md）。
func defaultWorkRoot() string {
	if runtime.GOOS == "darwin" {
		return "/Users/Shared/loop-now"
	}
	return "/var/lib/loop-now"
}
