package localmode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/server/apperr"
)

func strp(s string) *string { return &s }

func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	ae, ok := apperr.As(err)
	if !ok || ae.Code != apperr.CodeValidationFailed {
		t.Fatalf("应为校验失败：%v", err)
	}
	out := map[string]string{}
	for _, f := range ae.Fields {
		out[f.Field] = f.Message
	}
	return out
}

func TestValidateAcceptsCanonicalAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	in := []DirectoryInput{
		{Path: root, Note: strp("  前端仓库  ")},
		{Path: filepath.Join(root, "不存在", "也可以保存")},
		{Path: filepath.Join(root, "b"), Note: strp("   ")},
		{Path: filepath.Join(root, "c")},
	}
	out, err := Validate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0].Note != "前端仓库" || out[1].Note != "" || out[2].Note != "" || out[0].Path != root {
		t.Fatalf("结果不对：%+v", out)
	}
	if out, err := Validate([]DirectoryInput{}); err != nil || len(out) != 0 {
		t.Fatalf("空清单应可保存：%v %v", out, err)
	}
}

func TestValidateReportsIndexedFieldErrors(t *testing.T) {
	root := t.TempDir()
	in := []DirectoryInput{
		{Path: root},
		{Path: "relative/dir"},
		{Path: root + string(filepath.Separator)},
		{Path: root},
		{Path: filepath.Join(root, "x") + "\n" + filepath.Join(root, "y")},
		{Path: ""},
		{Path: filepath.Join(root, "ok"), Note: strp(strings.Repeat("注", MaxNoteRunes+1))},
		{Path: filepath.Join(root, "ok2"), Note: strp("a\tb")},
		{Path: string(filepath.Separator) + strings.Repeat("a", MaxPathBytes)},
	}
	if runtime.GOOS != "windows" {
		in = append(in, DirectoryInput{Path: root + "/./x"}, DirectoryInput{Path: root + "//y"})
	}
	errs := fieldErrors(t, func() error { _, err := Validate(in); return err }())
	want := map[string]string{
		"directories[1].path": "绝对路径",
		"directories[2].path": "应写作 " + root,
		"directories[3].path": "与第 1 项重复",
		"directories[4].path": "控制字符",
		"directories[5].path": "不能为空",
		"directories[6].note": "最长",
		"directories[7].note": "控制字符",
		"directories[8].path": "最长",
	}
	if runtime.GOOS != "windows" {
		want["directories[9].path"] = "应写作 " + root + "/x"
		want["directories[10].path"] = "应写作 " + root + "/y"
	}
	for field, sub := range want {
		if !strings.Contains(errs[field], sub) {
			t.Errorf("%s 的错误应含 %q，实际 %q", field, sub, errs[field])
		}
	}
	if len(errs) != len(want) {
		t.Errorf("字段错误 = %v", errs)
	}
	if _, ok := errs["directories[0].path"]; ok {
		t.Error("第一项不应报错")
	}
}

func TestValidateLimitsAndMissingList(t *testing.T) {
	if errs := fieldErrors(t, func() error { _, err := Validate(nil); return err }()); errs["directories"] == "" {
		t.Fatalf("缺少清单应报 directories：%v", errs)
	}
	many := make([]DirectoryInput, MaxDirectories+1)
	for i := range many {
		many[i] = DirectoryInput{Path: filepath.Join(t.TempDir(), "d")}
	}
	if errs := fieldErrors(t, func() error { _, err := Validate(many); return err }()); !strings.Contains(errs["directories"], "最多") {
		t.Fatalf("超过上限应报 directories：%v", errs)
	}
}

func TestValidateWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 路径规则只在 Windows 上生效")
	}
	ok := []DirectoryInput{{Path: `C:\Users\me\code`}, {Path: `\\server\share\dir`}, {Path: `D:\`}}
	if _, err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	errs := fieldErrors(t, func() error {
		_, err := Validate([]DirectoryInput{{Path: `C:\Code`}, {Path: `c:\code`}, {Path: `C:/x`}, {Path: `\x`}, {Path: `C:x`}})
		return err
	}())
	for _, f := range []string{"directories[1].path", "directories[2].path", "directories[3].path", "directories[4].path"} {
		if errs[f] == "" {
			t.Errorf("%s 应报错：%v", f, errs)
		}
	}
	if !strings.Contains(errs["directories[2].path"], `C:\x`) {
		t.Errorf("应给出规范写法：%q", errs["directories[2].path"])
	}
}

func TestStatus(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(root, link); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	cases := map[string]string{
		root:                             StatusOK,
		filepath.Join(root, "nope"):      StatusMissing,
		file:                             StatusNotDirectory,
		filepath.Join(file, "under"):     StatusMissing,
		filepath.Join(root, "nope", "x"): StatusMissing,
	}
	if runtime.GOOS != "windows" {
		cases[link] = StatusOK
	}
	for p, want := range cases {
		if got := Status(p); got != want {
			t.Errorf("Status(%s) = %s，应为 %s", p, got, want)
		}
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(root, "locked")
		if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if got := Status(filepath.Join(locked, "inner")); got != StatusInaccessible {
			t.Errorf("无权限读取时应为 inaccessible，实际 %s", got)
		}
	}
}
