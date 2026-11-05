//go:build windows

package execenv

import (
	"os"
	"path/filepath"
	"strings"
)

// systemRoot 取 Windows 目录（SYSTEMROOT，通常是 C:\Windows）。
func systemRoot() string {
	for _, k := range []string{"SYSTEMROOT", "WINDIR"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return `C:\Windows`
}

// systemDirs 是 Windows 默认 PATH 里的系统部分：System32（含 git 常依赖的 cmd.exe、where.exe）、Windows 目录、
// WMI 工具目录和 Windows PowerShell（Pi 的 bash 工具在 Windows 上对应 powershell）。
func systemDirs() []string {
	root := systemRoot()
	return []string{
		filepath.Join(root, "System32"),
		root,
		filepath.Join(root, "System32", "Wbem"),
		filepath.Join(root, "System32", "WindowsPowerShell", "v1.0"),
	}
}

// hostCommonDirs：Windows 上没有 Homebrew 那样的约定目录；git 的目录由调用方按 git.exe 的真实位置加入。
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
