package plugins_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"loop-now/internal/server/plugins"
)

// readmeDir creates a plugin directory; files maps file names to contents.
func readmeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// readme reads the README of dir and returns the chosen file name and its content; both are empty when there is
// none.
func readme(t *testing.T, dir, locale string) (string, string) {
	t.Helper()
	r, err := plugins.ReadReadme(dir, locale)
	if err != nil {
		t.Fatal(err)
	}
	if (r.File == nil) != (r.Markdown == nil) || r.Truncated {
		t.Fatalf("%s %q: %+v", dir, locale, r)
	}
	if r.File == nil {
		return "", ""
	}
	return *r.File, *r.Markdown
}

// With a language it looks for README.<locale>.md, README.<primary language>.md, README.md in turn, then the one whose
// language tag sorts first; without a language README.md comes first.
func TestReadmeOrder(t *testing.T) {
	files := map[string]string{"README.zh-CN.md": "Simplified Chinese", "README.zh.md": "Chinese", "README.md": "Default", "README.fr.md": "français", "README.en-US.md": "English"}
	dir := readmeDir(t, files)
	steps := []struct{ locale, want, remove string }{
		{"zh-CN", "README.zh-CN.md", "README.zh-CN.md"},
		{"zh-CN", "README.zh.md", "README.zh.md"},
		{"zh-CN", "README.md", ""},
		{"", "README.md", "README.md"},
		{"zh-CN", "README.en-US.md", ""},
		{"", "README.en-US.md", ""},
		{"en", "README.en-US.md", "README.en-US.md"},
		{"de-DE", "README.fr.md", ""},
	}
	for _, s := range steps {
		if got, content := readme(t, dir, s.locale); got != s.want || content != files[s.want] {
			t.Fatalf("locale %q: got %s %q, want %s", s.locale, got, content, s.want)
		}
		if s.remove != "" {
			if err := os.Remove(filepath.Join(dir, s.remove)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Falling back only goes as far as the primary language: for en-US it does not pick README.en-GB.md, which has
	// the same primary language, but README.md.
	dir = readmeDir(t, map[string]string{"README.en-GB.md": "GB", "README.md": "Default"})
	if got, _ := readme(t, dir, "en-US"); got != "README.md" {
		t.Fatalf("en-US should not pick README.en-GB.md: %s", got)
	}
}

// Base names are case-insensitive and the file name on disk is returned; when one language has several spellings,
// they are sorted by file name.
func TestReadmeCaseInsensitive(t *testing.T) {
	dir := readmeDir(t, map[string]string{"readme.ZH-cn.MD": "Simplified Chinese", "Readme.md": "Default"})
	if got, content := readme(t, dir, "zh-CN"); got != "readme.ZH-cn.MD" || content != "Simplified Chinese" {
		t.Fatalf("zh-CN: %s %q", got, content)
	}
	if got, _ := readme(t, dir, "ZH-cn"); got != "readme.ZH-cn.MD" {
		t.Fatalf("ZH-cn: %s", got)
	}
	if got, _ := readme(t, dir, ""); got != "Readme.md" {
		t.Fatalf("no language given: %s", got)
	}
	both := readmeDir(t, map[string]string{"README.md": "Upper case", "readme.md": "lower case"})
	if entries, _ := os.ReadDir(both); len(entries) == 2 { // on a case-insensitive file system both are the same file
		if got, _ := readme(t, both, ""); got != "README.md" {
			t.Fatalf("several spellings of one language: %s", got)
		}
	}
}

// Files that are not READMEs, READMEs in subdirectories, and a directory named README.md do not count.
func TestReadmeAbsent(t *testing.T) {
	dir := readmeDir(t, map[string]string{"README": "x", "README.txt": "x", "READMEX.md": "x", "README..md": "x", "README.zh_CN.md": "x",
		"docs/README.md": "x", "README.md/inner": "x", "package.json": "{}"})
	if got, _ := readme(t, dir, "zh-CN"); got != "" {
		t.Fatalf("should not pick a README: %s", got)
	}
	if got, _ := readme(t, t.TempDir(), ""); got != "" {
		t.Fatalf("empty directory: %s", got)
	}
}

// Beyond 1 MiB only the first 1 MiB is returned, without cutting through a character; exactly 1 MiB does not count as
// truncated.
func TestReadmeTruncated(t *testing.T) {
	exact := strings.Repeat("a", plugins.MaxReadmeBytes)
	dir := readmeDir(t, map[string]string{"README.md": exact})
	if r, err := plugins.ReadReadme(dir, ""); err != nil || r.Truncated || *r.Markdown != exact {
		t.Fatalf("exactly 1 MiB: %v %v", r.Truncated, err)
	}
	// "中" (one CJK character) takes 3 bytes; it starts 1 byte before 1 MiB and crosses the limit.
	long := strings.Repeat("a", plugins.MaxReadmeBytes-1) + "中" + "tail" // check-english: zh (a 3-byte character crossing the size limit)
	dir = readmeDir(t, map[string]string{"README.md": long})
	r, err := plugins.ReadReadme(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || *r.Markdown != strings.Repeat("a", plugins.MaxReadmeBytes-1) || !utf8.ValidString(*r.Markdown) {
		t.Fatalf("truncated: %v %d", r.Truncated, len(*r.Markdown))
	}
}

// A symlink must still be inside the plugin directory once resolved: one pointing outside counts as missing and the
// next candidate is tried; one pointing inside is read as usual; the plugin directory itself may also be given via a
// symlink.
func TestReadmeSymlinks(t *testing.T) {
	outside := readmeDir(t, map[string]string{"secret.md": "content outside the directory"})
	dir := readmeDir(t, map[string]string{"README.en.md": "English", "docs/guide.md": "Guide"})
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(outside, "secret.md"), "README.md")
	link("../"+filepath.Base(outside)+"/secret.md", "README.zh.md")
	if got, content := readme(t, dir, "zh-CN"); got != "README.en.md" || content != "English" {
		t.Fatalf("a symlink pointing outside should count as missing: %s %q", got, content)
	}
	link("docs/guide.md", "README.zh-CN.md")
	if got, content := readme(t, dir, "zh-CN"); got != "README.zh-CN.md" || content != "Guide" {
		t.Fatalf("symlink inside the directory: %s %q", got, content)
	}
	link("missing.md", "README.de.md")
	link("docs", "README.fr.md")
	if got, _ := readme(t, dir, "fr"); got != "README.en.md" {
		t.Fatalf("a dangling symlink or one pointing to a directory should count as missing: %s", got)
	}
	via := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(dir, via); err != nil {
		t.Fatal(err)
	}
	if got, _ := readme(t, via, "zh-CN"); got != "README.zh-CN.md" {
		t.Fatalf("plugin directory given via a symlink: %s", got)
	}
}
