package preflight

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareWorkRootCreatesPrivateDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "a", "loop-now")
	got, err := PrepareWorkRoot(root, []string{filepath.Join(base, "fakehome")})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(got)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("工作根目录应被创建且权限为 0700：%v %v", fi.Mode(), err)
	}
}

func TestPrepareWorkRootRejectsHome(t *testing.T) {
	home := t.TempDir()
	_, err := PrepareWorkRoot(filepath.Join(home, "work", "loop-now"), []string{home})
	if err == nil || !strings.Contains(err.Error(), "home") {
		t.Fatalf("位于 home 之内应拒绝：%v", err)
	}
}

func TestPrepareWorkRootRejectsAncestorContextFile(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareWorkRoot(filepath.Join(base, "x", "loop-now"), []string{"/nonexistent-home"})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(base, "AGENTS.md")) {
		t.Fatalf("上级目录有 AGENTS.md 应拒绝并给出路径：%v", err)
	}
}

func TestPiVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "pi")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 1.2.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := checkPiVersion(context.Background(), fake, "1.1.0", "/usr/bin:/bin")
	if err == nil || !strings.Contains(err.Error(), "1.1.0") || !strings.Contains(err.Error(), "1.2.0") {
		t.Fatalf("应报告期望版本与实际版本：%v", err)
	}
	if err := checkPiVersion(context.Background(), fake, "1.2.0", "/usr/bin:/bin"); err != nil {
		t.Fatalf("版本一致时应通过：%v", err)
	}
}

func TestCheckRedisUnavailable(t *testing.T) {
	err := CheckRedis(context.Background(), "redis://127.0.0.1:1/0")
	if err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("Redis 不可用时应指出 Redis：%v", err)
	}
}

func TestDeriveExecBasePathOrder(t *testing.T) {
	p := DeriveExecBasePath("/usr/bin/true", "/usr/bin/git")
	parts := strings.Split(p, ":")
	if parts[len(parts)-1] != "/sbin" {
		t.Fatalf("系统目录应在最后：%s", p)
	}
	idxSys := strings.Index(p, "/usr/bin")
	for _, d := range HostCommonDirs {
		if i := strings.Index(p, d); i >= 0 && i > idxSys {
			t.Fatalf("宿主常用目录 %s 应在系统目录之前：%s", d, p)
		}
	}
}

func TestResolveWithLocalPi(t *testing.T) {
	if _, err := lookPath("pi"); err != nil {
		t.Skip("本机没有 pi")
	}
	r, err := Resolve(context.Background(), Options{
		PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "root"), HomeDirs: []string{"/nonexistent-home"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.PiAIDir, "dist", "providers", "all.js")); err != nil {
		t.Fatalf("推导的 pi-ai 目录不对：%s", r.PiAIDir)
	}
	if !strings.HasSuffix(r.ExecBasePath, "/usr/bin:/bin:/usr/sbin:/sbin") {
		t.Fatalf("执行 PATH 不对：%s", r.ExecBasePath)
	}
}
