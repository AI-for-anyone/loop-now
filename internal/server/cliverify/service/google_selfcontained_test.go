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

	// 没有私有 Python.framework 的 Linux 自包含运行树：bin/gcloud 直接执行。
	spec := s.googleVersionSpec(t.TempDir(), nil, tmpl)
	if spec.Executable != filepath.Join(sdk, "bin", "gcloud") {
		t.Fatal("self-contained version spec must exec bin/gcloud", spec.Executable)
	}
	if strings.Contains(strings.Join(spec.Environment, "\n"), "DYLD_") {
		t.Fatal("self-contained version spec leaked macOS framework env")
	}

	// 存在私有 Python.framework 的运行树保持 macOS 解释路径不变。
	if e := os.MkdirAll(filepath.Join(fw, "Versions/3.13/bin"), 0755); e != nil {
		t.Fatal(e)
	}
	spec = s.googleVersionSpec(t.TempDir(), nil, tmpl)
	if spec.Executable != filepath.Join(fw, "Versions/3.13/bin/python3.13") {
		t.Fatal("framework runtime must keep private python entry", spec.Executable)
	}
}
