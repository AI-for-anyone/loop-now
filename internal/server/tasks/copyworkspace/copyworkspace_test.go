package copyworkspace

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

// snapshot describes a tree exactly: every entry's relative path, type, permissions, size, modification time, link
// target and content hash.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		line := fmt.Sprintf("%s %v %d %d", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			line += " -> " + target
		case info.Mode().IsRegular() && info.Mode().Perm()&0o400 != 0:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += fmt.Sprintf(" %x", sha256.Sum256(data))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// folder makes a plain folder with documents, a nested folder, a read-only folder and links inside it.
func folder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	write(t, filepath.Join(dir, "a.md"), "alpha")
	write(t, filepath.Join(dir, "sub", "b.txt"), "beta")
	write(t, filepath.Join(dir, "locked", "c.txt"), "gamma")
	if err := os.Chmod(filepath.Join(dir, "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })
	if err := os.Symlink("sub/b.txt", filepath.Join(dir, "to-b")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// copyPath is where a test makes its copy; it is removed at the end even when read-only folders are in it.
func copyPath(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "worktrees", "task-1", "notes")
	t.Cleanup(func() { _ = Remove(p) })
	return p
}

func TestMakeCopiesAndListsWithoutTouchingTheOriginal(t *testing.T) {
	src := folder(t)
	before := snapshot(t, src)
	dst := copyPath(t)
	l, err := Make(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if l.Files != 3 || l.Bytes != int64(len("alpha")+len("beta")+len("gamma")) || l.Left != 0 {
		t.Fatalf("listing = %+v", l)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "to-b")); err != nil || string(data) != "beta" {
		t.Fatalf("the inside link reads %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dst, ListingName)); err == nil {
		t.Fatal("the listing is inside the copy")
	}
	if _, err := os.Stat(ListingPath(dst)); err != nil {
		t.Fatal("no listing beside the copy")
	}
	// Steps write in the copy; the original stays as it was.
	write(t, filepath.Join(dst, "a.md"), "changed")
	write(t, filepath.Join(dst, "new.md"), "new")
	if after := snapshot(t, src); after != before {
		t.Fatalf("the original changed:\n%s\n---\n%s", before, after)
	}
	if err := Remove(dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(dst)); err == nil {
		t.Fatal("the task's folder was not removed")
	}
	if after := snapshot(t, src); after != before {
		t.Fatal("removing the copy changed the original")
	}
}

// Review focus: an unreadable file fails the copy with a reason naming it, and leaves nothing behind.
func TestMakeNamesAnUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions")
	}
	src := folder(t)
	secret := filepath.Join(src, "sub", "private.txt")
	write(t, secret, "x")
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })
	before := snapshot(t, src)
	dst := copyPath(t)
	_, err := Make(src, dst)
	if err == nil || !strings.Contains(err.Error(), "sub/private.txt cannot be read") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	for _, e := range entries {
		t.Errorf("left behind: %s", e.Name())
	}
	if snapshot(t, src) != before {
		t.Fatal("the original changed")
	}
}

// Making it again adopts a finished copy and redoes a partial one.
func TestMakeAgain(t *testing.T) {
	src := folder(t)
	dst := copyPath(t)
	if _, err := Make(src, dst); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dst, "work.md"), "done before the restart")
	l, err := Make(src, dst)
	if err != nil || l.Files != 3 {
		t.Fatalf("adopt: %+v, %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "work.md")); err != nil {
		t.Fatal("adopting redid the copy")
	}
	// A copy without its listing is partial: redone.
	if err := os.Remove(ListingPath(dst)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst+".copying-abc/x", 0o755); err != nil {
		t.Fatal(err)
	}
	if l, err = Make(src, dst); err != nil || l.Files != 3 {
		t.Fatalf("redo: %+v, %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "work.md")); err == nil {
		t.Fatal("a partial copy was adopted")
	}
	if _, err := os.Stat(dst + ".copying-abc"); err == nil {
		t.Fatal("a leftover partial copy was kept")
	}
}

// Review focus: added or changed files count; deleted files do not; a missing copy reads as missing.
func TestChanged(t *testing.T) {
	src := folder(t)
	dst := copyPath(t)
	if _, err := Make(src, dst); err != nil {
		t.Fatal(err)
	}
	if n, missing, err := Changed(dst); err != nil || n != 0 || missing {
		t.Fatalf("untouched copy: %d, %v, %v", n, missing, err)
	}
	if err := os.Remove(filepath.Join(dst, "a.md")); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := Changed(dst); n != 0 {
		t.Fatalf("a deleted file counted: %d", n)
	}
	write(t, filepath.Join(dst, "summary.md"), "new")
	if n, _, _ := Changed(dst); n != 1 {
		t.Fatalf("one new file: %d", n)
	}
	write(t, filepath.Join(dst, "sub", "b.txt"), "beta, edited")
	if n, _, _ := Changed(dst); n != 2 {
		t.Fatalf("plus one edited file: %d", n)
	}
	if err := Remove(dst); err != nil {
		t.Fatal(err)
	}
	if _, missing, err := Changed(dst); !missing || err != nil {
		t.Fatalf("removed copy: %v, %v", missing, err)
	}
}

// Review focus: two copies of the same folder at once are independent.
func TestTwoCopiesAtOnce(t *testing.T) {
	src := folder(t)
	before := snapshot(t, src)
	root := t.TempDir()
	paths := []string{filepath.Join(root, "task-1", "notes"), filepath.Join(root, "task-2", "notes")}
	t.Cleanup(func() {
		for _, p := range paths {
			_ = Remove(p)
		}
	})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Make(src, p)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(paths[0], "a.md"), "task 1 wrote this")
	if data, _ := os.ReadFile(filepath.Join(paths[1], "a.md")); string(data) != "alpha" {
		t.Fatalf("task 2's copy reads %q", data)
	}
	if n, _, _ := Changed(paths[1]); n != 0 {
		t.Fatal("task 2's copy changed")
	}
	if snapshot(t, src) != before {
		t.Fatal("the original changed")
	}
}
