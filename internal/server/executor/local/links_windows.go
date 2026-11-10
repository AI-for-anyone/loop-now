//go:build windows

package local

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linkDir 在 Windows 上建目录联接（junction）：不需要建软链接的特权，状态目录与 Agent 目录都在工作根下、同一个卷。
func linkDir(target, link string) error {
	if strings.ContainsAny(target+link, "\"&|<>^%") {
		return fmt.Errorf("路径含有不能交给 mklink 的字符")
	}
	cmd := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if out, err := exec.Command(cmd, "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		return fmt.Errorf("建立目录联接失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// linkFile 在 Windows 上建硬链接：同一个卷上不需要特权；Pi 原地写 auth.json，写入对所有链接可见。
func linkFile(target, link string) error { return os.Link(target, link) }

// isLink 报告 path 是不是链接本身（软链接或目录联接）。
func isLink(path string, info fs.FileInfo) bool {
	if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return true
	}
	_, err := os.Readlink(path)
	return err == nil
}
