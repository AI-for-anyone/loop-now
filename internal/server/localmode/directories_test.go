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
		t.Fatalf("want a validation failure: %v", err)
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
		{Path: root, Note: strp("  Frontend repo  ")},
		{Path: filepath.Join(root, "不存在", "也可以保存")}, // check-english: zh (non-ASCII path components are accepted)
		{Path: filepath.Join(root, "b"), Note: strp("   ")},
		{Path: filepath.Join(root, "c")},
	}
	out, err := Validate(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0].Note != "Frontend repo" || out[1].Note != "" || out[2].Note != "" || out[0].Path != root {
		t.Fatalf("unexpected result: %+v", out)
	}
	if out, err := Validate([]DirectoryInput{}); err != nil || len(out) != 0 {
		t.Fatalf("an empty list should be savable: %v %v", out, err)
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
		{Path: filepath.Join(root, "ok"), Note: strp(strings.Repeat("注", MaxNoteRunes+1))}, // check-english: zh (rune-based note limit)
		{Path: filepath.Join(root, "ok2"), Note: strp("a\tb")},
		{Path: string(filepath.Separator) + strings.Repeat("a", MaxPathBytes)},
	}
	if runtime.GOOS != "windows" {
		in = append(in, DirectoryInput{Path: root + "/./x"}, DirectoryInput{Path: root + "//y"})
	}
	errs := fieldErrors(t, func() error { _, err := Validate(in); return err }())
	want := map[string]string{
		"directories[1].path": "absolute path",
		"directories[2].path": "Write it as " + root,
		"directories[3].path": "Duplicates item 1",
		"directories[4].path": "control characters",
		"directories[5].path": "Must not be empty",
		"directories[6].note": "Must be at most",
		"directories[7].note": "control characters",
		"directories[8].path": "Must be at most",
	}
	if runtime.GOOS != "windows" {
		want["directories[9].path"] = "Write it as " + root + "/x"
		want["directories[10].path"] = "Write it as " + root + "/y"
	}
	for field, sub := range want {
		if !strings.Contains(errs[field], sub) {
			t.Errorf("error for %s should contain %q, got %q", field, sub, errs[field])
		}
	}
	if len(errs) != len(want) {
		t.Errorf("field errors = %v", errs)
	}
	if _, ok := errs["directories[0].path"]; ok {
		t.Error("the first item should not be an error")
	}
}

func TestValidateLimitsAndMissingList(t *testing.T) {
	if errs := fieldErrors(t, func() error { _, err := Validate(nil); return err }()); errs["directories"] == "" {
		t.Fatalf("a missing list should report directories: %v", errs)
	}
	many := make([]DirectoryInput, MaxDirectories+1)
	for i := range many {
		many[i] = DirectoryInput{Path: filepath.Join(t.TempDir(), "d")}
	}
	if errs := fieldErrors(t, func() error { _, err := Validate(many); return err }()); !strings.Contains(errs["directories"], "at most") {
		t.Fatalf("over the limit should report directories: %v", errs)
	}
}

func TestValidateWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path rules apply only on Windows")
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
			t.Errorf("%s should be an error: %v", f, errs)
		}
	}
	if !strings.Contains(errs["directories[2].path"], `C:\x`) {
		t.Errorf("should give the canonical form: %q", errs["directories[2].path"])
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
			t.Errorf("Status(%s) = %s, want %s", p, got, want)
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
			t.Errorf("want inaccessible without read permission, got %s", got)
		}
	}
}
