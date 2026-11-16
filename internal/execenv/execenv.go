// Package execenv provides the platform-dependent parts of a child process environment: system directories, a
// minimal PATH, and the base variables child processes need on Windows.
// The server builds every child process environment starting from empty; on Unix only a few such as HOME and PATH
// are needed, while on Windows node.exe, git.exe and PowerShell also need variables such as SYSTEMROOT, COMSPEC and
// PATHEXT, plus USERPROFILE / APPDATA / TEMP corresponding to HOME.
package execenv

import "path/filepath"

// SystemDirs returns the system directories that go at the end of the exec PATH.
func SystemDirs() []string { return systemDirs() }

// HostCommonDirs returns the host machine's common tool directories, which go before the system directories (the
// caller only adds the ones that exist).
func HostCommonDirs() []string { return hostCommonDirs() }

// SystemPath returns a minimal PATH containing only the system directories, for child processes that only run
// system tools.
func SystemPath() string { return systemPath() }

// PathKey returns the key used to compare whether two PATH directories are the same: the path is cleaned first, and
// on Windows also made case-insensitive (C:\WINDOWS\System32 from SYSTEMROOT and the C:\WINDOWS\system32 commonly
// found in a user's PATH are the same directory).
func PathKey(dir string) string { return pathKey(filepath.Clean(dir)) }

// Base returns the platform base variables a child process needs (as K=V): none on Unix; on Windows, system
// variables such as SYSTEMROOT, plus USERPROFILE, HOMEDRIVE/HOMEPATH, APPDATA, LOCALAPPDATA, TEMP and TMP set from
// home and tmp.
// Callers put these before their own variables and append their own variables of the same names (HOME, TMPDIR,
// PATH) as usual. When tmp is empty, home is used.
func Base(home, tmp string) []string { return base(home, tmp) }
