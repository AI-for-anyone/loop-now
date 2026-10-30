// Package clitemplate 管理平台内置的 CLI 模板：凭证录入项、凭证渲染与固定版本安装。
package clitemplate

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/content"
)

//go:embed templates/*.json
var builtin embed.FS

// Field 是凭证录入项。
type Field struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"` // string / secret / enum
	Options   []string `json:"options,omitempty"`
	Default   string   `json:"default,omitempty"`
	Required  bool     `json:"required"`
	Multiline bool     `json:"multiline,omitempty"`
}

// Warning 是展示给控制台的风险提示。
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Asset 是某个平台的安装包。
type Asset struct {
	URL          string  `json:"url"`
	SHA256       string  `json:"sha256"`
	Format       string  `json:"format"` // zip / tar.gz
	Dependencies []Asset `json:"dependencies,omitempty"`
	Layout       string  `json:"layout,omitempty"` // tree preserves the complete official runtime
	Path         string  `json:"path"`             // 安装包内二进制文件的路径
}

// Credential 描述凭证怎样生成。
type Credential struct {
	Env   map[string]string `json:"env,omitempty"`
	Files []FileSpec        `json:"files,omitempty"`
	// Renderer 指定由代码实现的渲染器（格式无法用简单模板表达时使用）。
	Renderer string `json:"renderer,omitempty"`
	// PendingReason 非空时，该模板暂不能录入凭证。
	PendingReason string `json:"pendingReason,omitempty"`
	// CleanupPaths 是每次启动前要从独立 HOME 中删除的凭证文件（相对 HOME）。
	CleanupPaths []string `json:"cleanupPaths,omitempty"`
	// FormatVersion 是凭证格式的版本；格式变化时递增，已录入的旧格式凭证需要重新录入。默认 "1"。
	FormatVersion string `json:"formatVersion,omitempty"`
}

// CredentialFormat 返回凭证格式版本。
func (t *Template) CredentialFormat() string {
	if t.Credential.FormatVersion == "" {
		return "1"
	}
	return t.Credential.FormatVersion
}

// FileSpec 是一个由模板生成的凭证文件。
type FileSpec struct {
	Path    string `json:"path"` // 相对执行 HOME
	Content string `json:"content"`
}

// Template 是一个 CLI 模板。
type Template struct {
	Name       string               `json:"name"`
	Version    string               `json:"version"`
	Injection  string               `json:"injection"` // env / files
	Fields     []Field              `json:"fields"`
	Credential Credential           `json:"credential"`
	Binary     string               `json:"binary"`
	Install    map[string]Asset     `json:"install"`
	Warnings   map[string][]Warning `json:"warnings,omitempty"` // 按 GOOS
	// Env 是执行时注入的固定环境变量（不是凭证，例如关闭后台联网的开关）。
	// 只要该 CLI 在 Agent 的可用列表中就注入，不依赖是否录入了凭证；按当前模板生效，不随凭证保存。
	Env map[string]string `json:"env,omitempty"`
	// ReservedEnv 与 ReservedEnvPrefixes 是该 CLI 会读取、能影响凭证、身份、主机或配置位置的环境变量（按源码核实）。
	// 模型账号凭证不能使用这些名字；模板自己注入的变量名（Env 与 Credential.Env）也自动视为保留。
	ReservedEnv         []string `json:"reservedEnv,omitempty"`
	ReservedEnvPrefixes []string `json:"reservedEnvPrefixes,omitempty"`
}

// Platform 返回当前平台的键，例如 darwin/arm64。
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// PlatformWarnings 返回当前平台的风险提示。
func (t *Template) PlatformWarnings() []Warning {
	if w := t.Warnings[runtime.GOOS]; w != nil {
		return w
	}
	return []Warning{}
}

// Asset 返回当前平台的安装包。
func (t *Template) Asset() (Asset, bool) {
	a, ok := t.Install[Platform()]
	return a, ok
}

// CredentialFilePaths 返回该模板可能写入独立 HOME 的全部凭证文件，用于启动前清理。
func (t *Template) CredentialFilePaths() []string {
	var out []string
	for _, f := range t.Credential.Files {
		out = append(out, f.Path)
	}
	out = append(out, t.Credential.CleanupPaths...)
	return out
}

// Catalog 是全部模板。
type Catalog struct {
	byName map[string]*Template
}

