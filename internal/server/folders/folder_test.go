package folders

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"loop-now/internal/server/apperr"
)

func testFolder(t *testing.T) Folder {
	t.Helper()
	base := t.TempDir()
	return Folder{Dir: filepath.Join(base, "workspace"), Trash: filepath.Join(base, ".trash")}
}

func mustWrite(t *testing.T, f Folder, p, text string) Entry {
	t.Helper()
	e, err := f.Write(p, strings.NewReader(text), 1<<20, false)
	if err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return e
}

func code(err error) string {
	if ae, ok := apperr.As(err); ok {
		return ae.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

func names(p Page) []string {
	out := []string{}
	for _, e := range p.Entries {
		out = append(out, e.Type+":"+e.Name)
	}
	return out
}

// Listing covers only one level: directories first, sorted by name, paged by cursor; temporary files being written do
// not appear.
func TestListOneLevelDirsFirstWithCursor(t *testing.T) {
	f := testFolder(t)
	for _, p := range []string{"b.txt", "a.txt", "z/deep/x.txt", "m/y.txt", "c.txt"} {
		mustWrite(t, f, p, "x")
	}
	if err := os.WriteFile(filepath.Join(f.Dir, tempPrefix+"abc"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	all, err := f.List("", "", 0)
	if err != nil || all.Path != "" || all.NextCursor != nil || !slices.Equal(names(all), []string{"dir:m", "dir:z", "file:a.txt", "file:b.txt", "file:c.txt"}) {
		t.Fatalf("root: %v %+v", err, names(all))
	}
	if *all.Entries[2].Size != 1 || all.Entries[0].Size != nil || all.Entries[2].Path != "a.txt" {
		t.Fatalf("size and path: %+v", all.Entries)
	}
	got := []string{}
	cursor := ""
	for range 10 {
		page, err := f.List("", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, names(page)...)
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if !slices.Equal(got, names(all)) {
		t.Fatalf("pages joined should equal the full listing: %v", got)
	}
	sub, err := f.List("z", "", 0)
	if err != nil || sub.Path != "z" || !slices.Equal(names(sub), []string{"dir:deep"}) || sub.Entries[0].Path != "z/deep" {
		t.Fatalf("subdirectory: %v %+v", err, sub)
	}
	if _, err := f.List("a.txt", "", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("listing a file should be 422: %v", err)
	}
	if _, err := f.List("nope", "", 0); code(err) != apperr.CodeNotFound {
		t.Fatalf("missing path should be 404: %v", err)
	}
	if _, err := f.List("", "%%%", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("bad cursor should be 422: %v", err)
	}
}

// A directory with a great many entries is returned page by page, without listing it all at once.
func TestListLargeDirectoryPages(t *testing.T) {
	f := testFolder(t)
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 5000 {
		if err := os.WriteFile(filepath.Join(f.Dir, fmt.Sprintf("f%05d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.List("", "", MaxPageSize+100)
	if err != nil || len(page.Entries) != MaxPageSize || page.NextCursor == nil {
		t.Fatalf("limit %d: %v %d", MaxPageSize, err, len(page.Entries))
	}
	page, err = f.List("", *page.NextCursor, 0)
	if err != nil || len(page.Entries) != DefaultPageSize || page.Entries[0].Name != fmt.Sprintf("f%05d", MaxPageSize) {
		t.Fatalf("second page: %v %d %s", err, len(page.Entries), page.Entries[0].Name)
	}
}

// Write: parent directories are created automatically; with create, an existing file conflicts; overwriting keeps
// permissions; over the limit is 413; a target that is a directory or a symlink is 422.
func TestWriteCreateOverwriteAndLimits(t *testing.T) {
	f := testFolder(t)
	e := mustWrite(t, f, "a/b/c.txt", "v1")
	if e.Type != "file" || *e.Size != 2 || e.Path != "a/b/c.txt" || e.Name != "c.txt" {
		t.Fatalf("create: %+v", e)
	}
	if _, err := f.Write("a/b/c.txt", strings.NewReader("v2"), 100, true); code(err) != apperr.CodePathConflict {
		t.Fatalf("create on an existing file should conflict: %v", err)
	}
	if err := os.Chmod(filepath.Join(f.Dir, "a/b/c.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, f, "a/b/c.txt", "v22")
	info, _ := os.Stat(filepath.Join(f.Dir, "a/b/c.txt"))
	if b, _ := os.ReadFile(filepath.Join(f.Dir, "a/b/c.txt")); string(b) != "v22" || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755) {
		t.Fatalf("overwrite: %q %v", b, info.Mode())
	}
	if _, err := f.Write("big.bin", bytes.NewReader(make([]byte, 11)), 10, false); code(err) != apperr.CodePayloadTooLarge {
		t.Fatalf("over the limit should be 413: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.Dir, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("a file over the limit should not be left behind")
	}
	if left, _ := filepath.Glob(filepath.Join(f.Dir, tempPrefix+"*")); len(left) != 0 {
		t.Fatalf("temporary files should be cleaned up: %v", left)
	}
	if _, err := f.Write("a/b", strings.NewReader("x"), 10, false); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("a directory target should be 422: %v", err)
	}
	if err := os.Symlink("a/b/c.txt", filepath.Join(f.Dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write("link", strings.NewReader("x"), 10, false); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("a symlink target should be 422: %v", err)
	}
	if _, err := f.Write("a/b/c.txt/x", strings.NewReader("x"), 10, false); code(err) == "" {
		t.Fatal("a parent path that is a file should fail")
	}
	empty, err := f.Write("empty.txt", strings.NewReader(""), 10, true)
	if err != nil || *empty.Size != 0 {
		t.Fatalf("create empty file: %v %+v", err, empty)
	}
}

// Path safety: .. and absolute paths are rejected; symlinks pointing outside the folder are not followed; symlinks
// pointing inside can be read.
func TestPathSafetyAndSymlinks(t *testing.T) {
	f := testFolder(t)
	mustWrite(t, f, "docs/a.md", "inside")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.Dir, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs", filepath.Join(f.Dir, "in")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../x", "/etc/passwd", "a/../b", "a//b", "a/"} {
		if _, err := f.Write(bad, strings.NewReader("x"), 10, false); code(err) != apperr.CodeValidationFailed {
			t.Fatalf("%q should be 422: %v", bad, err)
		}
		if _, err := f.List(bad, "", 0); code(err) != apperr.CodeValidationFailed {
			t.Fatalf("listing %q should be 422: %v", bad, err)
		}
	}
	if _, _, err := f.Open("out/secret"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("reading through an outside symlink should be 422: %v", err)
	}
	if _, err := f.List("out", "", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("listing an outside symlink should be 422: %v", err)
	}
	if _, err := f.Write("out/new", strings.NewReader("x"), 10, false); code(err) == "" {
		t.Fatal("writing through an outside symlink should fail")
	}
	if _, err := os.Lstat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("should not write outside the folder")
	}
	if err := f.Delete("out/secret"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("deleting through an outside symlink should be 422: %v", err)
	}
	h, _, err := f.Open("in/a.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(h)
	h.Close()
	if string(b) != "inside" {
		t.Fatalf("an inside symlink should be readable: %q", b)
	}
	page, _ := f.List("", "", 0)
	for _, e := range page.Entries {
		if e.Name == "out" && (e.Type != "symlink" || e.SymlinkTarget == nil || *e.SymlinkTarget != outside) {
			t.Fatalf("symlink entry: %+v", e)
		}
	}
	if err := f.Delete("out"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("deleting a symlink should not affect its target")
	}
	if _, _, err := f.Open("docs"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("downloading a directory should be 422: %v", err)
	}
}

// Create directories, move and delete.
func TestMkdirMoveDelete(t *testing.T) {
	f := testFolder(t)
	mustWrite(t, f, "a.txt", "a")
	if e, err := f.Mkdir("x/y"); err != nil || e.Type != "dir" {
		t.Fatalf("mkdir: %v %+v", err, e)
	}
	if _, err := f.Mkdir("x/y"); err != nil {
		t.Fatalf("an existing directory should be returned as is: %v", err)
	}
	if _, err := f.Mkdir("a.txt"); code(err) != apperr.CodePathConflict {
		t.Fatalf("a file with the same name should conflict: %v", err)
	}
	if e, err := f.Move("a.txt", "x/y/z/b.txt"); err != nil || e.Path != "x/y/z/b.txt" {
		t.Fatalf("move and create parent directories: %v %+v", err, e)
	}
	mustWrite(t, f, "c.txt", "c")
	if _, err := f.Move("c.txt", "x/y/z/b.txt"); code(err) != apperr.CodePathConflict {
		t.Fatalf("an existing target should conflict: %v", err)
	}
	if _, err := f.Move("x", "x/inner"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("moving under itself should be 422: %v", err)
	}
	if _, err := f.Move("nope", "q"); code(err) != apperr.CodeNotFound {
		t.Fatalf("a missing source should be 404: %v", err)
	}
	if err := f.Delete(""); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("deleting the root should fail: %v", err)
	}
	if err := f.Delete("nope"); code(err) != apperr.CodeNotFound {
		t.Fatalf("missing path should be 404: %v", err)
	}
	if err := f.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.Dir, "x")); !os.IsNotExist(err) {
		t.Fatal("should be gone from the folder right after deletion")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if left, _ := os.ReadDir(f.Trash); len(left) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the trash should be emptied in the background")
}

// Copying a folder: after the clone, changing the copy does not affect the original folder; symlinks and permissions
// are kept; a missing source gives an empty directory; an existing target is an error.
func TestCopyTreeCloneAndFallback(t *testing.T) {
	for _, mode := range []string{"clonefile", "copy"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "copy" {
				forceCopyFallback(t)
			}
			base := t.TempDir()
			src := filepath.Join(base, "src")
			f := Folder{Dir: src, Trash: filepath.Join(base, ".trash")}
			mustWrite(t, f, "node_modules/pkg/index.js", "module.exports = 1")
			mustWrite(t, f, "AGENTS.md", "entry point")
			if err := os.Chmod(filepath.Join(src, "AGENTS.md"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("node_modules/pkg", filepath.Join(src, "link")); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(base, "task", "workspace")
			res, err := CopyTree(src, dst)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "clonefile" && !cloneSupported(t, base) {
				mode = "copy"
			}
			t.Logf("copy method %s, took %v", res.Method, res.Duration)
			if res.Method != mode {
				t.Fatalf("method: %s, want %s", res.Method, mode)
			}
			if b, _ := os.ReadFile(filepath.Join(dst, "node_modules/pkg/index.js")); string(b) != "module.exports = 1" {
				t.Fatal("contents should match")
			}
			if l, _ := os.Readlink(filepath.Join(dst, "link")); filepath.ToSlash(l) != "node_modules/pkg" {
				t.Fatalf("symlink should be kept as is: %q", l)
			}
			if info, _ := os.Stat(filepath.Join(dst, "AGENTS.md")); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
				t.Fatalf("permissions should be kept: %v", info.Mode())
			}
			if err := os.WriteFile(filepath.Join(dst, "AGENTS.md"), []byte("changed by the task"), 0o640); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(src, "AGENTS.md")); string(b) != "entry point" {
				t.Fatal("changing the copy should not affect the original folder")
			}
			if _, err := CopyTree(src, dst); err == nil {
				t.Fatal("an existing target should fail")
			}
			if left, _ := filepath.Glob(dst + ".copying-*"); len(left) != 0 {
				t.Fatalf("no partial copy should be left: %v", left)
			}
			empty := filepath.Join(base, "empty", "workspace")
			if res, err := CopyTree(filepath.Join(base, "missing"), empty); err != nil || res.Method != "empty" {
				t.Fatalf("missing source: %v %+v", err, res)
			}
			if entries, _ := os.ReadDir(empty); len(entries) != 0 {
				t.Fatal("should be an empty directory")
			}
		})
	}
}
