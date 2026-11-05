package service

import (
	"runtime"
	"testing"
)

// skipWithoutCLITemplates 跳过要安装 CLI 模板的验证测试：模板目前没有 Windows 安装包，验证会以 verification_unsupported 结束。
func skipWithoutCLITemplates(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("CLI 模板还没有 Windows 安装包")
	}
}
