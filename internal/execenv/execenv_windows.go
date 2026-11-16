//go:build windows

package execenv

import (
	"os"
	"path/filepath"
	"strings"
)

// systemRoot returns the Windows directory (SYSTEMROOT, usually C:\Windows).
func systemRoot() string {
	for _, k := range []string{"SYSTEMROOT", "WINDIR"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return `C:\Windows`
}

// systemDirs is the system part of the default Windows PATH: System32 (with cmd.exe and where.exe, which git often
// relies on), the Windows directory, the WMI tools directory and Windows PowerShell (Pi's bash tool maps to
// powershell on Windows).
func systemDirs() []string {
	root := systemRoot()
	return []string{
		filepath.Join(root, "System32"),
		root,
		filepath.Join(root, "System32", "Wbem"),
		filepath.Join(root, "System32", "WindowsPowerShell", "v1.0"),
	}
}

// hostCommonDirs: Windows has no conventional directories like Homebrew's; the caller adds git's directory based on
// where git.exe really is.
func hostCommonDirs() []string { return nil }

func systemPath() string { return strings.Join(systemDirs(), string(os.PathListSeparator)) }

func base(home, tmp string) []string {
	if tmp == "" {
		tmp = home
	}
	root := systemRoot()
	out := []string{
		"SYSTEMROOT=" + root,
		"WINDIR=" + root,
		"SYSTEMDRIVE=" + filepath.VolumeName(root),
		"COMSPEC=" + filepath.Join(root, "System32", "cmd.exe"),
		"PATHEXT=.COM;.EXE;.BAT;.CMD",
	}
	for _, k := range []string{"PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "ALLUSERSPROFILE"} {
		if v := os.Getenv(k); v != "" {
			out = append(out, k+"="+v)
		}
	}
	if home != "" {
		out = append(out,
			"USERPROFILE="+home,
			"HOMEDRIVE="+filepath.VolumeName(home),
			"HOMEPATH="+strings.TrimPrefix(home, filepath.VolumeName(home)),
			"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
			"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		)
	}
	if tmp != "" {
		out = append(out, "TEMP="+tmp, "TMP="+tmp)
	}
	return out
}

// pathKey: Windows paths are case-insensitive.
func pathKey(dir string) string { return strings.ToLower(dir) }
