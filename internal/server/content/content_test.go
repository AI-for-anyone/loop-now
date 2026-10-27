package content

import (
	"archive/zip"
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func TestCleanRelPath(t *testing.T) {
	for _, ok := range []string{"a", "docs/api", "guide/images/a.png", "中文/说明.md"} {
		if _, err := CleanRelPath(ok); err != nil {
			t.Errorf("%q 应合法：%v", ok, err)
		}
	}
	for _, bad := range []string{"", "/abs", "../other", "a/../b", "a/./b", "./a", "a/", "a//b", `a\b`, "a\x00b", "a/.."} {
		if _, err := CleanRelPath(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		}
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"docs", "docs", true}, {"docs", "docs/api", true}, {"docs/api", "docs", true}, {"docs", "docs2", false}, {"a/b", "a/c", false}}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestValidateGitURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/org/repo.git", "git@github.com:org/repo.git", "ssh://git@host/org/repo", "file:///tmp/repo.git"} {
		if err := ValidateGitURL(ok); err != nil {
			t.Errorf("%q 应合法：%v", ok, err)
		}
	}
	for _, bad := range []string{"", "https://user:token@host/repo.git", "https://token@host/repo.git", "ssh://git:pw@host/repo", "-uhost", "ftp://host/repo", "/tmp/repo"} {
		if err := ValidateGitURL(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		}
	}
}

func TestValidateBranch(t *testing.T) {
	for _, ok := range []string{"main", "feature/x", "release-1.0"} {
		if err := ValidateBranch(ok); err != nil {
			t.Errorf("%q 应合法：%v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a..b", "a b", "a~1", "x.lock", "a/", "@{u}"} {
		if err := ValidateBranch(bad); err == nil {
			t.Errorf("%q 应拒绝", bad)
		}
	}
}

func TestParseSkillMarkdown(t *testing.T) {
	ok := "---\nname: pdf-tools\ndescription: >\n  Extract text from PDFs.\n  Use when reading PDFs.\nmetadata:\n  owner: x\n---\n# body\n"
	meta, err := ParseSkillMarkdown([]byte(ok))
	if err != nil || meta.Name != "pdf-tools" || meta.Description != "Extract text from PDFs. Use when reading PDFs." {
		t.Fatalf("解析结果不对：%+v %v", meta, err)
	}
	quoted := "---\nname: \"x-1\"\ndescription: 'it''s fine' # comment\n---\n"
	if meta, err := ParseSkillMarkdown([]byte(quoted)); err != nil || meta.Description != "it's fine" {
		t.Fatalf("引号值解析不对：%+v %v", meta, err)
	}
	for _, bad := range []string{
		"no frontmatter",
		"---\nname: a\n",
		"---\nname: pdf-tools\n---\n",
		"---\ndescription: d\n---\n",
		"---\nname: Bad_Name\ndescription: d\n---\n",
	} {
		if _, err := ParseSkillMarkdown([]byte(bad)); err == nil {
			t.Errorf("应拒绝：%q", bad)
		}
	}
	if _, err := ParseSkillMarkdown([]byte("---\nname: pdf-tools\n---\n")); err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("缺少 description 时应指出字段：%v", err)
	}
}

type zipEntry struct {
	name string
	data string
	mode fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const goodMD = "---\nname: pdf-tools\ndescription: Extract text.\n---\n"

var lim = SkillArchiveLimits{MaxTotal: 1 << 20, MaxEntries: 10}

func TestExtractSkillZip(t *testing.T) {
	files, meta, err := ExtractSkillZip(makeZip(t,
		zipEntry{name: "pdf-tools/SKILL.md", data: goodMD},
		zipEntry{name: "pdf-tools/scripts/run.sh", data: "echo hi"},
		zipEntry{name: "__MACOSX/pdf-tools/._SKILL.md", data: "junk"},
		zipEntry{name: "pdf-tools/.DS_Store", data: "junk"},
	), lim)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "pdf-tools" || len(files) != 2 || files[0].Path != "SKILL.md" || files[1].Path != "scripts/run.sh" {
		t.Fatalf("解压结果不对：%+v %+v", meta, files)
	}
}

func TestExtractSkillZipRejects(t *testing.T) {
	cases := map[string][]byte{
		"缺少 SKILL.md": makeZip(t, zipEntry{name: "a.txt", data: "x"}),
		"越界路径":        makeZip(t, zipEntry{name: "SKILL.md", data: goodMD}, zipEntry{name: "../../etc/x", data: "x"}),
		"绝对路径":        makeZip(t, zipEntry{name: "SKILL.md", data: goodMD}, zipEntry{name: "/etc/x", data: "x"}),
		"符号链接":        makeZip(t, zipEntry{name: "SKILL.md", data: goodMD}, zipEntry{name: "link", data: "/etc/passwd", mode: fs.ModeSymlink | 0o777}),
		"非 zip":       []byte("not a zip"),
	}
	for name, data := range cases {
		if _, _, err := ExtractSkillZip(data, lim); err == nil {
			t.Errorf("%s：应拒绝", name)
		}
	}
}

func TestExtractSkillZipLimits(t *testing.T) {
	var entries []zipEntry
	entries = append(entries, zipEntry{name: "SKILL.md", data: goodMD})
	for i := range 10 {
		entries = append(entries, zipEntry{name: "f" + string(rune('a'+i)), data: "x"})
	}
	if _, _, err := ExtractSkillZip(makeZip(t, entries...), lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("条目数超限应返回 ErrTooLarge：%v", err)
	}
	// 高压缩比内容：以实际解压字节计数。
	bomb := makeZip(t, zipEntry{name: "SKILL.md", data: goodMD}, zipEntry{name: "big", data: strings.Repeat("0", 2<<20)})
	if _, _, err := ExtractSkillZip(bomb, lim); !errors.Is(err, ErrTooLarge) {
		t.Errorf("解压后超限应返回 ErrTooLarge：%v", err)
	}
}

func TestTextRules(t *testing.T) {
	r := TextRules{Extensions: []string{".md", ".txt"}, MaxBytes: 10}
	if !r.Editable("a/b.MD", []byte("hello")) {
		t.Error("md 文件应可编辑")
	}
	if r.Editable("a.png", []byte("x")) || r.Editable("a.md", []byte("01234567890")) || r.Editable("a.md", []byte{0xff, 0xfe}) {
		t.Error("扩展名不符、超长或非 UTF-8 时不可编辑")
	}
}
