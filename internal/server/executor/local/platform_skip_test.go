package local

import (
	"runtime"
	"testing"
)

// skipWithoutCLITemplates 跳过要安装 CLI 模板（gh、lark-cli 等）的测试：模板目前没有 Windows 安装包。
func skipWithoutCLITemplates(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("CLI 模板还没有 Windows 安装包")
	}
}

// skipOnWindows 跳过依赖 Unix 专有能力（/bin/sleep、权限位）的测试。
func skipOnWindows(t *testing.T, reason string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip(reason)
	}
}
