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
	"loop-now/internal/testutil/testenv"
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

// Binaries for the 5 platforms, SHA256SUMS and manifest.json; the digests match shasum's results; the version the
// binary for this machine reports matches the manifest.
func TestBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiling for 4 platforms is slow")
	}
	m, dir := build(t, "0.0.0-disttest")
	if m.Program != "loopctl" || m.Version != "0.0.0-disttest" || m.SchemaVersion != 1 || len(m.Artifacts) != 5 || m.GoVersion != runtime.Version() {
		t.Fatalf("%+v", m)
	}
	var onDisk Manifest
	b, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal(b, &onDisk); err != nil || len(onDisk.Artifacts) != 5 {
		t.Fatalf("manifest.json: %v", err)
	}
	if shasum, err := exec.LookPath("shasum"); err == nil && runtime.GOOS != "windows" { // shasum.bat on the runner cannot be run directly
		cmd := exec.Command(shasum, "-a", "256", "-c", "SHA256SUMS")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("shasum -c failed: %v\n%s", err, out)
		}
	}
	for _, a := range m.Artifacts {
		_, sum, _ := digest(filepath.Join(dir, a.File))
		if sum != a.SHA256 {
			t.Errorf("%s digest does not match the manifest", a.File)
		}
		if file, err := exec.LookPath("file"); err == nil {
			out, _ := exec.Command(file, filepath.Join(dir, a.File)).Output()
			want := map[string][]string{
				"darwin/arm64": {"Mach-O", "arm64"}, "darwin/amd64": {"Mach-O", "x86_64"},
				"linux/amd64": {"ELF", "x86-64", "statically linked"}, "linux/arm64": {"ELF", "aarch64", "statically linked"},
			}[a.OS+"/"+a.Arch]
			for _, w := range want {
				if !strings.Contains(string(out), w) {
					t.Errorf("%s: file output %q does not contain %q", a.File, strings.TrimSpace(string(out)), w)
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
				t.Errorf("native binary reports version %q, manifest says %q", v.Data.Version, m.Version)
			}
		}
	}
}

// Building twice from the same commit with the same version number gives identical SHA256SUMS.
func TestReproducible(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiling for 4 platforms is slow")
	}
	_, a := build(t, "0.0.0-repro")
	_, b := build(t, "0.0.0-repro")
	x, _ := os.ReadFile(filepath.Join(a, "SHA256SUMS"))
	y, _ := os.ReadFile(filepath.Join(b, "SHA256SUMS"))
	if len(x) == 0 || string(x) != string(y) {
		t.Fatalf("SHA256SUMS differ between two builds:\n%s\n%s", x, y)
	}
}

func TestRejectsBadInput(t *testing.T) {
	root, _ := moduleRoot(context.Background())
	for _, v := range []string{"", "../x", "a b", "-x"} {
		if _, _, err := Build(context.Background(), root, v, t.TempDir()); err == nil {
			t.Errorf("version %q should be rejected", v)
		}
	}
	out := t.TempDir()
	os.MkdirAll(filepath.Join(out, "1.0.0"), 0o755)
	os.WriteFile(filepath.Join(out, "1.0.0", "old"), []byte("x"), 0o644)
	if _, _, err := Build(context.Background(), root, "1.0.0", out); err == nil {
		t.Error("should refuse a non-empty output directory")
	}
}

// The server accepts this tool's output using its own reading logic (internal/server/loopctldist, referenced only
// in tests): it checks the manifest and digests, installs the binary for this platform into the tools directory and
// serves downloads on the external port (tasks 8.3).
func TestServerAcceptsArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiling for 4 platforms is slow")
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
		t.Fatalf("server rejected the build output: %v", err)
	}
	if _, err := loopctldist.Load(out, "0.0.0-other"); err == nil {
		t.Fatal("server should reject a different version")
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
		t.Fatalf("loopctl installed in the tools directory reports version %q", v.Data.Version)
	}
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	for _, name := range []string{"manifest.json", "SHA256SUMS", testenv.Exe("loopctl-" + runtime.GOOS + "-" + runtime.GOARCH)} {
		resp, err := http.Get(srv.URL + "/downloads/loopctl/" + name)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h := sha256.Sum256(body)
		if resp.StatusCode != 200 || resp.Header.Get("X-Content-SHA256") != hex.EncodeToString(h[:]) {
			t.Errorf("%s: %d, X-Content-SHA256 %q", name, resp.StatusCode, resp.Header.Get("X-Content-SHA256"))
		}
	}
	if resp, _ := http.Get(srv.URL + "/downloads/loopctl/../go.mod"); resp == nil || resp.StatusCode == 200 {
		t.Error("files outside the manifest should not be downloadable")
	}
}
