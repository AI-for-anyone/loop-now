package claudecli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/claudecli"
	"loop-now/internal/testutil/fakeclaudebin"
)

// TestFind: Find looks for claude in the given directories in order, skips non-executable files and returns the path
// with symlinks resolved; when nothing is found it returns ErrNotFound.
func TestFind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows the lookup goes by extension; verified separately")
	}
	notExec, real, link := t.TempDir(), t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(notExec, "claude"), []byte("x"), 0o600)
	target := filepath.Join(real, "claude-2.1.0")
	_ = os.WriteFile(target, []byte("#!/bin/sh\n"), 0o700)
	if err := os.Symlink(target, filepath.Join(link, "claude")); err != nil {
		t.Fatal(err)
	}
	got, err := claudecli.Find([]string{"", "relative", notExec, link})
	want, _ := filepath.EvalSymlinks(target)
	if err != nil || got != want {
		t.Fatalf("Find: %q %v (want %q)", got, err, want)
	}
	if _, err := claudecli.Find([]string{notExec}); err != claudecli.ErrNotFound {
		t.Fatalf("not found: %v", err)
	}
}

func TestStripEnvAndEnv(t *testing.T) {
	got := claudecli.StripEnv([]string{"HOME=/h", "ANTHROPIC_API_KEY=x", "ANTHROPIC_BASE_URL=y", "CLAUDECODE=1", "CLAUDE_CONFIG_DIR=z", "CLAUDE_CODE_OAUTH_TOKEN=t", "PATH=/bin"})
	if strings.Join(got, ",") != "HOME=/h,PATH=/bin" {
		t.Fatalf("StripEnv: %v", got)
	}
	env := strings.Join(claudecli.Env(claudecli.EnvOptions{Home: "/home/u", User: "u", Path: []string{"/a", "/b"},
		Extra: []string{"LANG=C.UTF-8", "HOME=/evil", "USER=evil", "ANTHROPIC_API_KEY=x"}}), "\n")
	for _, want := range []string{"HOME=/home/u", "PATH=/a" + string(os.PathListSeparator) + "/b", "USER=u", "LOGNAME=u", "LANG=C.UTF-8"} {
		if !strings.Contains(env, want) {
			t.Fatalf("Env is missing %s: %s", want, env)
		}
	}
	if strings.Contains(env, "evil") || strings.Contains(env, "ANTHROPIC") {
		t.Fatalf("Extra must not override base variables or carry Anthropic variables: %s", env)
	}
}

// TestVersionAndAuthStatus: Version and AuthStatus only run claude's own commands; AuthStatus only takes loggedIn,
// authMethod, subscriptionType, loginExpiresAt and projectsDirectory, and drops the email and organization during
// decoding; errors on failure carry no output (the error output may contain the email too).
func TestVersionAndAuthStatus(t *testing.T) {
	bin := fakeclaudebin.Path(t)
	home := t.TempDir()
	env := claudecli.Env(claudecli.EnvOptions{Home: home, User: "tester", Path: []string{"/usr/bin", "/bin"}})
	if v, err := claudecli.Version(context.Background(), bin, env); err != nil || v != "9.9.9" {
		t.Fatalf("version: %q %v", v, err)
	}
	a, err := claudecli.AuthStatus(context.Background(), bin, env)
	if err != nil || !a.LoggedIn || a.AuthMethod != "claude.ai" || a.SubscriptionType != "max" || a.ProjectsDirectory != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("auth status: %+v %v", a, err)
	}
	b, _ := json.Marshal(a)
	for _, secret := range []string{"private@example.com", "Private Org", "org-private-id"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%q should not be kept: %s", secret, b)
		}
	}
	// Like the real claude: without USER it cannot find the login and exits with 1 but still prints JSON; going by the
	// output this is "not logged in", not an error.
	noUser := claudecli.Env(claudecli.EnvOptions{Home: home, Path: []string{"/usr/bin", "/bin"}})
	if a, err = claudecli.AuthStatus(context.Background(), bin, noUser); err != nil || a.LoggedIn || a.AuthMethod != "none" {
		t.Fatalf("without USER: %+v %v", a, err)
	}
	_ = os.WriteFile(filepath.Join(home, ".fakeclaude-logged-out"), nil, 0o600)
	if a, err = claudecli.AuthStatus(context.Background(), bin, env); err != nil || a.LoggedIn {
		t.Fatalf("not logged in: %+v %v", a, err)
	}
	_ = os.Remove(filepath.Join(home, ".fakeclaude-logged-out"))
	failing := append(env, "FAKECLAUDE_AUTH_FAIL=1")
	if _, err = claudecli.AuthStatus(context.Background(), bin, failing); err == nil || strings.Contains(err.Error(), "private@example.com") {
		t.Fatalf("the error on failure should carry no output: %v", err)
	}
	var probe claudecli.Probe
	if dir := probe.ProjectsDir(context.Background(), bin, env, home); dir != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("projects directory: %s", dir)
	}
}

// TestTranscriptPaths covers where session transcripts live: <session ID>.jsonl in a direct subdirectory of the
// projects directory.
func TestTranscriptPaths(t *testing.T) {
	projects, cwd := t.TempDir(), t.TempDir()
	id := uuid.NewString()
	if claudecli.TranscriptPath(projects, cwd, id) != "" {
		t.Fatal("should be empty before the transcript exists")
	}
	p := filepath.Join(projects, claudecli.ProjectDirName(cwd), id+".jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("{}\n"), 0o600)
	if got := claudecli.TranscriptPath(projects, cwd, id); got != p {
		t.Fatalf("TranscriptPath: %q", got)
	}
	if claudecli.TranscriptPath(projects, cwd, "../x") != "" || !claudecli.InProjects(projects, p) {
		t.Fatal("the session ID must be a UUID; p should be under projects")
	}
	for _, bad := range []string{filepath.Join(projects, id+".jsonl"), filepath.Join(projects, "a", "b", id+".jsonl"), filepath.Join(projects, "a", "notes.jsonl"), filepath.Join(t.TempDir(), "a", id+".jsonl")} {
		if claudecli.InProjects(projects, bad) {
			t.Errorf("should not count as a session transcript: %s", bad)
		}
	}
	if strings.ContainsAny(claudecli.ProjectDirName("/a b/c.d"), " /.") {
		t.Fatal("every non-alphanumeric character should become -")
	}
}
