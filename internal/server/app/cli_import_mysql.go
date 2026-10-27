package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"loop-now/internal/server/cliimport/source"
	"loop-now/internal/server/clitemplate"
)

// Only the pinned complete vendor bundle already prepared by Installer may
// support masked login-path discovery. Scan never downloads or executes a host
// binary, and a missing/bad bundle leaves that source precisely unavailable.
func configureImportMySQLTools(ctx context.Context, r *source.Reader, home, workRoot string, catalog *clitemplate.Catalog) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	if _, e := os.Lstat(filepath.Join(home, ".mylogin.cnf")); e != nil {
		return
	}
	t, ok := catalog.Get("mysql")
	if !ok || t.Version != "8.4.9" {
		return
	}
	a, ok := t.Asset()
	if !ok || a.SHA256 != "5e1e11782219f81e2246f64f5515af451c36c1167c8158760e17a6854e6e8971" || a.Path != "mysql-8.4.9-macos15-arm64/bin/mysql" || a.Layout != "tree" {
		return
	}
	install := filepath.Join(workRoot, "tools", "mysql", "8.4.9")
	marker := filepath.Join(install, ".bundle-ready.json")
	info, e := os.Lstat(marker)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return
	}
	f, e := os.Open(marker)
	if e != nil {
		return
	}
	raw, e := io.ReadAll(io.LimitReader(f, 4097))
	f.Close()
	if e != nil || len(raw) > 4096 {
		return
	}
	var ready struct{ SHA256, Version, Path string }
	if json.Unmarshal(raw, &ready) != nil || ready.SHA256 != a.SHA256 || ready.Version != t.Version || ready.Path != a.Path {
		return
	}
	_ = r.SetMySQLTools(ctx, source.MySQLTools{Root: filepath.Join(install, "runtime", "mysql-8.4.9-macos15-arm64"), EditorSHA256: "78a1335b071070867b23425226ed3aef5b79a0411241fe3b1f3105e1f434613d", PrinterSHA256: "af012462d8086fe8f1c5820b85123428b02c2594b94311399f28d3f477539cb3"})
}