// Load 加载内置模板；dir 非空时再加载该目录下的同格式模板，按名称覆盖或补充。
func Load(dir string) (*Catalog, error) {
	c := &Catalog{byName: map[string]*Template{}}
	if err := c.loadFS(builtin, "templates"); err != nil {
		return nil, err
	}
	if dir != "" {
		if err := c.loadFS(os.DirFS(dir), "."); err != nil {
			return nil, fmt.Errorf("加载 %s 中的 CLI 模板失败：%w", dir, err)
		}
	}
	return c, nil
}

func (c *Catalog) loadFS(fsys fs.FS, root string) error {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := fs.ReadFile(fsys, filepath.ToSlash(filepath.Join(root, e.Name())))
		if err != nil {
			return err
		}
		var t Template
		if err := json.Unmarshal(b, &t); err != nil {
			return fmt.Errorf("%s：%w", e.Name(), err)
		}
		if err := t.validate(); err != nil {
			return fmt.Errorf("%s：%w", e.Name(), err)
		}
		c.byName[t.Name] = &t
	}
	return nil
}

var (
	cliNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	sha256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	placeholder    = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)
)

func (t *Template) validate() error {
	if !cliNamePattern.MatchString(t.Name) {
		return errors.New("模板名称不合法")
	}
	if t.Version == "" || t.Binary == "" {
		return errors.New("缺少 version 或 binary")
	}
	if t.Injection != "env" && t.Injection != "files" {
		return errors.New("injection 只能是 env 或 files")
	}
	names := map[string]bool{}
	for _, f := range t.Fields {
		if f.Name == "" || f.Label == "" || names[f.Name] {
			return fmt.Errorf("录入项 %q 不合法或重复", f.Name)
		}
		names[f.Name] = true
		switch f.Type {
		case "string", "secret":
		case "enum":
			if len(f.Options) == 0 || (f.Default != "" && !slices.Contains(f.Options, f.Default)) {
				return fmt.Errorf("枚举录入项 %s 的可选值或默认值不合法", f.Name)
			}
		default:
			return fmt.Errorf("录入项 %s 的类型 %q 不支持", f.Name, f.Type)
		}
	}
	for _, n := range append(slices.Clone(t.ReservedEnv), t.ReservedEnvPrefixes...) {
		if !envNamePattern.MatchString(n) {
			return fmt.Errorf("保留的环境变量名或前缀 %q 不合法", n)
		}
	}
	for k, v := range t.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("环境变量名 %q 不合法", k)
		}
		if placeholder.MatchString(v) {
			return fmt.Errorf("固定环境变量 %s 不能引用录入项", k)
		}
	}
	for k, v := range t.Credential.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("环境变量名 %q 不合法", k)
		}
		if err := checkPlaceholders(v, names); err != nil {
			return err
		}
	}
	for _, f := range append(t.Credential.Files, cleanupSpecs(t.Credential.CleanupPaths)...) {
		if _, err := content.CleanRelPath(f.Path); err != nil {
			return fmt.Errorf("凭证文件路径 %q 不合法：%v", f.Path, err)
		}
		if err := checkPlaceholders(f.Content, names); err != nil {
			return err
		}
	}
	if t.Credential.Renderer != "" && t.Credential.Renderer != "lark-cli" && t.Credential.Renderer != "psql" && t.Credential.Renderer != "mysql" && t.Credential.Renderer != "gcloud" {
		return fmt.Errorf("不认识的渲染器 %q", t.Credential.Renderer)
	}
	for plat, a := range t.Install {
		if a.URL == "" || !sha256Pattern.MatchString(a.SHA256) || (a.Format != "zip" && a.Format != "tar.gz" && a.Format != "tar.xz") || a.Path == "" {
			return fmt.Errorf("平台 %s 的安装包定义不完整", plat)
		}
	}
	return nil
}

func cleanupSpecs(paths []string) []FileSpec {
	out := make([]FileSpec, len(paths))
	for i, p := range paths {
		out[i] = FileSpec{Path: p}
	}
	return out
}

func checkPlaceholders(s string, names map[string]bool) error {
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		if !names[m[1]] {
			return fmt.Errorf("占位符 {{%s}} 没有对应的录入项", m[1])
		}
	}
	return nil
}

// Get 按名称查找模板。
// ReservedEnvBy 返回把 name 列为保留名的模板名称；没有模板保留它时返回 false。
// 保留名包括各模板声明的精确名与前缀，以及模板自己注入的变量名。
func (c *Catalog) ReservedEnvBy(name string) (string, bool) {
	for _, t := range c.All() {
		if t.reservesEnv(name) {
			return t.Name, true
		}
	}
	return "", false
}

