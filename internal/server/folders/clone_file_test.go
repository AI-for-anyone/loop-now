package folders

import (
	"os"
	"path/filepath"
	"testing"
)

// A file is copied (cloned where possible, else copied) without changing the source; an existing target is refused.
func TestCloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.jsonl")
	if err := os.WriteFile(src, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, unsupported := range []bool{false, true} {
		if unsupported {
			forceCopyFallback(t)
		}
		dst := filepath.Join(dir, "out", "copy.jsonl")
		_ = os.Remove(dst)
		method, err := CloneFile(src, dst)
		if err != nil || (unsupported && method != "copy") {
			t.Fatalf("clone: %s %v", method, err)
		}
		if b, _ := os.ReadFile(dst); string(b) != "a\nb\n" {
			t.Fatalf("copy: %q", b)
		}
		if info, _ := os.Stat(dst); info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", info.Mode())
		}
		if _, err := CloneFile(src, dst); err == nil {
			t.Fatal("an existing target is refused")
		}
	}
	if b, _ := os.ReadFile(src); string(b) != "a\nb\n" {
		t.Fatal("the source changed")
	}
}
