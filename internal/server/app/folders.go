package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/folders"
)

// folderGitAccess 返回“从 Git 拉取”使用的环境：与执行时克隆仓库相同，用该 Agent 当前配置里的 gh CLI 凭证
// （项目 GitHub 账号）作 git 凭证助手；没有配置 gh 时匿名克隆。不继承服务端的其他环境变量。
func folderGitAccess(cfg *agentconfig.Service, templates *clitemplate.Catalog, installer *clitemplate.Installer, workRoot, basePath string) folders.GitAccess {
	return func(ctx context.Context, agentID uuid.UUID) ([]string, string, error) {
		home := filepath.Join(folders.AgentRoot(workRoot, agentID), "home")
		if err := os.MkdirAll(home, 0o700); err != nil {
			return nil, "", err
		}
		env := []string{"HOME=" + home, "PATH=" + basePath, "GIT_TERMINAL_PROMPT=0", "GH_CONFIG_DIR=" + filepath.Join(home, ".config", "gh")}
		snap, err := cfg.ExecutionSnapshot(ctx, agentID)
		if errors.Is(err, agentconfig.ErrCredentialUnavailable) {
			return nil, "", errors.New("Agent 的凭证不可用，请先在配置页修复")
		}
		if err != nil {
			return nil, "", err
		}
		payload, ok := snap.CLIPayloads["gh"]
		if !ok {
			return env, "", nil
		}
		template, ok := templates.Get("gh")
		if !ok {
			return nil, "", errors.New("gh CLI 模板不可用")
		}
		dir, err := installer.Ensure(ctx, template)
		if err != nil {
			return nil, "", errors.New("gh CLI 安装失败")
		}
		for k, v := range payload.Env {
			if !executor.IsReservedEnv(k) && k != "GH_CONFIG_DIR" {
				env = append(env, k+"="+v)
			}
		}
		helper := "!'" + strings.ReplaceAll(filepath.Join(dir, template.Binary), "'", "'\\''") + "' auth git-credential"
		return env, helper, nil
	}
}
