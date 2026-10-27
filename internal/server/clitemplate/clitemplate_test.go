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
		t.Fatalf("gh 模板不对：%+v", gh)
	}
	lark, ok := c.Get("lark-cli")
	if !ok || lark.Version != "1.0.94" || lark.Injection != "files" {
		t.Fatalf("lark-cli 模板不对：%+v", lark)
	}
	for _, tmpl := range c.All() {
		platforms := []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64"}
		if tmpl.Name == "psql" || tmpl.Name == "mysql" || tmpl.Name == "gcloud" {
			platforms = []string{"darwin/arm64"}
		}
		for _, plat := range platforms {
			if _, ok := tmpl.Install[plat]; !ok {
				t.Errorf("%s 缺少 %s 安装包", tmpl.Name, plat)
			}
		}
	}
	brand := lark.Fields[2]
	if brand.Type != "enum" || brand.Default != "feishu" || len(brand.Options) != 2 {
		t.Fatalf("lark-cli 的 brand 应是枚举：%+v", brand)
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
		t.Fatalf("目录中的模板应覆盖内置模板：%s", gh.Version)
	}
	bad := `{"name":"x","version":"1","injection":"env","fields":[],"credential":{"env":{"X":"{{nope}}"}},"binary":"x"}`
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("引用不存在录入项的模板应加载失败")
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
		t.Fatalf("渲染结果不对：%+v %v", p, names)
	}
	if _, _, err := gh.Render(map[string]string{}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("缺少必填项应校验失败：%v", err)
	}
	if _, _, err := gh.Render(map[string]string{"token": "x", "extra": "y"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("多余录入项应校验失败：%v", err)
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
		t.Fatalf("实际填写的录入项不对（brand 取默认值）：%v", names)
	}
	if len(p.Env) != 0 {
		t.Fatalf("lark-cli 的凭证不经环境变量注入：%v", p.Env)
	}
	if lark.Env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] != "1" || lark.Env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] != "1" || lark.Env["LARKSUITE_CLI_REMOTE_META"] != "off" || len(lark.Env) != 3 {
		t.Fatalf("模板应声明关闭后台联网的三个固定变量：%v", lark.Env)
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
		t.Fatalf("配置文件不是合法 JSON：%v", err)
	}
	if len(cfg.Apps) != 1 {
		t.Fatalf("应只有一个应用：%+v", cfg)
	}
	a := cfg.Apps[0]
	if a.AppID != "cli_example" || a.AppSecret != "secret-1" || a.Brand != "feishu" || a.StrictMode != "bot" || a.DefaultAs != "bot" || a.Users == nil || len(a.Users) != 0 {
		t.Fatalf("配置内容不对：%+v", a)
	}
	_, hasKey := files["Library/Application Support/lark-cli/master.key.file"]
	if hasKey != (runtime.GOOS == "darwin") || len(p.Files) != len(files) {
		t.Fatalf("本平台（%s）生成的文件不对：%v", runtime.GOOS, p.FilePaths())
	}
	// 每个可能写入的凭证文件都在启动前清理的范围内。
	for path := range files {
		if !slices.Contains(lark.CredentialFilePaths(), path) {
			t.Errorf("%s 不在清理范围内：%v", path, lark.CredentialFilePaths())
		}
	}
	if p2, _, err := lark.Render(map[string]string{"appId": "cli_example", "appSecret": "s", "brand": "lark"}); err != nil || !strings.Contains(string(p2.Files[0].Content), `"brand": "lark"`) {
		t.Fatalf("品牌 lark 应写入配置：%v", err)
	}
	if _, _, err := lark.Render(map[string]string{"appId": "cli_example"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("缺少 App Secret 应校验失败：%v", err)
	}
}

func TestTemplateEnvRejectsPlaceholders(t *testing.T) {
	tmpl := &Template{Name: "x", Version: "1", Binary: "x", Injection: "env", Fields: []Field{{Name: "token", Label: "t", Type: "secret"}},
		Env: map[string]string{"X_MODE": "{{token}}"}}
	if err := tmpl.validate(); err == nil || !strings.Contains(err.Error(), "不能引用录入项") {
		t.Fatalf("固定环境变量引用录入项时应拒绝加载：%v", err)
	}
}

