package acpcli_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"loop-now/internal/server/acpcli"
	"loop-now/internal/testutil/fakeacpbin"
)

func env(home, flavor string) []string {
	return append(acpcli.Env(acpcli.EnvOptions{Home: home, Path: []string{"/usr/bin", "/bin"}}), "FAKEACP_FLAVOR="+flavor)
}

// The version, the login state and Antigravity's models come from the CLI's own answers.
func TestVersionLoginAndModels(t *testing.T) {
	bin := fakeacpbin.Path(t)
	ctx := context.Background()
	home := t.TempDir()
	if v, err := acpcli.Version(ctx, bin, env(home, "grok")); err != nil || v != "1.0.88" || !acpcli.AtLeast(v, "1.0.88") || acpcli.AtLeast(v, "1.1.0") {
		t.Fatalf("grok version: %q %v", v, err)
	}
	if v, err := acpcli.Version(ctx, bin, env(home, "agy")); err != nil || v != "1.5.14" {
		t.Fatalf("agy version: %q %v", v, err)
	}
	if got := acpcli.GrokLogin(ctx, bin, env(home, "grok")); got != acpcli.LoggedIn {
		t.Fatalf("grok logged in: %s", got)
	}
	models, err := acpcli.AntigravityModels(ctx, bin, env(home, "agy"))
	if err != nil || !slices.Equal(models, []acpcli.Model{{ID: "fake-high", Name: "Fake (High)"}, {ID: "fake-low", Name: "Fake (Low)"}}) {
		t.Fatalf("agy models: %+v %v", models, err)
	}
	if got := acpcli.AntigravityLogin(ctx, bin, env(home, "agy")); got != acpcli.LoggedIn {
		t.Fatalf("agy logged in: %s", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".fakeacp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fakeacp", ".logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := acpcli.GrokLogin(ctx, bin, env(home, "grok")); got != acpcli.LoggedOut {
		t.Fatalf("grok logged out: %s", got)
	}
	if got := acpcli.AntigravityLogin(ctx, bin, env(home, "agy")); got != acpcli.LoggedOut {
		t.Fatalf("agy logged out: %s", got)
	}
}

// The CLIs get no API key, so they use this computer's login.
func TestStripEnv(t *testing.T) {
	got := acpcli.StripEnv([]string{"HOME=/h", "XAI_API_KEY=x", "GROK_API_KEY=g", "GEMINI_API_KEY=m", "GOOGLE_API_KEY=o", "PATH=/bin"})
	if !slices.Equal(got, []string{"HOME=/h", "PATH=/bin"}) {
		t.Fatalf("stripped: %v", got)
	}
}

// Find takes an executable from the given directories only.
func TestFind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executables are named grok.exe or grok.cmd on Windows")
	}
	dir := t.TempDir()
	if _, err := acpcli.Find("grok", []string{dir, "relative"}); err == nil {
		t.Fatal("found nothing")
	}
	p := filepath.Join(dir, "grok")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(p)
	if got, err := acpcli.Find("grok", []string{"relative", dir}); err != nil || got != real {
		t.Fatalf("found: %q %v", got, err)
	}
}
