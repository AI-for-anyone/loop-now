package clitemplate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"loop-now/internal/server/apperr"
)

func TestLoadBuiltin(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	gh, ok := c.Get("gh")
	if !ok || gh.Version != "2.100.0" || gh.Injection != "env" {
		t.Fatalf("unexpected gh template: %+v", gh)
	}
	lark, ok := c.Get("lark-cli")
	if !ok || lark.Version != "1.0.94" || lark.Injection != "files" {
		t.Fatalf("unexpected lark-cli template: %+v", lark)
	}
	for _, tmpl := range c.All() {
		platforms := []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"}
		if tmpl.Name == "psql" || tmpl.Name == "mysql" || tmpl.Name == "gcloud" {
			platforms = []string{"darwin/arm64"}
		}
		for _, plat := range platforms {
			if _, ok := tmpl.Install[plat]; !ok {
				t.Errorf("%s is missing the %s package", tmpl.Name, plat)
			}
		}
	}
	brand := lark.Fields[2]
	if brand.Type != "enum" || brand.Default != "feishu" || len(brand.Options) != 2 {
		t.Fatalf("lark-cli brand should be an enum: %+v", brand)
	}
}

func TestLoadOverrideDir(t *testing.T) {
	dir := t.TempDir()
	tmpl := `{"name":"gh","version":"9.9.9","injection":"env","fields":[{"name":"token","label":"t","type":"secret","required":true}],
	"credential":{"env":{"GH_TOKEN":"{{token}}"}},"binary":"gh","install":{}}`
	if err := os.WriteFile(filepath.Join(dir, "gh.json"), []byte(tmpl), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if gh, _ := c.Get("gh"); gh.Version != "9.9.9" {
		t.Fatalf("a template in the directory should override the built-in one: %s", gh.Version)
	}
	bad := `{"name":"x","version":"1","injection":"env","fields":[],"credential":{"env":{"X":"{{nope}}"}},"binary":"x"}`
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a template referring to a field that does not exist should fail to load")
	}
}

