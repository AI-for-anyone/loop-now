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

// githubCredentialScope is the address scope used only for the Agent's gh credential (GH_TOKEN, valid only for
// github.com) when "Use local git identity" is on.
const githubCredentialScope = "https://github.com"

// gitLocalIdentity holds what "Use local git identity" needs; nil means local mode is not enabled.
type gitLocalIdentity struct {
	modes    *localmode.Service
	userPath *localmode.UserPath
	gitBin   string
}

// folderGitAccess returns the environment used by "Pull from Git".
//
// When the local mode git switch is off, this is the same as cloning a repository during a run: HOME is the Agent's own
// directory, the credential helpers configured on this computer are cleared first, and then the gh CLI credential in
// the Agent's current config (the project GitHub account) is used as the credential helper; without gh configured the
// clone is anonymous. No other environment variables of the server are inherited.
//
// When the switch is on, it keeps the real HOME, SSH_AUTH_SOCK, git config and credential helpers of the user running
// the server process (localmode.GitIdentityEnv), so SSH addresses and HTTPS credentials already stored on this computer
// work. When the Agent has gh configured, HTTPS to github.com uses only its credential: the address-scoped
// credential.<url>.helper is cleared first and then set to gh only, so credentials stored on this computer for
// github.com are not read, and git never writes the Agent's token into (or, on rejection, deletes it from) the local
// credential store.
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
			// Query git config in the Agent root directory: like the .git-pulls directory where clones happen, it is
			// not inside any repository, so it reads the same global and system config.
			if localEnv, err = localmode.GitIdentityEnv(ctx, local.gitBin, basePath, userPath, folders.AgentRoot(workRoot, agentID)); err != nil {
				return folders.GitEnv{}, err
			}
		}
		return gitPullEnv(home, basePath, ghEnv, helper, useLocal, localEnv), nil
	}
}

// gitPullEnv combines the pull environment with the arguments before clone. When useLocal is false, HOME is the Agent's
// own directory, and credential helpers are cleared first and then only gh is used; when true, it uses localEnv (the
// local git identity) and the gh helper is used only for github.com. The gh config directory is always the Agent's own.
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

// folderGHCredential returns the environment variables of the gh credential in the Agent's current config and the
// matching git credential helper; both are empty when gh is not configured.
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
