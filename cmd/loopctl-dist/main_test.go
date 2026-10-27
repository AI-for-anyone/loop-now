package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/server/loopctldist"
)

func build(t *testing.T, version string) (*Manifest, string) {
	t.Helper()
	root, err := moduleRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m, dir, err := Build(context.Background(), root, version, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return m, dir
}

// 4 个平台的二进制、SHA256SUMS 与 manifest.json；摘要与 shasum 的结果一致；本机二进制报告的版本与清单一致。
func TestBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("交叉编译 4 个平台较慢")
	}
	m, dir := build(t, "0.0.0-disttest")
	if m.Program != "loopctl" || m.Version != "0.0.0-disttest" || m.SchemaVersion != 1 || len(m.Artifacts) != 4 || m.GoVersion != runtime.Version() {
		t.Fatalf("%+v", m)
	}
	var onDisk Manifest
	b, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal(b, &onDisk); err != nil || len(onDisk.Artifacts) != 4 {
		t.Fatalf("manifest.json：%v", err)
	}
	if shasum, err := exec.LookPath("shasum"); err == nil {
		cmd := exec.Command(shasum, "-a", "256", "-c", "SHA256SUMS")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shasum -c 失败：%v\n%s", err, out)
		}
	}
	for _, a := range m.Artifacts {
		_, sum, _ := digest(filepath.Join(dir, a.File))
		if sum != a.SHA256 {
			t.Errorf("%s 的摘要与清单不一致", a.File)
		}
		if file, err := exec.LookPath("file"); err == nil {
			out, _ := exec.Command(file, filepath.Join(dir, a.File)).Output()
			want := map[string][]string{
				"darwin/arm64": {"Mach-O", "arm64"}, "darwin/amd64": {"Mach-O", "x86_64"},
				"linux/amd64": {"ELF", "x86-64", "statically linked"}, "linux/arm64": {"ELF", "aarch64", "statically linked"},
			}[a.OS+"/"+a.Arch]
			for _, w := range want {
				if !strings.Contains(string(out), w) {
					t.Errorf("%s：file 的结果 %q 中没有 %q", a.File, strings.TrimSpace(string(out)), w)
				}
			}
		}
		if a.OS == runtime.GOOS && a.Arch == runtime.GOARCH {
			out, err := exec.Command(filepath.Join(dir, a.File), "version").Output()
			if err != nil {
				t.Fatal(err)
			}
			var v struct {
				Data struct {
					Version string `json:"version"`
				} `json:"data"`
			}
			if json.Unmarshal(out, &v); v.Data.Version != m.Version {
				t.Errorf("本机二进制报告的版本为 %q，清单为 %q", v.Data.Version, m.Version)
			}
		}
	}
}

// 同一提交、同一版本号构建两次，SHA256SUMS 完全相同。
func TestReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("交叉编译 4 个平台较慢")
	}
	_, a := build(t, "0.0.0-repro")
	_, b := build(t, "0.0.0-repro")
	x, _ := os.ReadFile(filepath.Join(a, "SHA256SUMS"))
	y, _ := os.ReadFile(filepath.Join(b, "SHA256SUMS"))
	if len(x) == 0 || string(x) != string(y) {
		t.Fatalf("两次构建的 SHA256SUMS 不同：\n%s\n%s", x, y)
	}
}

func TestRejectsBadInput(t *testing.T) {
	root, _ := moduleRoot(context.Background())
	for _, v := range []string{"", "../x", "a b", "-x"} {
		if _, _, err := Build(context.Background(), root, v, t.TempDir()); err == nil {
			t.Errorf("版本号 %q 应被拒绝", v)
		}
	}
	out := t.TempDir()
	os.MkdirAll(filepath.Join(out, "1.0.0"), 0o755)
	os.WriteFile(filepath.Join(out, "1.0.0", "old"), []byte("x"), 0o644)
	if _, _, err := Build(context.Background(), root, "1.0.0", out); err == nil {
		t.Error("输出目录不为空时应拒绝")
	}
}

// 服务端用自己的读取逻辑（internal/server/loopctldist，只在测试中引用）接受本工具的产物：
// 校验清单与摘要、把本机平台的二进制装进工具目录、在对外端口上提供下载（tasks 8.3）。
func TestServerAcceptsArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("交叉编译 4 个平台较慢")
	}
	root, err := moduleRoot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	const version = "0.0.0-server-check"
	if _, _, err := Build(context.Background(), root, version, out); err != nil {
		t.Fatal(err)
	}
	d, err := loopctldist.Load(out, version)
	if err != nil {
		t.Fatalf("服务端拒绝了产物：%v", err)
	}
	if _, err := loopctldist.Load(out, "0.0.0-other"); err == nil {
		t.Fatal("版本不同时服务端应拒绝")
	}
	binDir, err := d.Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := exec.Command(filepath.Join(binDir, "loopctl"), "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &v); v.Data.Version != version {
		t.Fatalf("装进工具目录的 loopctl 报告的版本为 %q", v.Data.Version)
	}
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	for _, name := range []string{"manifest.json", "SHA256SUMS", "loopctl-" + runtime.GOOS + "-" + runtime.GOARCH} {
		resp, err := http.Get(srv.URL + "/downloads/loopctl/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h := sha256.Sum256(body)
		if resp.StatusCode != 200 || resp.Header.Get("X-Content-SHA256") != hex.EncodeToString(h[:]) {
			t.Errorf("%s：%d，X-Content-SHA256 %q", name, resp.StatusCode, resp.Header.Get("X-Content-SHA256"))
		}
	}
	if resp, _ := http.Get(srv.URL + "/downloads/loopctl/../go.mod"); resp == nil || resp.StatusCode == 200 {
		t.Error("清单之外的文件不应提供下载")
	}
}