func TestRenderGH(t *testing.T) {
	c, _ := Load("")
	gh, _ := c.Get("gh")
	p, names, err := gh.Render(map[string]string{"token": "ghp_example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Env["GH_TOKEN"] != "ghp_example" || len(names) != 1 || names[0] != "token" {
		t.Fatalf("unexpected render result: %+v %v", p, names)
	}
	if _, _, err := gh.Render(map[string]string{}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("a missing required field should fail validation: %v", err)
	}
	if _, _, err := gh.Render(map[string]string{"token": "x", "extra": "y"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("an extra field should fail validation: %v", err)
	}
}

func TestRenderLark(t *testing.T) {
	c, _ := Load("")
	lark, _ := c.Get("lark-cli")
	p, names, err := lark.Render(map[string]string{"appId": "cli_example", "appSecret": "secret-1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "appId,appSecret,brand" {
		t.Fatalf("unexpected filled fields (brand takes its default): %v", names)
	}
	if len(p.Env) != 0 {
		t.Fatalf("lark-cli credentials should not be injected through environment variables: %v", p.Env)
	}
	if lark.Env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] != "1" || lark.Env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] != "1" || lark.Env["LARKSUITE_CLI_REMOTE_META"] != "off" || len(lark.Env) != 3 {
		t.Fatalf("the template should declare the three fixed variables that turn off background network access: %v", lark.Env)
	}
	files := map[string][]byte{}
	for _, f := range p.Files {
		files[f.Path] = f.Content
	}
	var cfg struct {
		Apps []struct {
			AppID, AppSecret, Brand, StrictMode, DefaultAs string
			Users                                          []any
		}
	}
	if err := json.Unmarshal(files[".lark-cli/config.json"], &cfg); err != nil {
		t.Fatalf("the config file is not valid JSON: %v", err)
	}
	if len(cfg.Apps) != 1 {
		t.Fatalf("there should be exactly one app: %+v", cfg)
	}
	a := cfg.Apps[0]
	if a.AppID != "cli_example" || a.AppSecret != "secret-1" || a.Brand != "feishu" || a.StrictMode != "bot" || a.DefaultAs != "bot" || a.Users == nil || len(a.Users) != 0 {
		t.Fatalf("unexpected config content: %+v", a)
	}
	_, hasKey := files["Library/Application Support/lark-cli/master.key.file"]
	if hasKey != (runtime.GOOS == "darwin") || len(p.Files) != len(files) {
		t.Fatalf("unexpected files generated for this platform (%s): %v", runtime.GOOS, p.FilePaths())
	}
	// Every credential file that may be written is covered by the cleanup before a start.
	for path := range files {
		if !slices.Contains(lark.CredentialFilePaths(), path) {
			t.Errorf("%s is not covered by the cleanup: %v", path, lark.CredentialFilePaths())
		}
	}
	if p2, _, err := lark.Render(map[string]string{"appId": "cli_example", "appSecret": "s", "brand": "lark"}); err != nil || !strings.Contains(string(p2.Files[0].Content), `"brand": "lark"`) {
		t.Fatalf("brand lark should be written to the config: %v", err)
	}
	if _, _, err := lark.Render(map[string]string{"appId": "cli_example"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("a missing App Secret should fail validation: %v", err)
	}
}

func TestTemplateEnvRejectsPlaceholders(t *testing.T) {
	tmpl := &Template{Name: "x", Version: "1", Binary: "x", Injection: "env", Fields: []Field{{Name: "token", Label: "t", Type: "secret"}},
		Env: map[string]string{"X_MODE": "{{token}}"}}
	if err := tmpl.validate(); err == nil || !strings.Contains(err.Error(), "must not reference a field") {
		t.Fatalf("a fixed environment variable referring to a field should be rejected at load: %v", err)
	}
}

// TestReservedEnv covers the variable names model account credentials cannot use: the exact names and prefixes each
// template declares based on the CLI's source code, plus the variable names the template injects itself.
func TestReservedEnv(t *testing.T) {
	c, _ := Load("")
	reserved := map[string]string{
		"GH_TOKEN": "gh", "GH_HOST": "gh", "GH_CONFIG_DIR": "gh", "GH_PATH": "gh", "GITHUB_TOKEN": "gh", "GITHUB_ENTERPRISE_TOKEN": "gh",
		"GITHUB_API_URL": "gh", "GITHUB_SERVER_URL": "gh", "XDG_CONFIG_HOME": "gh", "XDG_DATA_HOME": "gh",
		"LARKSUITE_CLI_APP_ID": "lark-cli", "LARKSUITE_CLI_APP_SECRET": "lark-cli", "LARKSUITE_CLI_REMOTE_META": "lark-cli",
		"LARK_CLI_NO_PROXY": "lark-cli", "OPENCLAW_HOME": "lark-cli", "HERMES_HOME": "lark-cli", "LARK_CHANNEL": "lark-cli", "LARK_CHANNEL_CONFIG": "lark-cli",
	}
	for name, want := range reserved {
		if got, ok := c.ReservedEnvBy(name); !ok || got != want {
			t.Errorf("%s should be reserved by %s, got %q %v", name, want, got, ok)
		}
	}
	// Decided by the user: generic variables such as proxies, certificates, GIT_* and SSH_* are not reserved (a known
	// risk in design.md).
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "SSL_CERT_FILE", "GIT_SSH_COMMAND", "SSH_AUTH_SOCK", "XDG_CACHE_HOME", "GITHUB", "LARK"} {
		if tmpl, ok := c.ReservedEnvBy(name); ok {
			t.Errorf("%s should not be reserved (reserved by %s)", name, tmpl)
		}
	}
	bad := &Template{Name: "x", Version: "1", Binary: "x", Injection: "env", ReservedEnvPrefixes: []string{"1BAD"}}
	if err := bad.validate(); err == nil {
		t.Fatal("an invalid reserved prefix should be rejected at load")
	}
}

func TestRenderLarkCLIByPlatform(t *testing.T) {
	values := map[string]string{"appId": "a", "appSecret": "s", "brand": "feishu"}
	linux, err := renderLarkCLI(values, "linux", bytes.NewReader(nil))
	if err != nil || len(linux) != 1 || linux[0].Path != ".lark-cli/config.json" {
		t.Fatalf("Linux should write only the config file: %+v %v", linux, err)
	}
	k1, err := renderLarkCLI(values, "darwin", bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil || len(k1) != 2 || k1[1].Path != "Library/Application Support/lark-cli/master.key.file" || !bytes.Equal(k1[1].Content, bytes.Repeat([]byte{1}, 32)) {
		t.Fatalf("macOS should also write a 32-byte master key file: %+v %v", k1, err)
	}
	if _, err := renderLarkCLI(values, "darwin", bytes.NewReader(make([]byte, 8))); err == nil {
		t.Fatal("should fail when there are not enough random bytes")
	}
}

func TestRenderEnumValidation(t *testing.T) {
	tmpl := &Template{Name: "x", Fields: []Field{{Name: "brand", Label: "Brand", Type: "enum", Options: []string{"feishu", "lark"}, Default: "feishu", Required: true}},
		Credential: Credential{Env: map[string]string{"B": "{{brand}}"}}}
	if p, _, err := tmpl.Render(map[string]string{}); err != nil || p.Env["B"] != "feishu" {
		t.Fatalf("should use the default when not filled in: %+v %v", p, err)
	}
	if _, _, err := tmpl.Render(map[string]string{"brand": "slack"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("a value outside the options should fail validation: %v", err)
	}
}

// ---- Installer ----

func tarGz(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(data)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, name string, data []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: name}
	h.SetMode(0o755)
	w, _ := zw.CreateHeader(h)
	_, _ = w.Write(data)
	_ = zw.Close()
	return buf.Bytes()
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func serve(t *testing.T, body []byte, hits *atomic.Int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testTemplate(url, sha, format, path string) *Template {
	return &Template{Name: "demo", Version: "1.0.0", Binary: "demo",
		Install: map[string]Asset{Platform(): {URL: url, SHA256: sha, Format: format, Path: path}}}
}

func TestInstallTarGzOnce(t *testing.T) {
	bin := []byte("#!/bin/sh\necho demo\n")
	archive := tarGz(t, "./demo-1.0.0/bin/demo", bin)
	var hits atomic.Int32
	srv := serve(t, archive, &hits)
	inst := NewInstaller(t.TempDir(), time.Minute, srv.Client())
	tmpl := testTemplate(srv.URL, shaHex(archive), "tar.gz", "demo-1.0.0/bin/demo")

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, err := inst.Ensure(context.Background(), tmpl); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatalf("concurrent installs should download once, got %d downloads", hits.Load())
	}
	got, err := os.ReadFile(filepath.Join(inst.BinDir(tmpl), "demo"))
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("unexpected installed binary: %v", err)
	}
	if st, _ := inst.Status(tmpl); st != StateInstalled {
		t.Fatalf("status should be installed: %s", st)
	}
	if _, err := inst.Ensure(context.Background(), tmpl); err != nil || hits.Load() != 1 {
		t.Fatalf("should not download again once installed: %v %d", err, hits.Load())
	}
}

func TestInstallZip(t *testing.T) {
	archive := zipOf(t, "demo_1.0.0/bin/demo", []byte("bin"))
	var hits atomic.Int32
	srv := serve(t, archive, &hits)
	inst := NewInstaller(t.TempDir(), time.Minute, srv.Client())
	tmpl := testTemplate(srv.URL, shaHex(archive), "zip", "demo_1.0.0/bin/demo")
	if _, err := inst.Ensure(context.Background(), tmpl); err != nil {
		t.Fatal(err)
	}
}

func TestInstallChecksumMismatch(t *testing.T) {
	archive := tarGz(t, "demo", []byte("bin"))
	var hits atomic.Int32
	srv := serve(t, archive, &hits)
	root := t.TempDir()
	inst := NewInstaller(root, time.Minute, srv.Client())
	tmpl := testTemplate(srv.URL, strings.Repeat("0", 64), "tar.gz", "demo")
	_, err := inst.Ensure(context.Background(), tmpl)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a checksum mismatch should fail: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "demo", "1.0.0")); !os.IsNotExist(err) {
		t.Fatal("on a checksum mismatch the version should not appear in the tools directory")
	}
	if st, msg := inst.Status(tmpl); st != StateFailed || !strings.Contains(msg, "checksum mismatch") {
		t.Fatalf("status should be failed with the reason: %s %s", st, msg)
	}
}

