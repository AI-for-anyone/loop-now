package clitemplate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBundleRejectsEscapeAndDuplicate(t *testing.T) {
	for _, entries := range [][]tar.Header{{{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "."}, {Name: "a/b", Typeflag: tar.TypeSymlink, Linkname: "../outside"}}, {{Name: "../outside", Mode: 0600, Size: 1}}, {{Name: "lib/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}}, {{Name: "same", Mode: 0600, Size: 1}, {Name: "same", Mode: 0600, Size: 1}}} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, h := range entries {
			tw.WriteHeader(&h)
			if h.Size > 0 {
				tw.Write([]byte("x"))
			}
		}
		tw.Close()
		gz.Close()
		file := filepath.Join(t.TempDir(), "bad.tar.gz")
		os.WriteFile(file, buf.Bytes(), 0600)
		if err := extractTree(file, "tar.gz", t.TempDir()); err == nil {
			t.Fatal("unsafe tree accepted")
		}
	}
}
func TestOfficialDatabaseBundlesOffline(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("official tree proof is darwin-arm64 only")
	}
	catalog, e := Load("")
	if e != nil {
		t.Fatal(e)
	}
	downloads := filepath.Join("..", "..", "..", ".local-build", "cli-installation-proof", "downloads")
	files := map[string]string{"psql": "postgresql-18.3-1-osx-binaries.zip", "mysql": "mysql-8.4.9-macos15-arm64.tar.gz"}
	for cli, file := range files {
		t.Run(cli, func(t *testing.T) {
			full, e := filepath.Abs(filepath.Join(downloads, file))
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(full); e != nil {
				t.Skip("optional reviewed vendor package unavailable")
			}
			// Serve reviewed bytes from a self-owned loopback server; no vendor requests.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, full) }))
			defer server.Close()
			original, _ := catalog.Get(cli)
			tmpl := *original
			tmpl.Install = map[string]Asset{}
			a, _ := original.Asset()
			a.URL = server.URL + "/archive"
			tmpl.Install[Platform()] = a
			installer := NewInstaller(t.TempDir(), time.Minute, server.Client())
			dir, attempt, e := installer.EnsureReport(context.Background(), &tmpl)
			if e != nil || !attempt {
				t.Fatal("complete install", e)
			}
			if _, e = os.Stat(filepath.Join(dir, cli)); e != nil {
				t.Fatal(e)
			}
			_, attempt, e = installer.EnsureReport(context.Background(), &tmpl)
			if e != nil || attempt {
				t.Fatal("second install", e)
			}
			native := filepath.Join(installer.toolsDir, cli, tmpl.Version, "runtime", filepath.FromSlash(a.Path))
			// A ready marker cannot override pinned archive identity, native version,
			// or required vendor dylibs. All faults affect only this temp install.
			badAsset := a
			badAsset.SHA256 = strings.Repeat("0", 64)
			tmpl.Install[Platform()] = badAsset
			if installer.installed(&tmpl) {
				t.Fatal("SHA drift accepted")
			}
			tmpl.Install[Platform()] = a
			if cli == "mysql" {
				library := filepath.Join(filepath.Dir(filepath.Dir(native)), "lib", "libssl.3.dylib")
				if e = os.Rename(library, library+".held"); e != nil {
					t.Fatal(e)
				}
				if installer.installed(&tmpl) {
					t.Fatal("missing vendor dylib accepted")
				}
				if e = os.Rename(library+".held", library); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.Rename(native, native+".held"); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(native, []byte("#!/bin/sh\necho wrong-version\n"), 0700); e != nil {
				t.Fatal(e)
			}
			if installer.installed(&tmpl) {
				t.Fatal("wrong native version accepted")
			}
			if e = os.Remove(native); e != nil {
				t.Fatal(e)
			}
			if e = os.Rename(native+".held", native); e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(native); e != nil {
				t.Fatal(e)
			}
			if installer.installed(&tmpl) {
				t.Fatal("missing native tree still installed")
			}
		})
	}
}

func TestBundleAllowsForwardRelativeVendorLinks(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []tar.Header{{Name: "lib/liba.dylib", Typeflag: tar.TypeSymlink, Linkname: "libb.dylib"}, {Name: "lib/libb.dylib", Typeflag: tar.TypeSymlink, Linkname: "real.dylib"}, {Name: "lib/real.dylib", Mode: 0600, Size: 1}}
	for _, h := range entries {
		if e := tw.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			tw.Write([]byte("x"))
		}
	}
	tw.Close()
	gz.Close()
	archive := filepath.Join(t.TempDir(), "safe.tar.gz")
	os.WriteFile(archive, buf.Bytes(), 0600)
	root := t.TempDir()
	if e := extractTree(archive, "tar.gz", root); e != nil {
		t.Fatal("safe vendor link chain rejected", e)
	}
	b, e := os.ReadFile(filepath.Join(root, "lib/liba.dylib"))
	if e != nil || string(b) != "x" {
		t.Fatal("forward link did not resolve", e)
	}
}
func TestBundleRejectsLateSymlinkTargetEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	os.Mkdir(root, 0700)
	os.WriteFile(filepath.Join(base, "outside"), []byte("outside-marker"), 0600)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range []tar.Header{{Name: "a", Typeflag: tar.TypeSymlink, Linkname: "sub/../outside"}, {Name: "sub", Typeflag: tar.TypeSymlink, Linkname: "."}} {
		tw.WriteHeader(&h)
	}
	tw.Close()
	gz.Close()
	archive := filepath.Join(base, "late.tar.gz")
	os.WriteFile(archive, buf.Bytes(), 0600)
	if e := extractTree(archive, "tar.gz", root); e == nil {
		t.Fatal("actual late target escaped tree")
	}
	marker, e := os.ReadFile(filepath.Join(base, "outside"))
	if e != nil || string(marker) != "outside-marker" {
		t.Fatal("outside fixture mutated")
	}
}
