package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/folders"
	"loop-now/internal/server/localmode"
)

// githubCredentialScope 是 Agent 的 gh 凭证（GH_TOKEN，只对 github.com 有效）在“用本机的 git 身份”时专用的地址范围。
const githubCredentialScope = "https://github.com"

// gitLocalIdentity 是“用本机的 git 身份”所需的东西；nil 表示不启用本机模式。
type gitLocalIdentity struct {
	modes    *localmode.Service
	userPath *localmode.UserPath
	gitBin   string
}

// folderGitAccess 返回“从 Git 拉取”使用的环境。
//
// 本机模式的 git 开关关闭时与执行时克隆仓库相同：HOME 是 Agent 的独立目录，先清空本机配置的凭证助手，再用该 Agent 当前配置里的
// gh CLI 凭证（项目 GitHub 账号）作凭证助手；没有配置 gh 时匿名克隆。不继承服务端的其他环境变量。
//
// 开关开启时沿用服务端进程用户真实的 HOME、SSH_AUTH_SOCK、git 配置与凭证助手（localmode.GitIdentityEnv），SSH 地址与本机已存的
// HTTPS 凭证可用；Agent 配置了 gh 时，github.com 的 HTTPS 只用它的凭证：按地址限定的 credential.<url>.helper 先清空再只放 gh，
// 本机为 github.com 存的凭证不被读取，git 也不会把 Agent 的令牌存进（或在被拒时从）本机的凭证库里写入或删除。
func folderGitAccess(cfg *agentconfig.Service, templates *clitemplate.Catalog, installer *clitemplate.Installer, workRoot, basePath string, local *gitLocalIdentity) folders.GitAccess {
	return func(ctx context.Context, agentID uuid.UUID) (folders.GitEnv, error) {
		home := filepath.Join(folders.AgentRoot(workRoot, agentID), "home")
		if err := os.MkdirAll(home, 0o700); err != nil {
			return folders.GitEnv{}, err
		}
		useLocal := false
		if local != nil {
			settings, err := local.modes.Settings(ctx)
			if err != nil {
				return folders.GitEnv{}, err
			}
			useLocal = settings.Git
		}
		ghEnv, helper, err := folderGHCredential(ctx, cfg, templates, installer, agentID)
		if err != nil {
			return folders.GitEnv{}, err
		}
		var localEnv []string
		if useLocal {
			var userPath []string
			if local.userPath != nil {
				userPath = local.userPath.Dirs(ctx)
			}
			// 在 Agent 根目录查询 git 配置：与克隆所在的 .git-pulls 一样不在任何仓库里，读到的是同一份全局与系统配置。
			if localEnv, err = localmode.GitIdentityEnv(ctx, local.gitBin, basePath, userPath, folders.AgentRoot(workRoot, agentID)); err != nil {
				return folders.GitEnv{}, err
			}
		}
		return gitPullEnv(home, basePath, ghEnv, helper, useLocal, localEnv), nil
	}
}

// gitPullEnv 组合拉取的环境与 clone 前的参数。useLocal 为假时 HOME 是 Agent 的独立目录、先清空凭证助手再只用 gh；
// 为真时用 localEnv（本机的 git 身份），gh 的助手只用于 github.com。gh 的配置目录始终是 Agent 自己的。
func gitPullEnv(home, basePath string, ghEnv []string, helper string, useLocal bool, localEnv []string) folders.GitEnv {
	ghConfig := "GH_CONFIG_DIR=" + filepath.Join(home, ".config", "gh")
	if !useLocal {
		out := folders.GitEnv{Env: append([]string{"HOME=" + home, "PATH=" + basePath, "GIT_TERMINAL_PROMPT=0", ghConfig}, ghEnv...), Args: []string{"-c", "credential.helper="}}
		if helper != "" {
			out.Args = append(out.Args, "-c", "credential.helper="+helper)
		}
		return out
	}
	out := folders.GitEnv{Env: append(append(slices.Clone(localEnv), ghConfig), ghEnv...)}
	if helper != "" {
		key := "credential." + githubCredentialScope + ".helper"
		out.Args = []string{"-c", key + "=", "-c", key + "=" + helper}
	}
	return out
}

// folderGHCredential 返回 Agent 当前配置里 gh 凭证的环境变量与对应的 git 凭证助手；没有配置 gh 时都为空。
func folderGHCredential(ctx context.Context, cfg *agentconfig.Service, templates *clitemplate.Catalog, installer *clitemplate.Installer, agentID uuid.UUID) ([]string, string, error) {
	snap, err := cfg.ExecutionSnapshot(ctx, agentID)
	if errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		return nil, "", errors.New("Agent 的凭证不可用，请先在配置页修复")
	}
	if err != nil {
		return nil, "", err
	}
	payload, ok := snap.CLIPayloads["gh"]
	if !ok {
		return nil, "", nil
	}
	template, ok := templates.Get("gh")
	if !ok {
		return nil, "", errors.New("gh CLI 模板不可用")
	}
	dir, err := installer.Ensure(ctx, template)
	if err != nil {
		return nil, "", errors.New("gh CLI 安装失败")
	}
	var env []string
	for k, v := range payload.Env {
		if !executor.IsReservedEnv(k) && k != "GH_CONFIG_DIR" {
			env = append(env, k+"="+v)
		}
	}
	helper := "!'" + strings.ReplaceAll(filepath.Join(dir, template.Binary), "'", "'\\''") + "' auth git-credential"
	return env, helper, nil
}
