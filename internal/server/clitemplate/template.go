// Package clitemplate manages the platform's built-in CLI templates: credential input fields, credential rendering
// and pinned-version installation.
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

// Field is a credential input field.
type Field struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"` // string / secret / enum
	Options   []string `json:"options,omitempty"`
	Default   string   `json:"default,omitempty"`
	Required  bool     `json:"required"`
	Multiline bool     `json:"multiline,omitempty"`
}

// Warning is a risk notice shown in the console.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Asset is the install package for one platform.
type Asset struct {
	URL          string  `json:"url"`
	SHA256       string  `json:"sha256"`
	Format       string  `json:"format"` // zip / tar.gz
	Dependencies []Asset `json:"dependencies,omitempty"`
	Layout       string  `json:"layout,omitempty"` // tree preserves the complete official runtime
	Path         string  `json:"path"`             // path of the binary inside the install package
}

// Credential describes how the credential is produced.
type Credential struct {
	Env   map[string]string `json:"env,omitempty"`
	Files []FileSpec        `json:"files,omitempty"`
	// Renderer names a renderer implemented in code (used when the format cannot be expressed as a simple template).
	Renderer string `json:"renderer,omitempty"`
	// PendingReason, when non-empty, means credentials cannot be entered for this template yet.
	PendingReason string `json:"pendingReason,omitempty"`
	// CleanupPaths are the credential files to delete from the isolated HOME before every start (relative to HOME).
	CleanupPaths []string `json:"cleanupPaths,omitempty"`
	// FormatVersion is the version of the credential format; it is bumped when the format changes, and credentials
	// already entered in the old format must be entered again. Default "1".
	FormatVersion string `json:"formatVersion,omitempty"`
}

// CredentialFormat returns the credential format version.
func (t *Template) CredentialFormat() string {
	if t.Credential.FormatVersion == "" {
		return "1"
	}
	return t.Credential.FormatVersion
}

// FileSpec is a credential file generated from the template.
type FileSpec struct {
	Path    string `json:"path"` // relative to the run's HOME
	Content string `json:"content"`
}

// Template is a CLI template.
type Template struct {
	Name       string               `json:"name"`
	Version    string               `json:"version"`
	Injection  string               `json:"injection"` // env / files
	Fields     []Field              `json:"fields"`
	Credential Credential           `json:"credential"`
	Binary     string               `json:"binary"`
	Install    map[string]Asset     `json:"install"`
	Warnings   map[string][]Warning `json:"warnings,omitempty"` // keyed by GOOS
	// Env are fixed environment variables injected at run time (not credentials; for example a switch that turns off
	// background network access).
	// They are injected whenever the CLI is in the Agent's available list, regardless of whether a credential was
	// entered; they follow the current template and are not saved with the credential.
	Env map[string]string `json:"env,omitempty"`
	// ReservedEnv and ReservedEnvPrefixes are the environment variables this CLI reads that can affect its
	// credentials, identity, host or config location (verified against its source code).
	// Model account credentials cannot use these names; variable names the template injects itself (Env and
	// Credential.Env) are automatically reserved too.
	ReservedEnv         []string `json:"reservedEnv,omitempty"`
	ReservedEnvPrefixes []string `json:"reservedEnvPrefixes,omitempty"`
}

// Platform returns the key of the current platform, for example darwin/arm64.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// PlatformWarnings returns the risk notices for the current platform.
func (t *Template) PlatformWarnings() []Warning {
	if w := t.Warnings[runtime.GOOS]; w != nil {
		return w
	}
	return []Warning{}
}

// Asset returns the install package for the current platform.
func (t *Template) Asset() (Asset, bool) {
	a, ok := t.Install[Platform()]
	return a, ok
}

// CredentialFilePaths returns every credential file this template may write into the isolated HOME, for cleanup
// before a start.
func (t *Template) CredentialFilePaths() []string {
	var out []string
	for _, f := range t.Credential.Files {
		out = append(out, f.Path)
	}
	out = append(out, t.Credential.CleanupPaths...)
	return out
}

// Catalog holds all templates.
type Catalog struct {
	byName map[string]*Template
}

