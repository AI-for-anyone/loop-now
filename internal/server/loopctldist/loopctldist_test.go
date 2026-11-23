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
	"loop-now/internal/testutil/testenv"
)

func TestLoadAndInstall(t *testing.T) {
	root := loopctlfake.Dir(t)
	d, err := loopctldist.Load(root, buildinfo.Version())
	if err != nil {
		t.Fatalf("valid build output should pass verification: %v", err)
	}
	local, _ := d.Local()
	if local.OS != runtime.GOOS || local.Arch != runtime.GOARCH {
		t.Fatalf("native platform: %+v", local)
	}
	work := t.TempDir()
	bin, err := d.Install(work)
	if err != nil {
		t.Fatal(err)
	}
	if bin != filepath.Join(work, "tools", "loopctl", buildinfo.Version(), "bin") {
		t.Fatalf("install directory: %s", bin)
	}
	b, err := os.ReadFile(testenv.Exe(filepath.Join(bin, "loopctl")))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != local.SHA256 {
		t.Fatal("installed binary digest should match the manifest")
	}
	if st, _ := os.Stat(testenv.Exe(filepath.Join(bin, "loopctl"))); runtime.GOOS != "windows" && st.Mode()&0o111 == 0 {
		t.Fatal("installed binary should be executable")
	}
	// After tampering, installing again restores it.
	if err := os.WriteFile(testenv.Exe(filepath.Join(bin, "loopctl")), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Install(work); err != nil {
		t.Fatal(err)
	}
	if b2, _ := os.ReadFile(testenv.Exe(filepath.Join(bin, "loopctl"))); string(b2) != string(b) {
		t.Fatal("installing again should overwrite the modified binary")
	}
}

func TestLoadRejects(t *testing.T) {
	v := buildinfo.Version()
	cases := map[string]func(dir string){
		"version mismatch": func(dir string) {
			rewrite(t, filepath.Join(dir, "manifest.json"), `"version": "`+v+`"`, `"version": "other"`)
		},
		"wrong program name": func(dir string) {
			rewrite(t, filepath.Join(dir, "manifest.json"), `"program": "loopctl"`, `"program": "pi"`)
		},
		"file modified": func(dir string) {
			f := testenv.Exe(filepath.Join(dir, "loopctl-"+runtime.GOOS+"-"+runtime.GOARCH))
			b, _ := os.ReadFile(f)
			_ = os.WriteFile(f, append(b, '#'), 0o755)
		},
		"SHA256SUMS mismatch": func(dir string) {
			b, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			_ = os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines[1:], "\n")+"\n"), 0o644)
		},
		"missing manifest": func(dir string) { _ = os.Remove(filepath.Join(dir, "manifest.json")) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := loopctlfake.Dir(t)
			mutate(filepath.Join(root, v))
			_, err := loopctldist.Load(root, v)
			if err == nil {
				t.Fatal("should be rejected")
			}
			if name == "version mismatch" && (!strings.Contains(err.Error(), "other") || !strings.Contains(err.Error(), v)) {
				t.Fatalf("a version mismatch should name both versions: %v", err)
			}
		})
	}
	// The directory has no build output for the server's version.
	if _, err := loopctldist.Load(loopctlfake.Dir(t), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("should reject when there is no build output for the version: %v", err)
	}
}

func rewrite(t *testing.T, p, old, new string) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %s", p, old)
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
			t.Fatalf("%s: %d %v", name, rec.Code, rec.Header())
		}
	}
	for _, name := range []string{"../manifest.json", "other", ""} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/downloads/loopctl/"+name, nil))
		if rec.Code != 404 {
			t.Fatalf("a file not in the manifest should return 404: %s %d", name, rec.Code)
		}
	}
}
