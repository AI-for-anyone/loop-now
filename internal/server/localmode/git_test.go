package localmode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func gitOrSkip(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	return git
}

func TestGitIdentityEnvUsesRealHomeAndNeverPrompts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows HOME comes from USERPROFILE")
	}
	git := gitOrSkip(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	base := strings.Join([]string{"/opt/bundle/node", "/usr/bin"}, string(os.PathListSeparator))
	env, err := GitIdentityEnv(context.Background(), git, base, []string{"/usr/bin", "/Users/me/.local/bin"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HOME": home, "SSH_AUTH_SOCK": "/tmp/agent.sock", "GIT_TERMINAL_PROMPT": "0", "GIT_ASKPASS": "", "GCM_INTERACTIVE": "never",
		"PATH":            "/opt/bundle/node:/usr/bin:/Users/me/.local/bin",
		"GIT_SSH_COMMAND": "ssh -o BatchMode=yes",
	}
	for k, v := range want {
		if got, ok := envValue(env, k); !ok || got != v {
			t.Errorf("%s = %q (present=%v), want %q", k, got, ok, v)
		}
	}
	if _, ok := envValue(env, "XDG_CONFIG_HOME"); ok {
		t.Error("an unset XDG_CONFIG_HOME should not appear")
	}
}

func TestGitIdentityEnvKeepsUsersSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("on Windows HOME comes from USERPROFILE")
	}
	git := gitOrSkip(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_SSH_COMMAND", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[core]\n\tsshCommand = ssh -i ~/.ssh/work_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := GitIdentityEnv(context.Background(), git, "/usr/bin", nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := envValue(env, "GIT_SSH_COMMAND"); got != "ssh -i ~/.ssh/work_key -o BatchMode=yes" {
		t.Fatalf("should keep core.sshCommand and add BatchMode: %q", got)
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh -F /etc/custom")
	env, _ = GitIdentityEnv(context.Background(), git, "/usr/bin", nil, t.TempDir())
	if got, _ := envValue(env, "GIT_SSH_COMMAND"); got != "ssh -F /etc/custom -o BatchMode=yes" {
		t.Fatalf("GIT_SSH_COMMAND from the process environment should win: %q", got)
	}
}

// TestScopedHelperOverridesOnlyGitHub checks with real git the semantics of an "address-scoped credential helper": on
// the command line, credential.https://github.com.helper is first cleared and then set to only the Agent's helper, so
// github.com no longer asks the local helpers while other hosts still use them. folderGitAccess in app relies on this,
// so that the Agent's gh token neither reads nor writes the github.com entries in the local credential store.
func TestScopedHelperOverridesOnlyGitHub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper scripts are written in sh")
	}
	git := gitOrSkip(t)
	home := t.TempDir()
	log := filepath.Join(home, "user-helper.log")
	userHelper := filepath.Join(home, "user-helper.sh")
	agentHelper := filepath.Join(home, "agent-helper.sh")
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(userHelper, "echo \"$1\" >> '"+log+"'\n[ \"$1\" = get ] && { echo username=user; echo password=user-secret; }\nexit 0\n")
	write(agentHelper, "[ \"$1\" = get ] && { echo username=x-access-token; echo password=agent-token; }\nexit 0\n")
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[credential]\n\thelper = !'"+userHelper+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fill := func(host string) string {
		key := "credential.https://github.com.helper"
		cmd := exec.Command(git, "-c", key+"=", "-c", key+"=!'"+agentHelper+"'", "credential", "fill")
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="}
		cmd.Dir = home
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("credential fill %s: %v: %s", host, err, out)
		}
		return string(out)
	}
	if out := fill("github.com"); !strings.Contains(out, "password=agent-token") {
		t.Fatalf("github.com should use only the Agent's helper: %s", out)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("github.com should not call the local helper: %q", b)
	}
	if out := fill("code.example.com"); !strings.Contains(out, "password=user-secret") {
		t.Fatalf("other hosts should use the local helper: %s", out)
	}
	if b, _ := os.ReadFile(log); !slices.Contains(strings.Fields(string(b)), "get") {
		t.Fatalf("other hosts should call the local helper: %q", b)
	}
}
