package folders

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		t.Fatalf("写入 %s：%v", p, err)
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

// 列表只列一层：目录在前、按名称排序、按游标分页；正在写入的临时文件不出现。
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
		t.Fatalf("根目录：%v %+v", err, names(all))
	}
	if *all.Entries[2].Size != 1 || all.Entries[0].Size != nil || all.Entries[2].Path != "a.txt" {
		t.Fatalf("大小与路径：%+v", all.Entries)
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
		t.Fatalf("分页拼起来应与整页一致：%v", got)
	}
	sub, err := f.List("z", "", 0)
	if err != nil || sub.Path != "z" || !slices.Equal(names(sub), []string{"dir:deep"}) || sub.Entries[0].Path != "z/deep" {
		t.Fatalf("子目录：%v %+v", err, sub)
	}
	if _, err := f.List("a.txt", "", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("列文件应 422：%v", err)
	}
	if _, err := f.List("nope", "", 0); code(err) != apperr.CodeNotFound {
		t.Fatalf("不存在应 404：%v", err)
	}
	if _, err := f.List("", "%%%", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("坏游标应 422：%v", err)
	}
}

// 一个目录里有大量条目时按页返回，不需要一次列完。
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
		t.Fatalf("上限 %d：%v %d", MaxPageSize, err, len(page.Entries))
	}
	page, err = f.List("", *page.NextCursor, 0)
	if err != nil || len(page.Entries) != DefaultPageSize || page.Entries[0].Name != fmt.Sprintf("f%05d", MaxPageSize) {
		t.Fatalf("第二页：%v %d %s", err, len(page.Entries), page.Entries[0].Name)
	}
}

// 写入：自动创建上级目录；create 时已存在冲突；覆盖保留权限；超限 413；目标是目录或软链接 422。
func TestWriteCreateOverwriteAndLimits(t *testing.T) {
	f := testFolder(t)
	e := mustWrite(t, f, "a/b/c.txt", "v1")
	if e.Type != "file" || *e.Size != 2 || e.Path != "a/b/c.txt" || e.Name != "c.txt" {
		t.Fatalf("新建：%+v", e)
	}
	if _, err := f.Write("a/b/c.txt", strings.NewReader("v2"), 100, true); code(err) != apperr.CodePathConflict {
		t.Fatalf("create 已存在应冲突：%v", err)
	}
	if err := os.Chmod(filepath.Join(f.Dir, "a/b/c.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, f, "a/b/c.txt", "v22")
	info, _ := os.Stat(filepath.Join(f.Dir, "a/b/c.txt"))
	if b, _ := os.ReadFile(filepath.Join(f.Dir, "a/b/c.txt")); string(b) != "v22" || info.Mode().Perm() != 0o755 {
		t.Fatalf("覆盖：%q %v", b, info.Mode())
	}
	if _, err := f.Write("big.bin", bytes.NewReader(make([]byte, 11)), 10, false); code(err) != apperr.CodePayloadTooLarge {
		t.Fatalf("超限应 413：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.Dir, "big.bin")); !os.IsNotExist(err) {
		t.Fatal("超限的文件不应留下")
	}
	if left, _ := filepath.Glob(filepath.Join(f.Dir, tempPrefix+"*")); len(left) != 0 {
		t.Fatalf("临时文件应已清理：%v", left)
	}
	if _, err := f.Write("a/b", strings.NewReader("x"), 10, false); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("目标是目录应 422：%v", err)
	}
	if err := os.Symlink("a/b/c.txt", filepath.Join(f.Dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write("link", strings.NewReader("x"), 10, false); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("目标是软链接应 422：%v", err)
	}
	if _, err := f.Write("a/b/c.txt/x", strings.NewReader("x"), 10, false); code(err) == "" {
		t.Fatal("上级路径是文件应失败")
	}
	empty, err := f.Write("empty.txt", strings.NewReader(""), 10, true)
	if err != nil || *empty.Size != 0 {
		t.Fatalf("新建空文件：%v %+v", err, empty)
	}
}

// 路径安全：.. 与绝对路径拒绝；指向文件夹之外的软链接不会被跟随；指向内部的软链接可以读。
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
			t.Fatalf("%q 应 422：%v", bad, err)
		}
		if _, err := f.List(bad, "", 0); code(err) != apperr.CodeValidationFailed {
			t.Fatalf("列 %q 应 422：%v", bad, err)
		}
	}
	if _, _, err := f.Open("out/secret"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("经由外部软链接读取应 422：%v", err)
	}
	if _, err := f.List("out", "", 0); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("列外部软链接应 422：%v", err)
	}
	if _, err := f.Write("out/new", strings.NewReader("x"), 10, false); code(err) == "" {
		t.Fatal("经由外部软链接写入应失败")
	}
	if _, err := os.Lstat(filepath.Join(outside, "new")); !os.IsNotExist(err) {
		t.Fatal("不应写到文件夹之外")
	}
	if err := f.Delete("out/secret"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("经由外部软链接删除应 422：%v", err)
	}
	h, _, err := f.Open("in/a.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(h)
	h.Close()
	if string(b) != "inside" {
		t.Fatalf("内部软链接应可读：%q", b)
	}
	page, _ := f.List("", "", 0)
	for _, e := range page.Entries {
		if e.Name == "out" && (e.Type != "symlink" || e.SymlinkTarget == nil || *e.SymlinkTarget != outside) {
			t.Fatalf("软链接条目：%+v", e)
		}
	}
	if err := f.Delete("out"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("删除软链接不应影响它指向的内容")
	}
	if _, _, err := f.Open("docs"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("下载目录应 422：%v", err)
	}
}

