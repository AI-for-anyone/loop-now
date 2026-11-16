package folders

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// forceCopyFallback makes clonefile report that it is not supported, to cover the fallback path to a plain copy.
func forceCopyFallback(t *testing.T) {
	orig := clonefile
	clonefile = func(string, string, int) error { return unix.ENOTSUP }
	t.Cleanup(func() { clonefile = orig })
}

// cloneSupported reports whether the file system that dir is on supports clonefile (APFS).
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
