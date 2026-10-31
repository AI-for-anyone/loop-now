package plugins

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// 插件的 README（plugins/SPEC.md §2.3）：插件根目录里的 README.md 与按语言的 README.<语言>.md，
// 控制台在插件详情与安装预览里展示。

// MaxReadmeBytes 是返回的 README 的大小上限，超出的部分截掉。
const MaxReadmeBytes = 1 << 20

// Readme 是接口里的 PluginReadme；没有任何 README 时 File 与 Markdown 为空。
type Readme struct {
	File      *string `json:"file"`
	Markdown  *string `json:"markdown"`
	Truncated bool    `json:"truncated"`
}

// Readme 读已安装插件当前版本的 README；locale 为空表示没有指定语言。
func (s *Service) Readme(ctx context.Context, id, locale string) (Readme, error) {
	p, err := s.Store.GetPlugin(ctx, id)
	if err != nil {
		return Readme{}, err
	}
	return ReadReadme(p.InstallDir, locale)
}

// PreviewReadme 读预览里插件的 README；预览过期、已用于安装或不存在时返回 404。
func (s *Service) PreviewReadme(id uuid.UUID, locale string) (Readme, error) {
	s.expirePreviews()
	s.mu.Lock()
	pv := s.previews[id]
	s.mu.Unlock()
	if pv == nil {
		return Readme{}, apperr.NotFound("预览")
	}
	r, err := ReadReadme(pv.dir, locale)
	if errors.Is(err, fs.ErrNotExist) {
		// 读的同时预览被安装或过期，暂存目录已经删掉。
		return Readme{}, apperr.NotFound("预览")
	}
	return r, err
}

// ReadReadme 在插件根目录 dir 的直接子文件里按 locale 选出 README 并读出。
// 软链接解析后不在 dir 里的、解析不了的、不是普通文件的都当作没有。
func ReadReadme(dir, locale string) (Readme, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Readme{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Readme{}, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, name := range readmeOrder(names, locale) {
		f, err := openInside(root, name)
		if err != nil {
			return Readme{}, err
		}
		if f == nil {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, MaxReadmeBytes+1))
		f.Close()
		if err != nil {
			return Readme{}, err
		}
		truncated := len(b) > MaxReadmeBytes
		if truncated {
			b = cutRunes(b[:MaxReadmeBytes])
		}
		markdown := string(b)
		return Readme{File: &name, Markdown: &markdown, Truncated: truncated}, nil
	}
	return Readme{}, nil
}

// readmeOrder 给出 README 的尝试顺序：给了 locale 时 README.<locale>.md、README.<主语言>.md、README.md，
// 没给时 README.md；之后是其余的 README.<语言>.md，按语言标签排序。基名不区分大小写；
// 同一语言有几种大小写写法的文件时按文件名排序。
func readmeOrder(names []string, locale string) []string {
	byTag := map[string][]string{} // 小写的语言标签 → 文件名；"" 是 README.md
	for _, name := range names {
		lower := strings.ToLower(name)
		if len(lower) < len("readme.md") || !strings.HasPrefix(lower, "readme") || !strings.HasSuffix(lower, ".md") {
			continue
		}
		tag := lower[len("readme") : len(lower)-len(".md")]
		if tag != "" {
			if tag[0] != '.' || !pluginmanifest.ValidLanguageTag(tag[1:]) {
				continue
			}
			tag = tag[1:]
		}
		byTag[tag] = append(byTag[tag], name)
	}
	var first []string
	if locale != "" {
		l := strings.ToLower(locale)
		first = append(first, l)
		if primary, _, ok := strings.Cut(l, "-"); ok {
			first = append(first, primary)
		}
	}
	first = append(first, "")
	var out []string
	for _, tag := range append(first, slices.Sorted(maps.Keys(byTag))...) {
		out = append(out, slices.Sorted(slices.Values(byTag[tag]))...)
		delete(byTag, tag)
	}
	return out
}

// openInside 打开 root 的直接子文件 name；解析后不在 root 里或不是普通文件时返回 nil。
func openInside(root, name string) (*os.File, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return nil, nil
	}
	if rel, err := filepath.Rel(root, real); err != nil || !filepath.IsLocal(rel) {
		return nil, nil
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, err
	}
	return f, nil
}

// cutRunes 去掉截断时留在末尾的半个 UTF-8 字符。
func cutRunes(b []byte) []byte {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				return b[:i]
			}
			break
		}
	}
	return b
}
