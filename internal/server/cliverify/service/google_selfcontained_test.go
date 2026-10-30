package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loop-now/internal/server/clitemplate"
)

func TestGoogleVersionSpecSelfContained(t *testing.T) {
	catalog, e := clitemplate.Load("")
	if e != nil {
		t.Fatal(e)
	}
	tmpl, _ := catalog.Get("gcloud")
	installer := clitemplate.NewInstaller(t.TempDir(), 0, nil)
	s := &Service{Installer: installer}
	sdk, fw := installer.GoogleRuntimePaths(tmpl)

	if e := os.MkdirAll(filepath.Dir(sdk), 0755); e != nil {
		t.Fatal(e)
	}
	ready := filepath.Join(filepath.Dir(sdk), "..", ".google-ready.json")
	// pythonSha256 为空的安装标记表示 Linux 自包含运行树。
	if e := os.WriteFile(ready, []byte(`{"pythonSha256":""}`), 0600); e != nil {
		t.Fatal(e)
	}
	spec, err := s.googleVersionSpec(t.TempDir(), nil, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Executable != filepath.Join(sdk, "bin", "gcloud") {
		t.Fatal("self-contained version spec must exec bin/gcloud", spec.Executable)
	}
	if strings.Contains(strings.Join(spec.Environment, "\n"), "DYLD_") {
		t.Fatal("self-contained version spec leaked macOS framework env")
	}

	// 标记带固定 Python 校验值时保持 macOS 解释路径，不探测 framework 目录。
	if e := os.WriteFile(ready, []byte(`{"pythonSha256":"pinned"}`), 0600); e != nil {
		t.Fatal(e)
	}
	spec, err = s.googleVersionSpec(t.TempDir(), nil, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Executable != filepath.Join(fw, "Versions/3.13/bin/python3.13") {
		t.Fatal("framework runtime must keep private python entry", spec.Executable)
	}
	if e := os.Remove(ready); e != nil {
		t.Fatal(e)
	}
	if _, err = s.googleVersionSpec(t.TempDir(), nil, tmpl); err == nil {
		t.Fatal("missing readiness marker must not fall back to filesystem probing")
	}
}
