package toolcommand

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFiles(t *testing.T) {
	dir := "/work/app"
	for _, tc := range []struct {
		paths []string
		want  string
	}{
		{[]string{"/work/app/internal/main.go"}, "internal/main.go"},
		{[]string{"/work/app"}, "."},
		{[]string{"/work/application/x.go"}, "/work/application/x.go"},
		{[]string{"/etc/hosts"}, "/etc/hosts"},
		{[]string{"/work/app/../other/x.go"}, "/work/app/../other/x.go"},
		{[]string{"relative/x.go"}, "relative/x.go"},
		{[]string{"/work/app/a.go", " ", "/work/app/b.go", "/work/app/a.go"}, "a.go, b.go"},
		{[]string{"/work/app/a\nb.go"}, "a b.go"},
		{nil, ""},
	} {
		if got := Files(dir, tc.paths); got != tc.want {
			t.Errorf("Files(%v) = %q, want %q", tc.paths, got, tc.want)
		}
	}
	if Files("", []string{"/work/app/a.go"}) != "/work/app/a.go" {
		t.Error("no working folder changed the path")
	}
	long := "/elsewhere/" + strings.Repeat("目录/", 300) + "file.go" // check-english: zh (rune-based cut keeps UTF-8 valid)
	got := Files(dir, []string{long})
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != MaxTargetChars || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "/file.go") {
		t.Errorf("long path: %d %q", utf8.RuneCountInString(got), got[:20])
	}
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, dir+"/pkg/"+strings.Repeat("x", 10)+string(rune('a'+i%26))+strings.Repeat("y", i))
	}
	got = Files(dir, many)
	if utf8.RuneCountInString(got) != MaxTargetChars || !strings.HasPrefix(got, "pkg/") || !strings.HasSuffix(got, "…") {
		t.Errorf("long list: %q", got)
	}
}

func TestQuery(t *testing.T) {
	if got := Query("func Display\\("); got != "func Display\\(" {
		t.Error(got)
	}
	if Query("  ") != "" {
		t.Error("empty query projected")
	}
	for _, q := range []string{"api_key=abc123", "Authorization: Bearer xyz", "ghp_abcdefghijklmnop"} {
		if Query(q) != HiddenQuery {
			t.Errorf("sensitive query shown: %q", q)
		}
	}
	got := Query(strings.Repeat("q", 300))
	if utf8.RuneCountInString(got) != MaxQueryChars || !strings.HasSuffix(got, "…") {
		t.Error("query not bounded", len(got))
	}
	if strings.ContainsRune(Query("a\x1b[31mb"), '\x1b') {
		t.Error("escape leaked")
	}
}
