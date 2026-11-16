//go:build unix

package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// bundledNode is the relative path of the bundled Node in the release directory (the layout of the official Node
// tarball).
const bundledNode = "runtime/node/bin/node"

// defaultDataDir: .loop_now under HOME; empty when HOME cannot be determined.
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

// defaultWorkRoot: a machine-wide directory outside the user's home (Pi loads AGENTS.md/CLAUDE.md from every parent
// directory of the working directory upwards).
func defaultWorkRoot() string {
	if runtime.GOOS == "darwin" {
		return "/Users/Shared/loop-now"
	}
	return "/var/lib/loop-now"
}
