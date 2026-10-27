package authhelper

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func assetRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "platform", "auth-helper-v1.1.0")
}
func checkAssets(t *testing.T, root string) {
	t.Helper()
	files, err := assets.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		expected, err := assets.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(root, f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(expected, actual) {
			t.Fatalf("asset mismatch: %s", f.Name())
		}
		info, err := os.Stat(filepath.Join(root, f.Name()))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("asset is not private")
		}
	}
	for _, dir := range []string{root, filepath.Dir(root)} {
		info, err := os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("install directory is not private")
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".auth-helper-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("asset stages were retained")
	}
	if _, err := os.Stat(filepath.Join(root, "test")); !os.IsNotExist(err) {
		t.Fatal("test fixtures installed")
	}
}
func TestInstallEmbeddedAssetsAndAtomicReplacement(t *testing.T) {
	root := assetRoot(t)
	helper, extension, err := Install(root)
	if err != nil {
		t.Fatal(err)
	}
	if helper != filepath.Join(root, "main.mjs") || extension != filepath.Join(root, "runtime-refresh.mjs") {
		t.Fatal("incorrect fixed entry paths")
	}
	checkAssets(t, root)
	// An open old file remains unchanged while its pathname is atomically replaced.
	if err := os.WriteFile(helper, []byte("old fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(helper)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	sentinel := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(root); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 11)
	if _, err := old.Read(data); err != nil || string(data) != "old fixture" {
		t.Fatal("old inode was modified in place")
	}
	checkAssets(t, root)
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
}
func TestInstallRejectsSymlinksAndPublicDirectories(t *testing.T) {
	for _, bad := range []string{"relative", "/", "/tmp/../other", "/tmp/bad\x00path"} {
		if _, _, err := Install(bad); err == nil {
			t.Fatal("invalid root accepted")
		}
	}
	for _, target := range []string{"parent", "root", "asset", "directory-asset", "public-parent", "public-root"} {
		t.Run(target, func(t *testing.T) {
			root := assetRoot(t)
			parent := filepath.Dir(root)
			if err := os.Mkdir(parent, 0700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			marker := filepath.Join(outside, "marker")
			if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			switch target {
			case "parent":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, parent); err != nil {
					t.Fatal(err)
				}
			case "root":
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(parent, 0755); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if target == "public-root" {
					if err := os.Chmod(root, 0755); err != nil {
						t.Fatal(err)
					}
				} else if target == "asset" {
					if err := os.Symlink(marker, filepath.Join(root, "main.mjs")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(filepath.Join(root, "main.mjs"), 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, _, err := Install(root); err == nil {
				t.Fatal("unsafe install target accepted")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
				t.Fatal("outside target modified")
			}
		})
	}
}
func TestInstallConcurrentIdenticalBinaryAssets(t *testing.T) {
	root := assetRoot(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := Install(root); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	checkAssets(t, root)
}
