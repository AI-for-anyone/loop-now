package gcloudcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlledArguments(t *testing.T) {
	for _, args := range [][]string{{"--verbosity=info", "auth", "login"}, {"--project=p", "beta", "auth", "login"}, {"alpha", "--project", "p", "config", "set", "account", "x"}, {"projects", "list", "--authorization-token-file=x"}, {"projects", "list", "--authority-selector=x"}, {"--verbosity", "info", "auth", "login"}, {"--project", "p", "auth", "login"}, {"beta", "--verbosity", "info", "auth", "login"}, {"--unknown", "p", "auth", "login"}, {"--format", "json", "config", "set", "account", "x"}, {"beta", "auth", "login"}, {"alpha", "config", "set", "account", "x"}, {"auth", "login"}, {"config", "set", "account", "x"}, {"projects", "list", "--account=x"}, {"run", "deploy", "--credential-file-override", "x"}, {"projects", "list", "--verbosity=debug"}, {"--configuration=x", "projects", "list"}, {"projects", "list", "--flags-file=x"}, {"projects", "list", "--log-http"}} {
		if Allowed(args) {
			t.Fatal("authentication override accepted")
		}
	}
	for _, args := range [][]string{{"--project", "p", "projects", "list"}, {"--verbosity", "info", "beta", "projects", "list"}, {"projects", "list", "--format=json"}, {"run", "deploy", "service", "--project=fixture", "--quiet"}, {"builds", "list", "--verbosity=warning"}, {"--version"}} {
		if !Allowed(args) {
			t.Fatal("normal native command rejected")
		}
	}
}

// TestRunSelfContained 验证 Linux 式自包含安装的执行路径：没有 Python.framework 时
// 直接执行 google-cloud-sdk/bin/gcloud，并沿用凭证与参数白名单约束。
func TestRunSelfContained(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "runtime")
	bin := filepath.Join(root, "google-cloud-sdk", "bin", "gcloud")
	if e := os.MkdirAll(filepath.Dir(bin), 0755); e != nil {
		t.Fatal(e)
	}
	body := []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Google Cloud SDK 568.0.0'; exit 0; fi\necho ran \"$@\"\n")
	if e := os.WriteFile(bin, body, 0755); e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "home")
	credential := filepath.Join(home, ".loop-now", "gcloud", "service-account.json")
	if e := os.MkdirAll(filepath.Dir(credential), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(credential, []byte(`{"type":"service_account"}`), 0600); e != nil {
		t.Fatal(e)
	}
	getenv := func(key string) string {
		if key == "HOME" {
			return home
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{root, "--version"}, Options{Getenv: getenv, Stdout: &stdout, Stderr: &stderr})
	if code != 0 || !strings.Contains(stdout.String(), "Google Cloud SDK 568.0.0") {
		t.Fatal("self-contained --version", code, stdout.String(), stderr.String())
	}
	if code := Run(context.Background(), []string{root, "auth", "login"}, Options{Getenv: getenv, Stdout: &stdout, Stderr: &stderr}); code != 2 {
		t.Fatal("auth group must stay blocked on self-contained runtime", code)
	}
	if code := Run(context.Background(), []string{"relative-root", "--version"}, Options{Getenv: getenv, Stdout: &stdout, Stderr: &stderr}); code != 2 {
		t.Fatal("relative runtime root accepted", code)
	}
}
