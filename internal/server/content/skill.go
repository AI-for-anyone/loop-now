package content

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// SkillFileName 是 Skill 目录中必需的说明文件。
const SkillFileName = "SKILL.md"

// File 是一个相对路径及其内容。
type File struct {
	Path string
	Data []byte
}

// SkillMeta 是 SKILL.md frontmatter 中平台关心的字段。
type SkillMeta struct {
	Name        string
	Description string
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ErrTooLarge 表示超过大小或条目数上限。
var ErrTooLarge = errors.New("超过上限")

// ParseSkillMarkdown 解析 SKILL.md 开头的 YAML frontmatter，校验 name 与 description。
// 只解析顶层标量字段，足以覆盖 Agent Skills 规范中的 name 与 description；其他字段不做校验。
func ParseSkillMarkdown(md []byte) (SkillMeta, error) {
	text := strings.ReplaceAll(string(md), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\uFEFF")
	if !strings.HasPrefix(text, "---\n") {
		return SkillMeta{}, errors.New("SKILL.md 必须以 --- 开头的 frontmatter 开始")
	}
	body := text[len("---\n"):]
	end := strings.Index(body, "\n---")
	if end < 0 {
		return SkillMeta{}, errors.New("SKILL.md 的 frontmatter 没有结束标记 ---")
	}
	fields := parseTopLevelScalars(body[:end])
	meta := SkillMeta{Name: strings.TrimSpace(fields["name"]), Description: strings.TrimSpace(fields["description"])}
	var missing []string
	if meta.Name == "" {
		missing = append(missing, "name")
	}
	if meta.Description == "" {
		missing = append(missing, "description")
	}
	if len(missing) > 0 {
		return meta, fmt.Errorf("SKILL.md 的 frontmatter 缺少 %s", strings.Join(missing, "、"))
	}
	if len(meta.Name) > 64 || !skillNamePattern.MatchString(meta.Name) {
		return meta, errors.New("Skill 名称只能由小写字母、数字和单个连字符组成，最长 64 个字符")
	}
	if len(meta.Description) > 1024 {
		return meta, errors.New("Skill 描述不能超过 1024 个字符")
	}
	return meta, nil
}

var topKey = regexp.MustCompile(`^([A-Za-z0-9_-]+):(?:\s+(.*))?$`)

func parseTopLevelScalars(fm string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		m := topKey.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		key, val := m[1], strings.TrimSpace(m[2])
		// 收集后续的缩进行（块标量或嵌套结构）。
		var block []string
		for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t") || lines[i+1] == "") {
			i++
			block = append(block, lines[i])
		}
		switch {
		case strings.HasPrefix(val, "|"), strings.HasPrefix(val, ">"):
			out[key] = blockScalar(block, val[0] == '>')
		case val == "":
			// 嵌套结构或空值：平台不关心，跳过。
		default:
			out[key] = plainScalar(val)
		}
	}
	return out
}

func blockScalar(lines []string, folded bool) string {
	// 去掉公共缩进。
	indent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < 0 || n < indent {
			indent = n
		}
	}
	var parts []string
	for _, l := range lines {
		if len(l) >= indent && indent >= 0 {
			l = l[indent:]
		}
		parts = append(parts, l)
	}
	sep := "\n"
	if folded {
		sep = " "
	}
	return strings.TrimSpace(strings.Join(parts, sep))
}

func plainScalar(v string) string {
	switch {
	case strings.HasPrefix(v, `"`):
		// 找到未转义的结束引号，忽略其后的注释。
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				if s, err := strconv.Unquote(v[:i+1]); err == nil {
					return s
				}
				return v[1:i]
			}
		}
		return strings.Trim(v, `"`)
	case strings.HasPrefix(v, "'"):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			if v[i] == '\'' {
				if i+1 < len(v) && v[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				return b.String()
			}
			b.WriteByte(v[i])
		}
		return b.String()
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// SkillArchiveLimits 是解压 Skill 压缩包时的上限。
type SkillArchiveLimits struct {
	MaxTotal   int64 // 解压后总字节数
	MaxEntries int   // 文件条目数
}

// ExtractSkillZip 解压 Skill 目录的 zip 压缩包，返回按路径排序的文件。
// SKILL.md 可以在压缩包根目录，也可以在唯一的顶层目录中（此时去掉该目录）。
// 出现绝对路径、`..`、符号链接、重复路径，或超过上限时，整体拒绝。
// 忽略 macOS 生成的 __MACOSX/ 与 .DS_Store。
func ExtractSkillZip(data []byte, lim SkillArchiveLimits) ([]File, SkillMeta, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, SkillMeta{}, errors.New("不是合法的 zip 压缩包")
	}
	var files []File
	seen := map[string]bool{}
	var total int64
	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&fs.ModeType != 0 {
			return nil, SkillMeta{}, fmt.Errorf("压缩包中只能包含普通文件，不能有符号链接等特殊文件：%s", name)
		}
		clean, err := CleanRelPath(name)
		if err != nil {
			return nil, SkillMeta{}, fmt.Errorf("压缩包中的路径 %q 不合法：%v", name, err)
		}
		if seen[clean] {
			return nil, SkillMeta{}, fmt.Errorf("压缩包中有重复路径：%s", clean)
		}
		seen[clean] = true
		if len(files)+1 > lim.MaxEntries {
			return nil, SkillMeta{}, fmt.Errorf("%w：文件数超过 %d", ErrTooLarge, lim.MaxEntries)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, SkillMeta{}, fmt.Errorf("读取压缩包条目 %s 失败", clean)
		}
		// 以实际读出的字节计数，防止压缩炸弹谎报大小。
		b, err := io.ReadAll(io.LimitReader(rc, lim.MaxTotal-total+1))
		rc.Close()
		if err != nil {
			return nil, SkillMeta{}, fmt.Errorf("读取压缩包条目 %s 失败", clean)
		}
		total += int64(len(b))
		if total > lim.MaxTotal {
			return nil, SkillMeta{}, fmt.Errorf("%w：解压后超过 %d 字节", ErrTooLarge, lim.MaxTotal)
		}
		files = append(files, File{Path: clean, Data: b})
	}
	files = stripSingleTopDir(files)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	var md []byte
	for _, f := range files {
		if f.Path == SkillFileName {
			md = f.Data
		}
	}
	if md == nil {
		return nil, SkillMeta{}, errors.New("压缩包中没有 SKILL.md（需要在根目录或唯一的顶层目录中）")
	}
	meta, err := ParseSkillMarkdown(md)
	if err != nil {
		return nil, meta, err
	}
	return files, meta, nil
}

func stripSingleTopDir(files []File) []File {
	if len(files) == 0 {
		return files
	}
	for _, f := range files {
		if f.Path == SkillFileName {
			return files
		}
	}
	top, _, ok := strings.Cut(files[0].Path, "/")
	if !ok {
		return files
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, top+"/") {
			return files
		}
	}
	out := make([]File, len(files))
	for i, f := range files {
		out[i] = File{Path: strings.TrimPrefix(f.Path, top+"/"), Data: f.Data}
	}
	return out
}
