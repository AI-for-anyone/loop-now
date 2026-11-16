package local

import (
	"runtime"
	"testing"
)

// skipWithoutCLITemplates skips tests that need to install CLI templates (gh, lark-cli, etc.): the templates have no
// Windows install package for now.
func skipWithoutCLITemplates(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("CLI 模板还没有 Windows 安装包")
	}
}

// skipOnWindows skips tests that rely on Unix-only features (/bin/sleep, permission bits).
func skipOnWindows(t *testing.T, reason string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip(reason)
	}
}
