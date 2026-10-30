package clitemplate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOfficialGoogleBundleOffline(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("reviewed official bundle is darwin-arm64 only")
	}
	downloads, e := filepath.Abs("../../../.local-build/cli-installation-proof/downloads")
	if e != nil {
		t.Fatal(e)
	}
	files := map[string]string{"/sdk": "google-cloud-cli-568.0.0-darwin-arm.tar.gz", "/python": "python-3.13.7-macos11.tar.gz"}
	for _, name := range files {
		if _, e = os.Stat(filepath.Join(downloads, name)); e != nil {
			t.Fatal("reviewed local fixture missing")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(downloads, name))
	}))
	defer server.Close()
	catalog, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	original, _ := catalog.Get("gcloud")
	tmpl := *original
	a, _ := tmpl.Asset()
	a.Dependencies = append([]Asset(nil), a.Dependencies...)
	a.URL = server.URL + "/sdk"
	a.Dependencies[0].URL = server.URL + "/python"
	tmpl.Install = map[string]Asset{Platform(): a}
	installer := NewInstaller(t.TempDir(), 2*time.Minute, server.Client())
	_, attempt, e := installer.EnsureReport(context.Background(), &tmpl)
	if e != nil || !attempt {
		t.Fatal("complete private install", e)
	}
	_, attempt, e = installer.EnsureReport(context.Background(), &tmpl)
	if e != nil || attempt {
		t.Fatal("second install", e)
	}
	sdk, fw := installer.GoogleRuntimePaths(&tmpl)
	if _, e = os.Stat(filepath.Join(sdk, "lib/third_party/google/auth/__init__.py")); e != nil {
		t.Fatal("SDK dependency missing", e)
	}
	if e = os.Remove(filepath.Join(fw, "Versions/3.13/lib/python3.13/lib-dynload/_ssl.cpython-313-darwin.so")); e != nil {
		t.Fatal(e)
	}
	if installer.installed(&tmpl) {
		t.Fatal("missing required Python native module accepted")
	}
}

// TestGoogleBundleSelfContainedOffline 验证 Linux 式自包含安装：无 python_framework 依赖的
// google_sdk 资产直接解包整树并用 bin/gcloud --version 验证，不下载私有 Python。
func TestGoogleBundleSelfContainedOffline(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho 'Google Cloud SDK 568.0.0'\n")
	if e := tw.WriteHeader(&tar.Header{Name: "google-cloud-sdk/bin/gcloud", Mode: 0755, Size: int64(len(body))}); e != nil {
		t.Fatal(e)
	}
	if _, e := tw.Write(body); e != nil {
		t.Fatal(e)
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(buf.Bytes())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk" {
			http.NotFound(w, r)
			return
		}
		w.Write(buf.Bytes())
	}))
	defer server.Close()

	catalog, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	original, _ := catalog.Get("gcloud")
	tmpl := *original
	tmpl.Install = map[string]Asset{Platform(): {
		URL:    server.URL + "/sdk",
		SHA256: fmt.Sprintf("%x", sum),
		Format: "tar.gz",
		Path:   "google-cloud-sdk/bin/gcloud",
		Layout: "google_sdk",
	}}
	installer := NewInstaller(t.TempDir(), 2*time.Minute, server.Client())
	if _, attempt, e := installer.EnsureReport(context.Background(), &tmpl); e != nil || !attempt {
		t.Fatal("self-contained install", e, attempt)
	}
	if _, attempt, e := installer.EnsureReport(context.Background(), &tmpl); e != nil || attempt {
		t.Fatal("second self-contained install", e, attempt)
	}
	sdk, fw := installer.GoogleRuntimePaths(&tmpl)
	if _, e := os.Stat(filepath.Join(sdk, "bin/gcloud")); e != nil {
		t.Fatal("SDK entry missing", e)
	}
	if _, e := os.Stat(fw); !os.IsNotExist(e) {
		t.Fatal("self-contained install must not ship a private Python.framework", e)
	}
	if !installer.installed(&tmpl) {
		t.Fatal("self-contained bundle not reported installed")
	}
	if e := os.Remove(filepath.Join(sdk, "bin/gcloud")); e != nil {
		t.Fatal(e)
	}
	if installer.installed(&tmpl) {
		t.Fatal("broken self-contained bundle accepted")
	}
}