// 模型账号凭证不能使用的变量名：各模板按源码声明的精确名与前缀，加上模板自己注入的变量名。
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
			t.Errorf("%s 应由 %s 保留，实际 %q %v", name, want, got, ok)
		}
	}
	// 用户已决定：代理、证书、GIT_*、SSH_* 等通用变量不保留（design.md 已知风险）。
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "SSL_CERT_FILE", "GIT_SSH_COMMAND", "SSH_AUTH_SOCK", "XDG_CACHE_HOME", "GITHUB", "LARK"} {
		if tmpl, ok := c.ReservedEnvBy(name); ok {
			t.Errorf("%s 不应被保留（实际由 %s 保留）", name, tmpl)
		}
	}
	bad := &Template{Name: "x", Version: "1", Binary: "x", Injection: "env", ReservedEnvPrefixes: []string{"1BAD"}}
	if err := bad.validate(); err == nil {
		t.Fatal("不合法的保留前缀应拒绝加载")
	}
}

func TestRenderLarkCLIByPlatform(t *testing.T) {
	values := map[string]string{"appId": "a", "appSecret": "s", "brand": "feishu"}
	linux, err := renderLarkCLI(values, "linux", bytes.NewReader(nil))
	if err != nil || len(linux) != 1 || linux[0].Path != ".lark-cli/config.json" {
		t.Fatalf("Linux 只写配置文件：%+v %v", linux, err)
	}
	k1, err := renderLarkCLI(values, "darwin", bytes.NewReader(bytes.Repeat([]byte{1}, 32)))
	if err != nil || len(k1) != 2 || k1[1].Path != "Library/Application Support/lark-cli/master.key.file" || !bytes.Equal(k1[1].Content, bytes.Repeat([]byte{1}, 32)) {
		t.Fatalf("macOS 应另写 32 字节主密钥文件：%+v %v", k1, err)
	}
	if _, err := renderLarkCLI(values, "darwin", bytes.NewReader(make([]byte, 8))); err == nil {
		t.Fatal("随机数不足时应报错")
	}
}

func TestRenderEnumValidation(t *testing.T) {
	tmpl := &Template{Name: "x", Fields: []Field{{Name: "brand", Label: "品牌", Type: "enum", Options: []string{"feishu", "lark"}, Default: "feishu", Required: true}},
		Credential: Credential{Env: map[string]string{"B": "{{brand}}"}}}
	if p, _, err := tmpl.Render(map[string]string{}); err != nil || p.Env["B"] != "feishu" {
		t.Fatalf("未填写时应使用默认值：%+v %v", p, err)
	}
	if _, _, err := tmpl.Render(map[string]string{"brand": "slack"}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatalf("不在可选值中应校验失败：%v", err)
	}
}

// ---- 安装器 ----

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
		t.Fatalf("并发安装应只下载一次，实际 %d 次", hits.Load())
	}
	got, err := os.ReadFile(filepath.Join(inst.BinDir(tmpl), "demo"))
	if err != nil || !bytes.Equal(got, bin) {
		t.Fatalf("安装的二进制不对：%v", err)
	}
	if st, _ := inst.Status(tmpl); st != StateInstalled {
		t.Fatalf("状态应为已安装：%s", st)
	}
	if _, err := inst.Ensure(context.Background(), tmpl); err != nil || hits.Load() != 1 {
		t.Fatalf("已安装时不应再下载：%v %d", err, hits.Load())
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
	if err == nil || !strings.Contains(err.Error(), "摘要不符") {
		t.Fatalf("摘要不符应失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "demo", "1.0.0")); !os.IsNotExist(err) {
		t.Fatal("摘要不符时工具目录中不应出现该版本")
	}
	if st, msg := inst.Status(tmpl); st != StateFailed || !strings.Contains(msg, "摘要不符") {
		t.Fatalf("状态应为失败并带原因：%s %s", st, msg)
	}
}

func TestInstallUnsupportedPlatform(t *testing.T) {
	inst := NewInstaller(t.TempDir(), time.Minute, nil)
	tmpl := &Template{Name: "demo", Version: "1", Binary: "demo", Install: map[string]Asset{"plan9/mips": {}}}
	if _, err := inst.Ensure(context.Background(), tmpl); err == nil || !strings.Contains(err.Error(), Platform()) {
		t.Fatalf("无对应平台安装包应失败并说明平台：%v", err)
	}
	if st, _ := inst.Status(tmpl); st != StateUnsupportedPlatform {
		t.Fatalf("状态应为 unsupported_platform：%s", st)
	}
}
