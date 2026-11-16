package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/loopctl/output"
)

func TestSaveAndLoad(t *testing.T) {
	home := t.TempDir()
	path := Path(home)
	f, exists, e := Load(path)
	if e != nil || exists || len(f.Profiles) != 0 {
		t.Fatalf("a missing file should give an empty config: %v %v", e, exists)
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
		if runtime.GOOS != "windows" && info.Mode().Perm() != want { // on Windows it is an ACL; see privatefs
			t.Errorf("%s has mode %o, want %o", p, info.Mode().Perm(), want)
		}
	}
	got, exists, e := Load(path)
	if e != nil || !exists || got.Profiles["default"].Server != "https://a.example" {
		t.Fatalf("reading back failed: %v %+v", e, got)
	}
}

func TestSaveTightensDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits mean nothing on Windows; privacy comes from the ACL")
	}
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
		t.Errorf("directory mode is %o, want 700", info.Mode().Perm())
	}
}

func TestLoadRejectsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod cannot change the ACL on Windows, so too-broad permissions cannot be set up")
	}
	path := Path(t.TempDir())
	if err := Save(path, &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Token: "lne_secret"}}}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		os.Chmod(path, mode)
		_, _, e := Load(path)
		if e == nil || e.Code != output.CodeConfigInsecurePermissions || e.Category != output.CategoryConfig {
			t.Errorf("mode %o: should fail with config_insecure_permissions, got %v", mode, e)
		}
	}
}

func TestLoadRejectsInvalidContent(t *testing.T) {
	cases := map[string]string{
		"not JSON":        `{"schemaVersion":1,"profiles":{"default":{"token":"lne_SECRET"`,
		"unknown field":   `{"schemaVersion":1,"profiles":{},"extra":1}`,
		"wrong version":   `{"schemaVersion":2,"profiles":{}}`,
		"profile name":    `{"schemaVersion":1,"profiles":{"bad name":{}}}`,
		"several objects": `{"schemaVersion":1,"profiles":{}} {}`,
	}
	for name, content := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(content), 0o600)
		_, _, e := Load(path)
		if e == nil || e.Code != output.CodeConfigInvalid {
			t.Errorf("%s: should fail with config_invalid, got %v", name, e)
			continue
		}
		if strings.Contains(e.Message, "SECRET") {
			t.Errorf("%s: the error message leaks file content: %s", name, e.Message)
		}
	}
}

// When writing fails midway, the original file stays unchanged and no temporary file is left in the directory.
func TestSaveFailureKeepsOriginal(t *testing.T) {
	path := Path(t.TempDir())
	orig := &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Server: "https://old.example"}}}
	if err := Save(path, orig); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	rename = func(string, string) error { return errors.New("injected failure") }
	t.Cleanup(func() { rename = os.Rename })
	if err := Save(path, &File{SchemaVersion: 1, Profiles: map[string]Profile{"default": {Server: "https://new.example"}}}); err == nil {
		t.Fatal("should fail")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the original file was changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		info, _ := e.Info()
		if e.Name() != "config.json" {
			t.Errorf("leftover file %s", e.Name())
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %o, broader than 0600", e.Name(), info.Mode().Perm())
		}
	}
}