func (t *Template) reservesEnv(name string) bool {
	if slices.Contains(t.ReservedEnv, name) {
		return true
	}
	if _, ok := t.Env[name]; ok {
		return true
	}
	if _, ok := t.Credential.Env[name]; ok {
		return true
	}
	return slices.ContainsFunc(t.ReservedEnvPrefixes, func(p string) bool { return strings.HasPrefix(name, p) })
}

func (c *Catalog) Get(name string) (*Template, bool) {
	t, ok := c.byName[name]
	return t, ok
}

// All 按名称排序返回全部模板。
func (c *Catalog) All() []*Template {
	out := make([]*Template, 0, len(c.byName))
	for _, t := range c.byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllCredentialFilePaths 返回全部模板可能写入独立 HOME 的凭证文件，去重排序。
func (c *Catalog) AllCredentialFilePaths() []string {
	set := map[string]bool{}
	for _, t := range c.byName {
		for _, p := range t.CredentialFilePaths() {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Payload 是渲染后的凭证内容：入库保存，执行时写入独立 HOME 与进程环境。
type Payload struct {
	Env   map[string]string `json:"env"`
	Files []PayloadFile     `json:"files"`
}

// PayloadFile 是一个凭证文件。
type PayloadFile struct {
	Path    string `json:"path"` // 相对执行 HOME
	Content []byte `json:"content"`
}

// EnvNames 返回排序后的环境变量名。
func (p Payload) EnvNames() []string {
	out := make([]string, 0, len(p.Env))
	for k := range p.Env {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FilePaths 返回排序后的文件路径。
func (p Payload) FilePaths() []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// Render 按模板校验录入项并生成凭证内容。返回的 fieldNames 是实际填写的录入项名称。
func (t *Template) Render(input map[string]string) (Payload, []string, error) {
	if t.Credential.PendingReason != "" {
		return Payload{}, nil, apperr.Invalid("cli", t.Credential.PendingReason)
	}
	values := map[string]string{}
	var fieldErrs []apperr.FieldError
	for k := range input {
		if !slices.ContainsFunc(t.Fields, func(f Field) bool { return f.Name == k }) {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+k, "模板中没有这个录入项"))
		}
	}
	for _, f := range t.Fields {
		v := strings.TrimSpace(input[f.Name])
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+f.Name, f.Label+"必填"))
			continue
		}
		if f.Type == "enum" && v != "" && !slices.Contains(f.Options, v) {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+f.Name, f.Label+"只能是 "+strings.Join(f.Options, "、")))
			continue
		}
		// Database passwords are opaque: do not trim meaningful whitespace.
		if (t.Name == "psql" || t.Name == "mysql") && f.Type == "secret" && input[f.Name] != "" {
			v = input[f.Name]
		}
		values[f.Name] = v
	}
	if len(fieldErrs) > 0 {
		sort.Slice(fieldErrs, func(i, j int) bool { return fieldErrs[i].Field < fieldErrs[j].Field })
		return Payload{}, nil, apperr.Validation(fieldErrs...)
	}
	fill := func(s string) string {
		return placeholder.ReplaceAllStringFunc(s, func(m string) string { return values[placeholder.FindStringSubmatch(m)[1]] })
	}
	p := Payload{Env: map[string]string{}, Files: []PayloadFile{}}
	for k, v := range t.Credential.Env {
		p.Env[k] = fill(v)
	}
	for _, f := range t.Credential.Files {
		p.Files = append(p.Files, PayloadFile{Path: f.Path, Content: []byte(fill(f.Content))})
	}
	if t.Credential.Renderer == "lark-cli" {
		files, err := renderLarkCLI(values, runtime.GOOS, rand.Reader)
		if err != nil {
			return Payload{}, nil, err
		}
		p.Files = append(p.Files, files...)
	}
	if t.Credential.Renderer == "psql" || t.Credential.Renderer == "mysql" {
		files, err := renderDatabase(t.Name, values)
		if err != nil {
			return Payload{}, nil, err
		}
		p.Files = append(p.Files, files...)
	}
	if t.Credential.Renderer == "gcloud" {
		files, err := renderGoogleSA(values)
		if err != nil {
			return Payload{}, nil, err
		}
		p.Files = append(p.Files, files...)
	}
	var names []string
	for _, f := range t.Fields {
		if values[f.Name] != "" {
			names = append(names, f.Name)
		}
	}
	return p, names, nil
}
