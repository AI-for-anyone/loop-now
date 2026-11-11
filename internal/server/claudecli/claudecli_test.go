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

// Find 在给定目录里按顺序找 claude，跳过不可执行的，返回解析符号链接后的路径；找不到时为 ErrNotFound。
func TestFind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上按扩展名找，另行验证")
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
		t.Fatalf("Find：%q %v（期望 %q）", got, err, want)
	}
	if _, err := claudecli.Find([]string{notExec}); err != claudecli.ErrNotFound {
		t.Fatalf("找不到：%v", err)
	}
}

func TestStripEnvAndEnv(t *testing.T) {
	got := claudecli.StripEnv([]string{"HOME=/h", "ANTHROPIC_API_KEY=x", "ANTHROPIC_BASE_URL=y", "CLAUDECODE=1", "CLAUDE_CONFIG_DIR=z", "CLAUDE_CODE_OAUTH_TOKEN=t", "PATH=/bin"})
	if strings.Join(got, ",") != "HOME=/h,PATH=/bin" {
		t.Fatalf("StripEnv：%v", got)
	}
	env := strings.Join(claudecli.Env("/home/u", "", []string{"/a", "/b"}), "\n")
	if !strings.Contains(env, "HOME=/home/u") || !strings.Contains(env, "PATH=/a"+string(os.PathListSeparator)+"/b") {
		t.Fatalf("Env：%s", env)
	}
}

// Version 与 AuthStatus 只运行 claude 自己的命令；AuthStatus 只取 loggedIn、authMethod、subscriptionType、loginExpiresAt 与
// projectsDirectory，邮箱与组织在解码时就丢掉；失败时的错误不带输出（错误输出里同样可能有邮箱）。
func TestVersionAndAuthStatus(t *testing.T) {
	bin := fakeclaudebin.Path(t)
	home := t.TempDir()
	env := claudecli.Env(home, "", []string{"/usr/bin", "/bin"})
	if v, err := claudecli.Version(context.Background(), bin, env); err != nil || v != "9.9.9" {
		t.Fatalf("版本：%q %v", v, err)
	}
	a, err := claudecli.AuthStatus(context.Background(), bin, env)
	if err != nil || !a.LoggedIn || a.AuthMethod != "claude.ai" || a.SubscriptionType != "max" || a.ProjectsDirectory != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("auth status：%+v %v", a, err)
	}
	b, _ := json.Marshal(a)
	for _, secret := range []string{"private@example.com", "Private Org", "org-private-id"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("不应保留 %q：%s", secret, b)
		}
	}
	_ = os.WriteFile(filepath.Join(home, ".fakeclaude-logged-out"), nil, 0o600)
	if a, err = claudecli.AuthStatus(context.Background(), bin, env); err != nil || a.LoggedIn {
		t.Fatalf("未登录：%+v %v", a, err)
	}
	failing := append(env, "FAKECLAUDE_AUTH_FAIL=1")
	if _, err = claudecli.AuthStatus(context.Background(), bin, failing); err == nil || strings.Contains(err.Error(), "private@example.com") {
		t.Fatalf("失败时不带输出：%v", err)
	}
	var probe claudecli.Probe
	if dir := probe.ProjectsDir(context.Background(), bin, env, home); dir != filepath.Join(home, ".claude", "projects") {
		t.Fatalf("projects 目录：%s", dir)
	}
}

// 会话记录的位置：projects 目录下一层子目录里的 <会话 ID>.jsonl。
func TestTranscriptPaths(t *testing.T) {
	projects, cwd := t.TempDir(), t.TempDir()
	id := uuid.NewString()
	if claudecli.TranscriptPath(projects, cwd, id) != "" {
		t.Fatal("还没有时为空")
	}
	p := filepath.Join(projects, claudecli.ProjectDirName(cwd), id+".jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("{}\n"), 0o600)
	if got := claudecli.TranscriptPath(projects, cwd, id); got != p {
		t.Fatalf("TranscriptPath：%q", got)
	}
	if claudecli.TranscriptPath(projects, cwd, "../x") != "" || !claudecli.InProjects(projects, p) {
		t.Fatal("会话 ID 必须是 UUID；p 在 projects 下")
	}
	for _, bad := range []string{filepath.Join(projects, id+".jsonl"), filepath.Join(projects, "a", "b", id+".jsonl"), filepath.Join(projects, "a", "notes.jsonl"), filepath.Join(t.TempDir(), "a", id+".jsonl")} {
		if claudecli.InProjects(projects, bad) {
			t.Errorf("不应算作会话记录：%s", bad)
		}
	}
	if strings.ContainsAny(claudecli.ProjectDirName("/a b/c.d"), " /.") {
		t.Fatal("非字母数字都换成 -")
	}
}