func TestInstallUnsupportedPlatform(t *testing.T) {
	inst := NewInstaller(t.TempDir(), time.Minute, nil)
	tmpl := &Template{Name: "demo", Version: "1", Binary: "demo", Install: map[string]Asset{"plan9/mips": {}}}
	if _, err := inst.Ensure(context.Background(), tmpl); err == nil || !strings.Contains(err.Error(), Platform()) {
		t.Fatalf("no package for this platform should fail and name the platform: %v", err)
	}
	if st, _ := inst.Status(tmpl); st != StateUnsupportedPlatform {
		t.Fatalf("status should be unsupported_platform: %s", st)
	}
}

// Field messages of a template do not repeat the field's label (labels come from the template files and are not
// English): the error is already attached to fields.<name>.
func TestRenderFieldMessagesOmitLabel(t *testing.T) {
	c, _ := Load("")
	gh, _ := c.Get("gh")
	_, _, err := gh.Render(map[string]string{})
	if e, ok := apperr.As(err); !ok || len(e.Fields) != 1 || e.Fields[0].Field != "fields.token" || e.Fields[0].Message != "Required." {
		t.Fatalf("missing required field: %+v", err)
	}
	lark, _ := c.Get("lark-cli")
	_, _, err = lark.Render(map[string]string{"brand": "other"})
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("invalid enum value: %v", err)
	}
	found := false
	for _, f := range e.Fields {
		if f.Field == "fields.brand" {
			found = f.Message == "Must be one of: feishu, lark."
		}
	}
	if !found {
		t.Fatalf("invalid enum value: %+v", e.Fields)
	}
}
