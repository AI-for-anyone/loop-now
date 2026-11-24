package codexcli_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/codexcli"
	"loop-now/internal/testutil/fakecodexbin"
)

func TestVersionAndLogin(t *testing.T) {
	bin := fakecodexbin.Path(t)
	home := t.TempDir()
	env := codexcli.Env(codexcli.EnvOptions{Home: t.TempDir(), CodexHome: home, Path: []string{"/usr/bin", "/bin"}})
	ctx := context.Background()
	v, err := codexcli.Version(ctx, bin, env)
	if err != nil || v != "0.167.0" {
		t.Fatalf("version: %q %v", v, err)
	}
	login, err := codexcli.LoginStatus(ctx, bin, env)
	if err != nil || !login.LoggedIn || login.Method != "chatgpt" {
		t.Fatalf("logged in: %+v %v", login, err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fakecodex-logged-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if login, err := codexcli.LoginStatus(ctx, bin, env); err != nil || login.LoggedIn {
		t.Fatalf("logged out: %+v %v", login, err)
	}
}

// Only CODEX_HOME reaches codex: no OpenAI or Codex variable may change its login or configuration.
func TestEnv(t *testing.T) {
	env := codexcli.WithHome([]string{"PATH=/bin", "OPENAI_API_KEY=x", "OPENAI_BASE_URL=y", "CODEX_HOME=/elsewhere", "CODEX_API_KEY=z", "LANG=C"}, "/home/u/.codex")
	if !slices.Equal(env, []string{"PATH=/bin", "LANG=C", "CODEX_HOME=/home/u/.codex"}) {
		t.Fatalf("env: %v", env)
	}
	full := codexcli.Env(codexcli.EnvOptions{Home: "/home/u", CodexHome: "/home/u/.codex", User: "u", Path: []string{"/a", "/b"}, Extra: []string{"LANG=C", "HOME=/wrong", "OPENAI_API_KEY=x"}})
	joined := strings.Join(full, "\n")
	for _, want := range []string{"HOME=/home/u", "USER=u", "CODEX_HOME=/home/u/.codex", "LANG=C"} {
		if !slices.Contains(full, want) {
			t.Fatalf("missing %s in %v", want, full)
		}
	}
	if strings.Contains(joined, "HOME=/wrong") || strings.Contains(joined, "OPENAI_API_KEY") {
		t.Fatalf("env: %v", full)
	}
	if got := codexcli.Home(func(string) string { return "" }, "/home/u"); got != filepath.Join("/home/u", ".codex") {
		t.Fatal(got)
	}
	if got := codexcli.Home(func(k string) string { return map[string]string{"CODEX_HOME": "/custom"}[k] }, "/home/u"); got != "/custom" {
		t.Fatal(got)
	}
}

func TestAtLeast(t *testing.T) {
	for _, c := range []struct {
		v    string
		want bool
	}{{"0.167.0", true}, {"0.168.1", true}, {"1.0.0", true}, {"0.166.9", false}, {"0.99.0", false}, {"0.167.0-alpha.1", true}} {
		if got := codexcli.AtLeast(c.v, "0.167.0"); got != c.want {
			t.Errorf("%s: %v", c.v, got)
		}
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	if _, err := codexcli.Find([]string{dir}); err != codexcli.ErrNotFound {
		t.Fatal(err)
	}
	bin := fakecodexbin.Path(t)
	name := "codex"
	if filepath.Ext(bin) == ".exe" {
		name = "codex.exe"
	}
	if err := os.Symlink(bin, filepath.Join(dir, name)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got, err := codexcli.Find([]string{"relative", dir})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(bin)
	if got != want {
		t.Fatalf("%s != %s", got, want)
	}
}

// TestRealCodex checks the parsing against the Codex installed on this computer. It runs only when
// LOOP_NOW_TEST_CODEX_BIN names the codex executable (CI skips it); it prints no account details.
func TestRealCodex(t *testing.T) {
	bin := os.Getenv("LOOP_NOW_TEST_CODEX_BIN")
	if bin == "" {
		t.Skip("LOOP_NOW_TEST_CODEX_BIN is not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	env := codexcli.Env(codexcli.EnvOptions{Home: t.TempDir(), CodexHome: codexcli.Home(os.Getenv, home), Path: filepath.SplitList(os.Getenv("PATH"))})
	v, err := codexcli.Version(context.Background(), bin, env)
	if err != nil || !codexcli.AtLeast(v, "0.167.0") {
		t.Fatalf("version %q: %v", v, err)
	}
	login, err := codexcli.LoginStatus(context.Background(), bin, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("codex %s, logged in: %v, method: %s", v, login.LoggedIn, login.Method)
}
