package folders_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/folders"
)

// 拉取用 GitAccess 给出的环境与 clone 前的参数；SSH 主机未确认时不等待、不询问，直接以 host_key_unverified 失败并说明怎么确认。
func TestGitPullUsesAccessAndReportsHostKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("假 ssh 是 sh 脚本")
	}
	ctx := context.Background()
	f := newFixture(t, "git", 20*time.Second)
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "ssh-args")
	fakeSSH := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\necho \"$@\" > '" + argsLog + "'\n" +
		"echo 'No ED25519 host key is known for code.example.com and you have requested strict checking.' >&2\n" +
		"echo 'Host key verification failed.' >&2\nexit 255\n"
	if err := os.WriteFile(fakeSSH, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	gotAgent := make(chan uuid.UUID, 1)
	f.svc.Git = func(_ context.Context, agentID uuid.UUID) (folders.GitEnv, error) {
		gotAgent <- agentID
		return folders.GitEnv{
			Env:  []string{"HOME=" + dir, "PATH=" + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GIT_SSH_COMMAND=" + fakeSSH + " -o BatchMode=yes"},
			Args: []string{"-c", "credential.https://github.com.helper="},
		}, nil
	}
	p, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: uuid.New(), URL: "git@code.example.com:team/app.git", Branch: "main", Path: "app"})
	if err != nil {
		t.Fatal(err)
	}
	done := f.wait(p.ID)
	if done.Status != "failed" || done.ErrorCode == nil || *done.ErrorCode != "host_key_unverified" || !strings.Contains(*done.ErrorMessage, "ssh -T git@code.example.com") {
		t.Fatalf("结果：%+v %s", done, deref(done.ErrorMessage))
	}
	if got := <-gotAgent; got != f.agent {
		t.Fatalf("GitAccess 收到的 Agent = %s", got)
	}
	if b, _ := os.ReadFile(argsLog); !strings.Contains(string(b), "BatchMode=yes") || !strings.Contains(string(b), "git@code.example.com") {
		t.Fatalf("应经由 GIT_SSH_COMMAND 运行 ssh：%q", b)
	}
}

// HTTPS 被要求认证而又没有可用凭证：不弹提示，报 auth_failed（页面据此提示打开“用本机的 git 身份”）。
func TestGitPullReportsAuthFailure(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	f := newFixture(t, "git", 20*time.Second)
	home := t.TempDir()
	f.svc.Git = func(context.Context, uuid.UUID) (folders.GitEnv, error) {
		return folders.GitEnv{Env: []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="}, Args: []string{"-c", "credential.helper="}}, nil
	}
	p, err := f.svc.StartGitPull(ctx, f.agent, folders.GitPullInput{ActionID: uuid.New(), URL: srv.URL + "/team/app.git", Branch: "main", Path: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.wait(p.ID); done.Status != "failed" || *done.ErrorCode != "auth_failed" {
		t.Fatalf("结果：%+v %s", done, deref(done.ErrorMessage))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
