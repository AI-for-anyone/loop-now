package localmode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
)

// GitIdentityEnv 返回“用本机的 git 身份”时“从 Git 拉取”所用的环境：服务端进程用户真实的 HOME（Windows 上另有与之对应的
// USERPROFILE、APPDATA 等）、SSH_AUTH_SOCK 与 XDG_CONFIG_HOME，于是用户的 git 配置、credential helper、SSH 密钥与
// known_hosts 都可用；PATH 是执行 PATH 接上用户的 PATH（找得到装在用户目录里的 helper）。
//
// 拉取在后台进行、没有人能回答提问，所以关掉一切交互：保持 GIT_TERMINAL_PROMPT=0；GIT_ASKPASS 置空，git 不再退到
// core.askPass 与 SSH_ASKPASS；GCM_INTERACTIVE=never 让 Git Credential Manager 只用已存的凭证；SSH 以 BatchMode 运行，
// 不询问口令，也不替用户确认未知主机（直接以 “Host key verification failed” 失败）。用户自己配置的 ssh 命令
// （GIT_SSH_COMMAND 或 core.sshCommand）照用，只在后面加上 -o BatchMode=yes。
// dir 是查询 git 配置时的工作目录，应与克隆时一样不在任何仓库里。
func GitIdentityEnv(ctx context.Context, gitBin, basePath string, userPath []string, dir string) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("读取服务端进程用户的主目录失败：%w", err)
	}
	dirs := filepath.SplitList(basePath)
	seen := map[string]bool{}
	for _, d := range dirs {
		seen[execenv.PathKey(d)] = true
	}
	for _, d := range userPath {
		if k := execenv.PathKey(d); !seen[k] {
			seen[k] = true
			dirs = append(dirs, d)
		}
	}
	env := execenv.Base(home, os.TempDir())
	env = append(env, "HOME="+home, "PATH="+strings.Join(dirs, string(os.PathListSeparator)),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GCM_INTERACTIVE=never")
	for _, k := range []string{"SSH_AUTH_SOCK", "XDG_CONFIG_HOME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "GIT_SSH_COMMAND="+sshCommand(ctx, gitBin, env, dir)+" -o BatchMode=yes"), nil
}

// sshCommand 返回用户为 git 配置的 ssh 命令：服务端进程环境里的 GIT_SSH_COMMAND，其次是 git 配置的 core.sshCommand，都没有时为 ssh。
func sshCommand(ctx context.Context, gitBin string, env []string, dir string) string {
	if v := strings.TrimSpace(os.Getenv("GIT_SSH_COMMAND")); v != "" {
		return v
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBin, "config", "--get", "core.sshCommand")
	cmd.Env = env
	cmd.Dir = dir
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = time.Second
	if out, err := procgroup.Output(cmd); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return "ssh"
}
