package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/loopctl/output"
)

func TestSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	f, exists, e := Load(path)
	if e != nil || exists || len(f.Profiles) != 0 {
		t.Fatalf("文件不存在时应返回空配置：%v %v", e, exists)
	}
	f.Profiles["default"] = Profile{Server: "https://a.example", Token: "lne_x"}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{filepath.Dir(path): 0o700, path: 0o600} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s 的权限为 %o，应为 %o", p, info.Mode().Perm(), want)
		}
	}
	got, exists, e := Load(path)
	if e != nil || !exists || got.Profiles["default"].Server != "https://a.example" {
		t.Fatalf("读回失败：%v %+v", e, got)
	}
}

func TestSaveTightensDirectory(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &File{SchemaVersion: 1, Profiles: map[string]Profile{}}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0o700 {
		t.Errorf("目录权限为 %o，应为 700", info.Mode().Perm())
	}
}

func TestLoadRejectsInsecurePermissions(t *testing.T) {
	path := Path(t.TempDir())
	if err := Save(path, &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Token: "lne_secret"}}}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		os.Chmod(path, mode)
		_, _, e := Load(path)
		if e == nil || e.Code != output.CodeConfigInsecurePermissions || e.Category != output.CategoryConfig {
			t.Errorf("权限 %o：应以 config_insecure_permissions 失败，得到 %v", mode, e)
		}
	}
}

func TestLoadRejectsInvalidContent(t *testing.T) {
	cases := map[string]string{
		"不是 JSON":   `{"schemaVersion":1,"profiles":{"default":{"token":"lne_SECRET"`,
		"未知字段":      `{"schemaVersion":1,"profiles":{},"extra":1}`,
		"版本不符":      `{"schemaVersion":2,"profiles":{}}`,
		"profile 名": `{"schemaVersion":1,"profiles":{"bad name":{}}}`,
		"多个对象":      `{"schemaVersion":1,"profiles":{}} {}`,
	}
	for name, content := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(content), 0o600)
		_, _, e := Load(path)
		if e == nil || e.Code != output.CodeConfigInvalid {
			t.Errorf("%s：应以 config_invalid 失败，得到 %v", name, e)
			continue
		}
		if strings.Contains(e.Message, "SECRET") {
			t.Errorf("%s：错误说明中带出了文件内容：%s", name, e.Message)
		}
	}
}

// 写入过程中失败时，原文件保持不变，目录中没有残留的临时文件。
func TestSaveFailureKeepsOriginal(t *testing.T) {
	path := Path(t.TempDir())
	orig := &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Server: "https://old.example"}}}
	if err := Save(path, orig); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	rename = func(string, string) error { return errors.New("注入的失败") }
	t.Cleanup(func() { rename = os.Rename })
	if err := Save(path, &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Server: "https://new.example"}}}); err == nil {
		t.Fatal("应当失败")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("原文件被改动")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		info, _ := e.Info()
		if e.Name() != "config.json" {
			t.Errorf("残留文件 %s", e.Name())
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s 的权限 %o 宽于 0600", e.Name(), info.Mode().Perm())
		}
	}
}
