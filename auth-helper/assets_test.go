package authhelper

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func assetRoot(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "platform", "auth-helper", Digest())
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
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) { // Windows 上由继承的 ACL 决定
			t.Fatal("asset is not private")
		}
	}
	for _, dir := range []string{root, filepath.Dir(root)} {
		info, err := os.Stat(dir)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) { // Windows 上由继承的 ACL 决定
			t.Fatal("install directory is not private")
		}
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(root), ".stage-*"))
	if err != nil || len(matches) != 0 {
		t.Fatal("asset stages were retained")
	}
	if _, err := os.Stat(filepath.Join(root, "test")); !os.IsNotExist(err) {
		t.Fatal("test fixtures installed")
	}
}
func TestDigestNamesTheInstallDirectory(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(Digest()) {
		t.Fatalf("digest %q is not 12 hex digits", Digest())
	}
}
func TestInstallPublishesOnceAndOnlyVerifiesAfterwards(t *testing.T) {
	root := assetRoot(t)
	parent := filepath.Dir(root)
	// Another build's directory beside this one is never touched.
	other := filepath.Join(parent, "000000000000")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "main.mjs"), []byte("other build"), 0600); err != nil {
		t.Fatal(err)
	}
	helper, extension, err := Install(parent)
	if err != nil {
		t.Fatal(err)
	}
	if helper != filepath.Join(root, "main.mjs") || extension != filepath.Join(root, "runtime-refresh.mjs") {
		t.Fatal("incorrect fixed entry paths")
	}
	checkAssets(t, root)
	before, err := os.Stat(helper)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Install(parent); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(helper); err != nil || !os.SameFile(before, after) {
		t.Fatal("an installed digest directory was rewritten")
	}
	checkAssets(t, root)
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
	if data, err := os.ReadFile(filepath.Join(other, "main.mjs")); err != nil || string(data) != "other build" {
		t.Fatal("another build's assets changed")
	}
}
func TestInstallRejectsChangedOrMissingAssets(t *testing.T) {
	for _, change := range []string{"content", "missing"} {
		t.Run(change, func(t *testing.T) {
			root := assetRoot(t)
			helper, _, err := Install(filepath.Dir(root))
			if err != nil {
				t.Fatal(err)
			}
			if change == "content" {
				err = os.WriteFile(helper, []byte("changed"), 0600)
			} else {
				err = os.Remove(helper)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := Install(filepath.Dir(root)); err == nil || !strings.Contains(err.Error(), "main.mjs") {
				t.Fatalf("changed assets accepted: %v", err)
			}
			data, err := os.ReadFile(helper)
			if change == "content" && (err != nil || string(data) != "changed") || change == "missing" && !os.IsNotExist(err) {
				t.Fatal("a verified directory was rewritten")
			}
		})
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
			if err := os.MkdirAll(parent, 0700); err != nil {
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
				if runtime.GOOS == "windows" {
					t.Skip("chmod 在 Windows 上改不了 ACL，无法制造公开目录")
				}
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
			if _, _, err := Install(parent); err == nil {
				t.Fatal("unsafe install target accepted")
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "keep" {
				t.Fatal("outside target modified")
			}
		})
	}
}
func TestInstallConcurrentIdenticalBinaryAssets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上目录被别的句柄打开时不能改名，并发发布的竞争语义不同")
	}
	root := assetRoot(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := Install(filepath.Dir(root)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	checkAssets(t, root)
}
