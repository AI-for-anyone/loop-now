package cloudflared_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"loop-now/internal/server/cloudflared"
)

// script is a stand-in cloudflared that prints version.
func script(version string) []byte {
	return []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo \"cloudflared version " + version + " (built 2026-11-23T11:41:20Z)\"; exit 0; fi\nexit 3\n")
}

func writeScript(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(p, script(version), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func unixOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in cloudflared is a shell script")
	}
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func tgz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

type release struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func serve(t *testing.T, body []byte) *release {
	t.Helper()
	r := &release{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func TestTheConfiguredPathComesFirstAndNeverFallsBack(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	configured := writeScript(t, filepath.Join(dir, "configured"), "2026.11.0")
	onPath := writeScript(t, filepath.Join(dir, "bin"), cloudflared.Version)
	i := &cloudflared.Installer{Dir: filepath.Join(dir, "tools"), Configured: configured, PathDirs: func(context.Context) []string { return []string{filepath.Dir(onPath)} }}
	b, err := i.Ensure(context.Background(), nil)
	if err != nil || b.Path != configured || b.Source != cloudflared.SourceConfigured || b.Version != "2026.11.0" {
		t.Fatalf("got %+v, %v", b, err)
	}
	old := writeScript(t, filepath.Join(dir, "old"), "2025.1.0")
	i = &cloudflared.Installer{Dir: filepath.Join(dir, "tools"), Configured: old, PathDirs: func(context.Context) []string { return []string{filepath.Dir(onPath)} }}
	if _, err := i.Ensure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "LOOP_NOW_CLOUDFLARED_PATH") {
		t.Fatalf("an old configured cloudflared: %v", err)
	}
}

func TestPathSkipsOldOnesAndComesBeforeTheDownload(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	old := writeScript(t, filepath.Join(dir, "old"), "2026.10.2")
	good := writeScript(t, filepath.Join(dir, "good"), "2026.11.1")
	rel := serve(t, script(cloudflared.Version))
	i := &cloudflared.Installer{Dir: filepath.Join(dir, "tools"), GOOS: "linux", GOARCH: "amd64",
		Asset:    &cloudflared.Asset{URL: rel.srv.URL + "/cloudflared-linux-amd64", SHA256: sha(script(cloudflared.Version))},
		PathDirs: func(context.Context) []string { return []string{"relative/dir", filepath.Dir(old), filepath.Dir(good)} }}
	b, err := i.Ensure(context.Background(), nil)
	if err != nil || b.Path != good || b.Source != cloudflared.SourcePath {
		t.Fatalf("got %+v, %v", b, err)
	}
	if rel.hits.Load() != 0 {
		t.Fatal("downloaded although PATH had one")
	}
}

func TestDownloadChecksTheSHA256AndInstallsIntoTheDataFolder(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	body := script(cloudflared.Version)
	rel := serve(t, body)
	i := &cloudflared.Installer{Dir: filepath.Join(dir, "tools", "cloudflared"), GOOS: "linux", GOARCH: "amd64",
		Asset: &cloudflared.Asset{URL: rel.srv.URL + "/cloudflared-linux-amd64", SHA256: sha(body)}}
	var last, total int64
	b, err := i.Ensure(context.Background(), func(received, t int64) { last, total = received, t })
	want := filepath.Join(dir, "tools", "cloudflared", cloudflared.Version, "linux-amd64", "cloudflared")
	if err != nil || b.Path != want || b.Source != cloudflared.SourceDownloaded || b.Version != cloudflared.Version {
		t.Fatalf("got %+v, %v", b, err)
	}
	if last != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("progress %d of %d, want %d", last, total, len(body))
	}
	if info, err := os.Stat(want); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed file: %v %v", info, err)
	}
	// Found again without downloading.
	if b, err := i.Ensure(context.Background(), nil); err != nil || b.Path != want || rel.hits.Load() != 1 {
		t.Fatalf("second Ensure: %+v %v, %d downloads", b, err, rel.hits.Load())
	}
	entries, _ := os.ReadDir(filepath.Dir(want))
	if len(entries) != 1 {
		t.Fatalf("leftovers next to the program: %v", entries)
	}
}

func TestDownloadUnpacksATgz(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	body := tgz(t, "cloudflared", script(cloudflared.Version))
	rel := serve(t, body)
	i := &cloudflared.Installer{Dir: dir, GOOS: "darwin", GOARCH: "arm64",
		Asset: &cloudflared.Asset{URL: rel.srv.URL + "/cloudflared-darwin-arm64.tgz", SHA256: sha(body), Tgz: true}}
	b, err := i.Ensure(context.Background(), nil)
	if err != nil || b.Path != filepath.Join(dir, cloudflared.Version, "darwin-arm64", "cloudflared") {
		t.Fatalf("got %+v, %v", b, err)
	}
}

func TestADownloadWithAnotherChecksumIsRefused(t *testing.T) {
	unixOnly(t)
	dir := t.TempDir()
	rel := serve(t, script(cloudflared.Version))
	i := &cloudflared.Installer{Dir: dir, GOOS: "linux", GOARCH: "arm64",
		Asset: &cloudflared.Asset{URL: rel.srv.URL + "/cloudflared-linux-arm64", SHA256: sha([]byte("what the release really is"))}}
	_, err := i.Ensure(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") || errors.Is(err, cloudflared.ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(i.ManagedPath()); !os.IsNotExist(err) {
		t.Fatalf("a refused download was installed: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(i.ManagedPath()))
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestADownloadOfAnotherVersionIsRefused(t *testing.T) {
	unixOnly(t)
	body := script("2026.1.1")
	rel := serve(t, body)
	i := &cloudflared.Installer{Dir: t.TempDir(), GOOS: "linux", GOARCH: "amd64", Asset: &cloudflared.Asset{URL: rel.srv.URL + "/x", SHA256: sha(body)}}
	if _, err := i.Ensure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "2026.1.1") {
		t.Fatalf("got %v", err)
	}
}

func TestAFailedDownloadSaysWhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusNotFound) }))
	defer srv.Close()
	i := &cloudflared.Installer{Dir: t.TempDir(), GOOS: "linux", GOARCH: "amd64", Asset: &cloudflared.Asset{URL: srv.URL + "/x", SHA256: "00"}}
	if _, err := i.Ensure(context.Background(), nil); err == nil || !strings.Contains(err.Error(), srv.URL) || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v", err)
	}
}

func TestAPlatformWithoutARelease(t *testing.T) {
	i := &cloudflared.Installer{Dir: t.TempDir(), GOOS: "freebsd", GOARCH: "riscv64"}
	if _, err := i.Ensure(context.Background(), nil); !errors.Is(err, cloudflared.ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}

func TestThePinnedReleasesCoverThePlannedPlatforms(t *testing.T) {
	for _, p := range [][2]string{{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "arm64"}, {"linux", "amd64"}, {"windows", "amd64"}} {
		a, ok := cloudflared.AssetFor(p[0], p[1])
		if !ok || !strings.HasPrefix(a.URL, "https://github.com/cloudflare/cloudflared/releases/download/"+cloudflared.Version+"/") || len(a.SHA256) != 64 || a.Tgz != (p[0] == "darwin") {
			t.Errorf("%s/%s: %+v", p[0], p[1], a)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"cloudflared version 2026.11.2 (built 2026-11-23T11:41:20Z)": "2026.11.2",
		"cloudflared version 2026.01.0":                              "2026.1.0",
	} {
		if got, ok := cloudflared.ParseVersion(in); !ok || got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	if _, ok := cloudflared.ParseVersion("no version here"); ok {
		t.Error("found a version in nothing")
	}
}
