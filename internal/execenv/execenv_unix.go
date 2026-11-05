//go:build unix

package execenv

func systemDirs() []string      { return []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} }
func hostCommonDirs() []string  { return []string{"/opt/homebrew/bin", "/usr/local/bin"} }
func systemPath() string        { return "/usr/bin:/bin:/usr/sbin:/sbin" }
func base(_, _ string) []string { return nil }
