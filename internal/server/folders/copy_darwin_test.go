package folders

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// forceCopyFallback 让 clonefile 报告不支持，覆盖退回普通复制的路径。
func forceCopyFallback(t *testing.T) {
	orig := clonefile
	clonefile = func(string, string, int) error { return unix.ENOTSUP }
	t.Cleanup(func() { clonefile = orig })
}

// cloneSupported 报告 dir 所在的文件系统是否支持 clonefile（APFS）。
func cloneSupported(t *testing.T, dir string) bool {
	src := filepath.Join(dir, ".probe-src")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(src)
	dst := filepath.Join(dir, ".probe-dst")
	defer os.Remove(dst)
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) == nil
}
