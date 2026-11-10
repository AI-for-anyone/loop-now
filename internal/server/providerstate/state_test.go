package providerstate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

// 发布：写 auth.json、收紧权限、改名为正式目录，再提交；提交失败或不是有效条目时目录删除。软链接不跟随。
func TestPublish(t *testing.T) {
	m := &Manager{Root: filepath.Join(t.TempDir(), "states")}
	v := uuid.New()
	home, err := m.Begin(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pi", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi", "x", "a.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
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
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(m.Dir(v), ".pi", "x", "a.json")); info.Mode().Perm() != 0o600 {
			t.Fatalf("文件权限：%v", info.Mode())
		}
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
