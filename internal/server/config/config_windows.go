//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// bundledNode is the relative path of the bundled Node in the release directory (once the official Node zip is
// unpacked, node.exe sits directly at its top level).
const bundledNode = "runtime/node/node.exe"

// defaultDataDir: %LOCALAPPDATA%\Loop Now\data (the local, non-roaming user data directory, in the same style as the
// desktop shell's directories).
// The LOOP_NOW_DATA_DIR environment variable still takes precedence. When LOCALAPPDATA is unavailable it falls back
// to os.UserCacheDir (which is also LocalAppData on Windows).
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

// defaultWorkRoot: %ProgramData%\loop-now. The work root must be outside the user profile directory (Pi loads
// AGENTS.md/CLAUDE.md from every parent directory of the working directory upwards, and LOCALAPPDATA is inside the user
// profile directory). ProgramData is the Windows counterpart of /var/lib and /Users/Shared; ordinary users can create
// directories in it, and once created the startup checks restrict it to the current user.
func defaultWorkRoot() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "loop-now")
}
