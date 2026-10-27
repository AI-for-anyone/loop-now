package loopctldist_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/buildinfo"
	"loop-now/internal/server/loopctldist"
	"loop-now/internal/testutil/loopctlfake"
)

func TestLoadAndInstall(t *testing.T) {
	root := loopctlfake.Dir(t)
	d, err := loopctldist.Load(root, buildinfo.Version())
	if err != nil {
		t.Fatalf("合法的构建产物应通过校验：%v", err)
	}
	local, _ := d.Local()
	if local.OS != runtime.GOOS || local.Arch != runtime.GOARCH {
		t.Fatalf("本机平台：%+v", local)
	}
	work := t.TempDir()
	bin, err := d.Install(work)
	if err != nil {
		t.Fatal(err)
	}
	if bin != filepath.Join(work, "tools", "loopctl", buildinfo.Version(), "bin") {
		t.Fatalf("安装目录：%s", bin)
	}
	b, err := os.ReadFile(filepath.Join(bin, "loopctl"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != local.SHA256 {
		t.Fatal("安装的二进制摘要应与清单一致")
	}
	if st, _ := os.Stat(filepath.Join(bin, "loopctl")); st.Mode()&0o111 == 0 {
		t.Fatal("安装的二进制应可执行")
	}
	// 被改动后再次安装会恢复。
	if err := os.WriteFile(filepath.Join(bin, "loopctl"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Install(work); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(filepath.Join(bin, "loopctl")); string(b2) != string(b) {
		t.Fatal("再次安装应覆盖被改动的二进制")
	}
}

func TestLoadRejects(t *testing.T) {
	v := buildinfo.Version()
	cases := map[string]func(dir string){
		"版本不一致": func(dir string) {
			rewrite(t, filepath.Join(dir, "manifest.json"), `"version": "`+v+`"`, `"version": "other"`)
		},
		"程序名不对": func(dir string) {
			rewrite(t, filepath.Join(dir, "manifest.json"), `"program": "loopctl"`, `"program": "pi"`)
		},
		"文件被改动": func(dir string) {
			f := filepath.Join(dir, "loopctl-"+runtime.GOOS+"-"+runtime.GOARCH)
			b, _ := os.ReadFile(f)
			_ = os.WriteFile(f, append(b, '#'), 0o755)
		},
		"SHA256SUMS 不一致": func(dir string) {
			b, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			_ = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines[1:], "\n")+"\n"), 0o644)
		},
		"缺少清单": func(dir string) { _ = os.Remove(filepath.Join(dir, "manifest.json")) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := loopctlfake.Dir(t)
			mutate(filepath.Join(root, v))
			_, err := loopctldist.Load(root, v)
			if err == nil {
				t.Fatal("应拒绝")
			}
			if name == "版本不一致" && (!strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), v)) {
				t.Fatalf("版本不一致时应给出两个版本号：%v", err)
			}
		})
	}
	// 目录中没有服务端版本的产物。
	if _, err := loopctldist.Load(loopctlfake.Dir(t), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("没有同版本的产物时应拒绝：%v", err)
	}
}

func rewrite(t *testing.T, p, old, new string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s 中没有 %s", p, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHandler(t *testing.T) {
	d, err := loopctldist.Load(loopctlfake.Dir(t), buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	h := d.Handler()
	local, _ := d.Local()
	for _, name := range []string{"manifest.json", "SHA256SUMS", local.File} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/downloads/loopctl/"+name, nil))
		body, _ := io.ReadAll(rec.Body)
		sum := sha256.Sum256(body)
		if rec.Code != 200 || rec.Header().Get("X-Content-SHA256") != hex.EncodeToString(sum[:]) || rec.Header().Get("Content-Length") == "" {
			t.Fatalf("%s：%d %v", name, rec.Code, rec.Header())
		}
	}
	for _, name := range []string{"../manifest.json", "other", ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/downloads/loopctl/"+name, nil))
		if rec.Code != 404 {
			t.Fatalf("清单中没有的文件应返回 404：%s %d", name, rec.Code)
		}
	}
}