// 新建目录、移动与删除。
func TestMkdirMoveDelete(t *testing.T) {
	f := testFolder(t)
	mustWrite(t, f, "a.txt", "a")
	if e, err := f.Mkdir("x/y"); err != nil || e.Type != "dir" {
		t.Fatalf("新建目录：%v %+v", err, e)
	}
	if _, err := f.Mkdir("x/y"); err != nil {
		t.Fatalf("已存在的目录原样返回：%v", err)
	}
	if _, err := f.Mkdir("a.txt"); code(err) != apperr.CodePathConflict {
		t.Fatalf("同名文件应冲突：%v", err)
	}
	if e, err := f.Move("a.txt", "x/y/z/b.txt"); err != nil || e.Path != "x/y/z/b.txt" {
		t.Fatalf("移动并建上级目录：%v %+v", err, e)
	}
	mustWrite(t, f, "c.txt", "c")
	if _, err := f.Move("c.txt", "x/y/z/b.txt"); code(err) != apperr.CodePathConflict {
		t.Fatalf("目标已存在应冲突：%v", err)
	}
	if _, err := f.Move("x", "x/inner"); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("移到自身之下应 422：%v", err)
	}
	if _, err := f.Move("nope", "q"); code(err) != apperr.CodeNotFound {
		t.Fatalf("来源不存在应 404：%v", err)
	}
	if err := f.Delete(""); code(err) != apperr.CodeValidationFailed {
		t.Fatalf("不能删除根：%v", err)
	}
	if err := f.Delete("nope"); code(err) != apperr.CodeNotFound {
		t.Fatalf("不存在应 404：%v", err)
	}
	if err := f.Delete("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.Dir, "x")); !os.IsNotExist(err) {
		t.Fatal("删除后应立即不在文件夹中")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if left, _ := os.ReadDir(f.Trash); len(left) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("回收区应在后台清空")
}

// 复制文件夹：克隆后改动副本不影响原文件夹；保留软链接与权限；来源不存在时得到空目录；目标已存在报错。
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
			mustWrite(t, f, "AGENTS.md", "入口")
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
			t.Logf("复制方式 %s，耗时 %v", res.Method, res.Duration)
			if res.Method != mode {
				t.Fatalf("方式：%s，期望 %s", res.Method, mode)
			}
			if b, _ := os.ReadFile(filepath.Join(dst, "node_modules/pkg/index.js")); string(b) != "module.exports = 1" {
				t.Fatal("内容应一致")
			}
			if l, _ := os.Readlink(filepath.Join(dst, "link")); l != "node_modules/pkg" {
				t.Fatalf("软链接应原样保留：%q", l)
			}
			if info, _ := os.Stat(filepath.Join(dst, "AGENTS.md")); info.Mode().Perm() != 0o640 {
				t.Fatalf("权限应保留：%v", info.Mode())
			}
			if err := os.WriteFile(filepath.Join(dst, "AGENTS.md"), []byte("任务改了"), 0o640); err != nil {
				t.Fatal(err)
			}
			if b, _ := os.ReadFile(filepath.Join(src, "AGENTS.md")); string(b) != "入口" {
				t.Fatal("改副本不应影响原文件夹")
			}
			if _, err := CopyTree(src, dst); err == nil {
				t.Fatal("目标已存在应报错")
			}
			if left, _ := filepath.Glob(dst + ".copying-*"); len(left) != 0 {
				t.Fatalf("不应留下半成品：%v", left)
			}
			empty := filepath.Join(base, "empty", "workspace")
			if res, err := CopyTree(filepath.Join(base, "missing"), empty); err != nil || res.Method != "empty" {
				t.Fatalf("来源不存在：%v %+v", err, res)
			}
			if entries, _ := os.ReadDir(empty); len(entries) != 0 {
				t.Fatal("应为空目录")
			}
		})
	}
}
