package preflight

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"loop-now/internal/privatefs"
)

func TestPrepareWorkRootCreatesPrivateDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "a", "loop-now")
	got, err := PrepareWorkRoot(root, []string{filepath.Join(base, "fakehome")})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(got)
	if err != nil || !fi.IsDir() || !privatefs.IsPrivate(got, fi) {
		t.Fatalf("the work root should be created and accessible only to the current user: %v %v", fi.Mode(), err)
	}
}

func TestPrepareWorkRootRejectsHome(t *testing.T) {
	home := t.TempDir()
	_, err := PrepareWorkRoot(filepath.Join(home, "work", "loop-now"), []string{home})
	if err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("a work root inside home should be rejected: %v", err)
	}
}

func TestPrepareWorkRootRejectsAncestorContextFile(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(base) // Windows temp dirs contain 8.3 short names; the error has the resolved path
	_, err := PrepareWorkRoot(filepath.Join(base, "x", "loop-now"), []string{filepath.Join(base, "nonexistent-home")})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(real, "AGENTS.md")) {
		t.Fatalf("an AGENTS.md in a parent directory should be rejected with its path: %v", err)
	}
}

func TestPiVersionMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake pi is a sh script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "pi")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 1.2.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := checkPiVersion(context.Background(), "", fake, "1.1.0", "/usr/bin:/bin")
	if err == nil || !strings.Contains(err.Error(), "1.1.0") || !strings.Contains(err.Error(), "1.2.0") {
		t.Fatalf("should report the expected and the actual version: %v", err)
	}
	if err := checkPiVersion(context.Background(), "", fake, "1.2.0", "/usr/bin:/bin"); err != nil {
		t.Fatalf("should pass when the versions match: %v", err)
	}
}

func TestDeriveExecBasePathOrder(t *testing.T) {
	p := DeriveExecBasePath(filepath.Join(SystemDirs[0], "true"), filepath.Join(SystemDirs[0], "git"))
	parts := strings.Split(p, string(os.PathListSeparator))
	if parts[len(parts)-1] != SystemDirs[len(SystemDirs)-1] {
		t.Fatalf("system directories should come last: %s", p)
	}
	idxSys := strings.Index(p, SystemDirs[0])
	for _, d := range HostCommonDirs {
		if i := strings.Index(p, d); i >= 0 && i > idxSys {
			t.Fatalf("common host directory %s should come before the system directories: %s", d, p)
		}
	}
}

// TestDeriveExecBasePathNodeFirst checks that node's directory comes first, so another node installed on the host does
// not shadow the configured one.
func TestDeriveExecBasePathNodeFirst(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := DeriveExecBasePath(node, filepath.Join(SystemDirs[0], "git")); !strings.HasPrefix(p, real+string(os.PathListSeparator)) {
		t.Fatalf("node's directory should come first: %s", p)
	}
}

func TestResolveWithLocalPi(t *testing.T) {
	if _, err := lookPath("pi"); err != nil {
		t.Skip("pi is not installed on this machine")
	}
	r, err := Resolve(context.Background(), Options{
		PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "root"), HomeDirs: []string{"/nonexistent-home"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.PiAIDir, "dist", "providers", "all.js")); err != nil {
		t.Fatalf("wrong derived pi-ai directory: %s", r.PiAIDir)
	}
	if !strings.HasSuffix(r.ExecBasePath, "/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Fatalf("wrong exec PATH: %s", r.ExecBasePath)
	}
}
