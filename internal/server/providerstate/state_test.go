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

// assertPrivate 核对 path 只有当前用户能访问：各平台都看 privatefs（Windows 上是 DACL），Unix 上再核对确切的权限位。
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

// 发布：写 auth.json、收紧权限、改名为正式目录，再提交；提交失败或不是有效条目时目录删除。软链接不跟随。
func TestPublish(t *testing.T) {
	// 上级目录对所有人开放：状态目录的私有不能靠继承上级（Windows 的临时目录本来就私有）。
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
	// 扩展在 HOME 里写下对所有人开放的目录与文件，发布时要收紧。
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
	// 状态目录里是订阅令牌：发布后整棵树只有当前用户能访问。
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
