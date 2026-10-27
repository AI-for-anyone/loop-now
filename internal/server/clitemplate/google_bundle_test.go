package clitemplate

import (
	"context"
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
