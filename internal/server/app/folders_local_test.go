package app

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestGitPullEnv(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ghConfig := "GH_CONFIG_DIR=" + filepath.Join(home, ".config", "gh")
	helper := "!'/cli/gh' auth git-credential"
	gh := []string{"GH_TOKEN=synthetic"}

	// git 开关关闭：与原来相同，Agent 的独立 HOME，先清空本机助手再只用 gh。
	off := gitPullEnv(home, "/usr/bin:/bin", gh, helper, false, nil)
	if !slices.Equal(off.Env, []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", ghConfig, "GH_TOKEN=synthetic"}) ||
		!slices.Equal(off.Args, []string{"-c", "credential.helper=", "-c", "credential.helper=" + helper}) {
		t.Fatalf("关闭时：%+v", off)
	}
	if anon := gitPullEnv(home, "/usr/bin", nil, "", false, nil); !slices.Equal(anon.Args, []string{"-c", "credential.helper="}) {
		t.Fatalf("没有 gh 时匿名：%+v", anon)
	}

	// git 开关开启：用本机的环境，不清空本机助手；gh 只用于 github.com。
	local := []string{"HOME=/Users/me", "SSH_AUTH_SOCK=/tmp/agent.sock", "GIT_TERMINAL_PROMPT=0"}
	on := gitPullEnv(home, "/usr/bin", gh, helper, true, local)
	if !slices.Equal(on.Env, append(slices.Clone(local), ghConfig, "GH_TOKEN=synthetic")) ||
		!slices.Equal(on.Args, []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=" + helper}) {
		t.Fatalf("开启时：%+v", on)
	}
	if plain := gitPullEnv(home, "/usr/bin", nil, "", true, local); len(plain.Args) != 0 || slices.Contains(plain.Env, "GH_TOKEN=synthetic") {
		t.Fatalf("开启且没有 gh 时不加任何助手参数：%+v", plain)
	}
	if &on.Env[0] == &local[0] {
		t.Fatal("不应改写传入的 localEnv")
	}
}
