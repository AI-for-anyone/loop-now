package plugins_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"loop-now/internal/server/plugins"
)

// readmeDir 建一个插件目录，files 是文件名到内容。
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

// readme 读出 dir 的 README，返回选中的文件名与内容；没有时都为空。
func readme(t *testing.T, dir, locale string) (string, string) {
	t.Helper()
	r, err := plugins.ReadReadme(dir, locale)
	if err != nil {
		t.Fatal(err)
	}
	if (r.File == nil) != (r.Markdown == nil) || r.Truncated {
		t.Fatalf("%s %q：%+v", dir, locale, r)
	}
	if r.File == nil {
		return "", ""
	}
	return *r.File, *r.Markdown
}

// 给了语言时依次找 README.<locale>.md、README.<主语言>.md、README.md，再取语言标签排序最前的；没给时 README.md 优先。
func TestReadmeOrder(t *testing.T) {
	files := map[string]string{"README.zh-CN.md": "简体", "README.zh.md": "中文", "README.md": "默认", "README.fr.md": "français", "README.en-US.md": "English"}
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
			t.Fatalf("locale %q：%s %q，期望 %s", s.locale, got, content, s.want)
		}
		if s.remove != "" {
			if err := os.Remove(filepath.Join(dir, s.remove)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 回落只到主语言：给 en-US 时不选同主语言的 README.en-GB.md，而是 README.md。
	dir = readmeDir(t, map[string]string{"README.en-GB.md": "GB", "README.md": "默认"})
	if got, _ := readme(t, dir, "en-US"); got != "README.md" {
		t.Fatalf("en-US 不应取 README.en-GB.md：%s", got)
	}
}

// 基名不区分大小写，返回磁盘上的文件名；同一语言有几种写法时按文件名排序。
func TestReadmeCaseInsensitive(t *testing.T) {
	dir := readmeDir(t, map[string]string{"readme.ZH-cn.MD": "简体", "Readme.md": "默认"})
	if got, content := readme(t, dir, "zh-CN"); got != "readme.ZH-cn.MD" || content != "简体" {
		t.Fatalf("zh-CN：%s %q", got, content)
	}
	if got, _ := readme(t, dir, "ZH-cn"); got != "readme.ZH-cn.MD" {
		t.Fatalf("ZH-cn：%s", got)
	}
	if got, _ := readme(t, dir, ""); got != "Readme.md" {
		t.Fatalf("没给语言：%s", got)
	}
	both := readmeDir(t, map[string]string{"README.md": "大写", "readme.md": "小写"})
	if entries, _ := os.ReadDir(both); len(entries) == 2 { // 不区分大小写的文件系统上两者是同一个文件
		if got, _ := readme(t, both, ""); got != "README.md" {
			t.Fatalf("同一语言的几种写法：%s", got)
		}
	}
}

// 不是 README 的文件、子目录里的 README、名叫 README.md 的目录都不算。
func TestReadmeAbsent(t *testing.T) {
	dir := readmeDir(t, map[string]string{"README": "x", "README.txt": "x", "READMEX.md": "x", "README..md": "x", "README.zh_CN.md": "x",
		"docs/README.md": "x", "README.md/inner": "x", "package.json": "{}"})
	if got, _ := readme(t, dir, "zh-CN"); got != "" {
		t.Fatalf("不应选出 README：%s", got)
	}
	if got, _ := readme(t, t.TempDir(), ""); got != "" {
		t.Fatalf("空目录：%s", got)
	}
}

// 超过 1 MiB 时只返回前 1 MiB，不在一个字符中间截断；正好 1 MiB 不算截断。
func TestReadmeTruncated(t *testing.T) {
	exact := strings.Repeat("a", plugins.MaxReadmeBytes)
	dir := readmeDir(t, map[string]string{"README.md": exact})
	if r, err := plugins.ReadReadme(dir, ""); err != nil || r.Truncated || *r.Markdown != exact {
		t.Fatalf("正好 1 MiB：%v %v", r.Truncated, err)
	}
	// "中" 占 3 字节，从 1 MiB 前 1 字节开始，跨过上限。
	long := strings.Repeat("a", plugins.MaxReadmeBytes-1) + "中" + "尾"
	dir = readmeDir(t, map[string]string{"README.md": long})
	r, err := plugins.ReadReadme(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || *r.Markdown != strings.Repeat("a", plugins.MaxReadmeBytes-1) || !utf8.ValidString(*r.Markdown) {
		t.Fatalf("截断：%v %d", r.Truncated, len(*r.Markdown))
	}
}

// 软链接解析后须仍在插件目录内：指到目录外的当作没有，接着找下一个；指到目录内的照常读；插件目录本身经软链接给出也可以。
func TestReadmeSymlinks(t *testing.T) {
	outside := readmeDir(t, map[string]string{"secret.md": "目录外的内容"})
	dir := readmeDir(t, map[string]string{"README.en.md": "English", "docs/guide.md": "指南"})
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	link(filepath.Join(outside, "secret.md"), "README.md")
	link("../"+filepath.Base(outside)+"/secret.md", "README.zh.md")
	if got, content := readme(t, dir, "zh-CN"); got != "README.en.md" || content != "English" {
		t.Fatalf("目录外的软链接应当作没有：%s %q", got, content)
	}
	link("docs/guide.md", "README.zh-CN.md")
	if got, content := readme(t, dir, "zh-CN"); got != "README.zh-CN.md" || content != "指南" {
		t.Fatalf("目录内的软链接：%s %q", got, content)
	}
	link("missing.md", "README.de.md")
	link("docs", "README.fr.md")
	if got, _ := readme(t, dir, "fr"); got != "README.en.md" {
		t.Fatalf("悬空或指到目录的软链接应当作没有：%s", got)
	}
	via := filepath.Join(t.TempDir(), "plugin")
	if err := os.Symlink(dir, via); err != nil {
		t.Fatal(err)
	}
	if got, _ := readme(t, via, "zh-CN"); got != "README.zh-CN.md" {
		t.Fatalf("经软链接给出的插件目录：%s", got)
	}
}
