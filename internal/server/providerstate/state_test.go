package providerstate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/privatefs"
	"loop-now/internal/testutil/fsperm"
)

// assertPrivate checks that only the current user can access path: on every platform it asks privatefs (the DACL on
// Windows), and on Unix it also checks the exact permission bits.
func assertPrivate(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !privatefs.IsPrivate(path, info) || (runtime.GOOS != "windows" && info.Mode().Perm() != mode) {
		t.Fatalf("%s 不是只有当前用户能访问：%v", path, info.Mode())
	}
}

// Publish writes auth.json, tightens permissions, renames to the final directory and then commits; when the commit
// fails or the entry is not valid, the directory is deleted. Symlinks are not followed.
func TestPublish(t *testing.T) {
	// The parent directory is open to everyone: the state directory must not rely on inheriting privacy from its
	// parent (on Windows the temporary directory is already private).
	parent := t.TempDir()
	fsperm.Open(t, parent)
	m := &Manager{Root: filepath.Join(parent, "states")}
	v := uuid.New()
	home, err := m.Begin(v)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, m.Root, 0o700)
	assertPrivate(t, home, 0o700)
	// The extension writes directories and files open to everyone into HOME; publishing must tighten them.
	nested := filepath.Join(home, ".pi", "x")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	fsperm.Open(t, nested)
	if err := os.WriteFile(filepath.Join(nested, "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsperm.Open(t, filepath.Join(nested, "a.json"))
	if info, err := os.Lstat(filepath.Join(nested, "a.json")); err != nil || privatefs.IsPrivate(filepath.Join(nested, "a.json"), info) {
		t.Fatalf("发布前的文件应对所有人开放，否则核对不出收紧：%v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(outside, filepath.Join(home, "link")); err != nil {
			t.Fatal(err)
		}
	}
	committed := false
	if err := m.Publish(v, "p", []byte(`{"type":"oauth","access":"m"}`), func() error { committed = true; return nil }); err != nil || !committed {
		t.Fatalf("发布：%v %v", err, committed)
	}
	b, err := os.ReadFile(filepath.Join(m.Dir(v), AuthFile))
	if err != nil || string(b) != `{"p":{"type":"oauth","access":"m"}}` {
		t.Fatalf("auth.json：%s %v", b, err)
	}
	// The state directory holds subscription tokens: after publishing, only the current user can access the whole
	// tree.
	final := m.Dir(v)
	for path, mode := range map[string]os.FileMode{final: 0o700, filepath.Join(final, ".pi"): 0o700, filepath.Join(final, ".pi", "x"): 0o700, filepath.Join(final, ".pi", "x", "a.json"): 0o600, filepath.Join(final, AuthFile): 0o600} {
		assertPrivate(t, path, mode)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(outside); info.Mode().Perm() != 0o644 {
			t.Fatal("不能经软链接改外面的文件")
		}
	}

	v2 := uuid.New()
	if _, err := m.Begin(v2); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(v2, "p", []byte(`{"type":"oauth"}`), func() error { return ErrNotSaved }); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("提交失败：%v", err)
	}
	if _, err := os.Stat(m.Dir(v2)); !os.IsNotExist(err) {
		t.Fatal("提交失败时删除目录")
	}
	v3 := uuid.New()
	if _, err := m.Begin(v3); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish(v3, "p", []byte(`"not an object"`), func() error { t.Fatal("不该提交"); return nil }); err == nil {
		t.Fatal("不是对象的条目应拒绝")
	}
	if entries, _ := os.ReadDir(m.Root); len(entries) != 1 {
		t.Fatalf("只剩第一个版本：%v", entries)
	}
}
