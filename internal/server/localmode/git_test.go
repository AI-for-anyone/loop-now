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
		t.Skip("没有 git")
	}
	return git
}

func TestGitIdentityEnvUsesRealHomeAndNeverPrompts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME 的来源在 Windows 上是 USERPROFILE")
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
			t.Errorf("%s = %q（有=%v），应为 %q", k, got, ok, v)
		}
	}
	if _, ok := envValue(env, "XDG_CONFIG_HOME"); ok {
		t.Error("没设置的 XDG_CONFIG_HOME 不应出现")
	}
}

func TestGitIdentityEnvKeepsUsersSSHCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME 的来源在 Windows 上是 USERPROFILE")
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
		t.Fatalf("应沿用 core.sshCommand 并加 BatchMode：%q", got)
	}
	t.Setenv("GIT_SSH_COMMAND", "ssh -F /etc/custom")
	env, _ = GitIdentityEnv(context.Background(), git, "/usr/bin", nil, t.TempDir())
	if got, _ := envValue(env, "GIT_SSH_COMMAND"); got != "ssh -F /etc/custom -o BatchMode=yes" {
		t.Fatalf("进程环境里的 GIT_SSH_COMMAND 优先：%q", got)
	}
}

// TestScopedHelperOverridesOnlyGitHub 用真实 git 核对“按地址限定的凭证助手”的语义：命令行的
// credential.https://github.com.helper 先清空再只放 Agent 的助手，github.com 不再问本机的助手；其他主机仍用本机的。
// app 的 folderGitAccess 依赖这一点，让 Agent 的 gh 令牌不读、不写本机凭证库里 github.com 的条目。
func TestScopedHelperOverridesOnlyGitHub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("助手脚本用 sh 写")
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
			t.Fatalf("credential fill %s：%v：%s", host, err, out)
		}
		return string(out)
	}
	if out := fill("github.com"); !strings.Contains(out, "password=agent-token") {
		t.Fatalf("github.com 应只用 Agent 的助手：%s", out)
	}
	if b, _ := os.ReadFile(log); len(b) != 0 {
		t.Fatalf("github.com 不应调用本机的助手：%q", b)
	}
	if out := fill("code.example.com"); !strings.Contains(out, "password=user-secret") {
		t.Fatalf("其他主机应用本机的助手：%s", out)
	}
	if b, _ := os.ReadFile(log); !slices.Contains(strings.Fields(string(b)), "get") {
		t.Fatalf("其他主机应调用本机的助手：%q", b)
	}
}