// Load loads the built-in templates; when dir is non-empty it also loads templates of the same format from that
// directory, which override or add to them by name.
func Load(dir string) (*Catalog, error) {
	c := &Catalog{byName: map[string]*Template{}}
	if err := c.loadFS(builtin, "templates"); err != nil {
		return nil, err
	}
	if dir != "" {
		if err := c.loadFS(os.DirFS(dir), "."); err != nil {
			return nil, fmt.Errorf("load CLI templates from %s: %w", dir, err)
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
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if err := t.validate(); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
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
		return errors.New("invalid template name")
	}
	if t.Version == "" || t.Binary == "" {
		return errors.New("version or binary is missing")
	}
	if t.Injection != "env" && t.Injection != "files" {
		return errors.New("injection must be env or files")
	}
	names := map[string]bool{}
	for _, f := range t.Fields {
		if f.Name == "" || f.Label == "" || names[f.Name] {
			return fmt.Errorf("field %q is invalid or duplicated", f.Name)
		}
		names[f.Name] = true
		switch f.Type {
		case "string", "secret":
		case "enum":
			if len(f.Options) == 0 || (f.Default != "" && !slices.Contains(f.Options, f.Default)) {
				return fmt.Errorf("enum field %s has invalid options or an invalid default", f.Name)
			}
		default:
			return fmt.Errorf("field %s has unsupported type %q", f.Name, f.Type)
		}
	}
	for _, n := range append(slices.Clone(t.ReservedEnv), t.ReservedEnvPrefixes...) {
		if !envNamePattern.MatchString(n) {
			return fmt.Errorf("invalid reserved environment variable name or prefix %q", n)
		}
	}
	for k, v := range t.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("invalid environment variable name %q", k)
		}
		if placeholder.MatchString(v) {
			return fmt.Errorf("fixed environment variable %s must not reference a field", k)
		}
	}
	for k, v := range t.Credential.Env {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("invalid environment variable name %q", k)
		}
		if err := checkPlaceholders(v, names); err != nil {
			return err
		}
	}
	for _, f := range append(t.Credential.Files, cleanupSpecs(t.Credential.CleanupPaths)...) {
		if _, err := content.CleanRelPath(f.Path); err != nil {
			return fmt.Errorf("invalid credential file path %q: %v", f.Path, err)
		}
		if err := checkPlaceholders(f.Content, names); err != nil {
			return err
		}
	}
	if t.Credential.Renderer != "" && t.Credential.Renderer != "lark-cli" && t.Credential.Renderer != "psql" && t.Credential.Renderer != "mysql" && t.Credential.Renderer != "gcloud" {
		return fmt.Errorf("unknown renderer %q", t.Credential.Renderer)
	}
	for plat, a := range t.Install {
		if a.URL == "" || !sha256Pattern.MatchString(a.SHA256) || (a.Format != "zip" && a.Format != "tar.gz" && a.Format != "tar.xz") || a.Path == "" {
			return fmt.Errorf("install package definition for platform %s is incomplete", plat)
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
			return fmt.Errorf("placeholder {{%s}} has no matching field", m[1])
		}
	}
	return nil
}

// ReservedEnvBy returns the name of the template that lists name as reserved; it returns false when no template
// reserves it.
// Reserved names include the exact names and prefixes each template declares, plus the variable names the template
// injects itself.
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

// Get looks up a template by name.
func (c *Catalog) Get(name string) (*Template, bool) {
	t, ok := c.byName[name]
	return t, ok
}

// All returns all templates sorted by name.
func (c *Catalog) All() []*Template {
	out := make([]*Template, 0, len(c.byName))
	for _, t := range c.byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllCredentialFilePaths returns the credential files any template may write into the isolated HOME, deduplicated
// and sorted.
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

// Payload is the rendered credential content: it is stored in the database and, at run time, written into the
// isolated HOME and the process environment.
type Payload struct {
	Env   map[string]string `json:"env"`
	Files []PayloadFile     `json:"files"`
}

// PayloadFile is one credential file.
type PayloadFile struct {
	Path    string `json:"path"` // relative to the run's HOME
	Content []byte `json:"content"`
}

// EnvNames returns the environment variable names, sorted.
func (p Payload) EnvNames() []string {
	out := make([]string, 0, len(p.Env))
	for k := range p.Env {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FilePaths returns the file paths, sorted.
func (p Payload) FilePaths() []string {
	out := make([]string, 0, len(p.Files))
	for _, f := range p.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// Render validates the input fields against the template and produces the credential content. The returned
// fieldNames are the names of the fields actually filled in.
func (t *Template) Render(input map[string]string) (Payload, []string, error) {
	if t.Credential.PendingReason != "" {
		return Payload{}, nil, apperr.Invalid("cli", t.Credential.PendingReason)
	}
	values := map[string]string{}
	var fieldErrs []apperr.FieldError
	for k := range input {
		if !slices.ContainsFunc(t.Fields, func(f Field) bool { return f.Name == k }) {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+k, "The template has no such field."))
		}
	}
	for _, f := range t.Fields {
		v := strings.TrimSpace(input[f.Name])
		if v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+f.Name, f.Label+" is required."))
			continue
		}
		if f.Type == "enum" && v != "" && !slices.Contains(f.Options, v) {
			fieldErrs = append(fieldErrs, apperr.Field("fields."+f.Name, f.Label+" must be one of: "+strings.Join(f.Options, ", ")))
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
