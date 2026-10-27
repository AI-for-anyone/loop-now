// Package local 是本机执行器：在服务端所在机器上，以 RPC 模式为每个常态 Agent 管理一个常驻 Pi 进程。
package local

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// agentDirs 是一个 Agent 在工作根目录下的目录布局。
type agentDirs struct {
	root      string // <WORK_ROOT>/agents/<id>
	home      string // 独立 HOME；删除 Agent 时整体删除
	tmp       string // TMPDIR
	pi        string // PI_CODING_AGENT_DIR
	sessions  string // --session-dir
	skills    string // 每次启动按配置重建
	prompt    string // --append-system-prompt 文件
	workspace string // Pi 的工作目录
	pidFile   string
}

func (e *Executor) dirs(agentID uuid.UUID) agentDirs {
	root := filepath.Join(e.o.WorkRoot, "agents", agentID.String())
	return agentDirs{
		root:      root,
		home:      filepath.Join(root, "home"),
		tmp:       filepath.Join(root, "tmp"),
		pi:        filepath.Join(root, "pi"),
		sessions:  filepath.Join(root, "sessions"),
		skills:    filepath.Join(root, "skills"),
		prompt:    filepath.Join(root, "prompt.md"),
		workspace: filepath.Join(root, "workspace"),
		pidFile:   filepath.Join(root, "pi.pid"),
	}
}

func (d agentDirs) ensure() error {
	for _, dir := range []string{d.root, d.home, d.tmp, d.pi, d.sessions, d.workspace} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// buildEnv 从空环境构造 Pi 进程的环境：目录变量、执行 PATH、允许透传的通用变量、
// 被允许 CLI 的凭证；模型秘密仅通过独立auth.json/私有fd。不继承服务端的任何其他变量。
func (e *Executor) buildEnv(d agentDirs, snap agentconfig.Snapshot, cliBinDirs []string, log *slog.Logger) []string {
	vars := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, ok := vars[k]; !ok {
			order = append(order, k)
		}
		vars[k] = v
	}
	for _, name := range e.o.Passthrough {
		if v := e.o.Getenv(name); v != "" && !executor.IsReservedEnv(name) && !e.modelSecretEnv(name) {
			set(name, v)
		}
	}
	for _, cli := range snap.CLIs {
		if cli == "lark-cli" && snap.LarkBinding != nil {
			continue // never inject captured legacy bot payload into a user runtime
		}
		for k, v := range snap.CLIPayloads[cli].Env {
			if !executor.IsReservedEnv(k) {
				set(k, v)
			}
		}
	}
	// 模板的固定环境变量按当前模板注入，放在凭证之后，不会被模型凭证中的同名变量覆盖。
	for _, cli := range snap.CLIs {
		t, ok := e.tmpl.Get(cli)
		if !ok {
			continue
		}
		for _, k := range slices.Sorted(maps.Keys(t.Env)) {
			if !executor.IsReservedEnv(k) {
				set(k, t.Env[k])
			}
		}
	}
	set("HOME", d.home)
	set("TMPDIR", d.tmp)
	set("PI_CODING_AGENT_DIR", d.pi)
	set("PI_OFFLINE", "1")
	set("GIT_TERMINAL_PROMPT", "0")
	set("USER", e.o.Username)
	set("LOGNAME", e.o.Username)
	var path []string
	if e.o.LoopctlBinDir != "" {
		path = append(path, e.o.LoopctlBinDir) // 同版本 loopctl 放在最前面，不会被 CLI 目录中的同名程序覆盖
	}
	path = append(append(append(path, cliBinDirs...), snap.Plugins.Bin...), e.o.ExecBasePath)
	set("PATH", strings.Join(path, string(os.PathListSeparator)))
	// 已授权插件的配置与账号；不覆盖上面的目录、PATH 与保留变量。
	for _, k := range slices.Sorted(maps.Keys(snap.Plugins.Env)) {
		if _, taken := vars[k]; !taken && !executor.IsReservedEnv(k) {
			set(k, snap.Plugins.Env[k])
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// pluginArgs 返回已授权插件的 Pi 参数（plugins/SPEC.md §2.1），排在平台自己的 -e 之后：
// 每个扩展一条 -e，每个 Skill 目录一条 --skill；有 MCP 服务器时加 -e builtin:mcp（--no-extensions 也关掉了内置 MCP）。
func pluginArgs(p agentconfig.PluginAssets) []string {
	var args []string
	for _, ext := range p.Extensions {
		args = append(args, "-e", ext)
	}
	for _, s := range p.Skills {
		args = append(args, "--skill", s)
	}
	if len(p.MCPServers) > 0 {
		args = append(args, "-e", "builtin:mcp")
	}
	return args
}

// writePluginMCP 把已授权插件的 MCP 服务器写进 Agent 的 Pi 目录 mcp.json；没有时删除旧文件。
func writePluginMCP(d agentDirs, p agentconfig.PluginAssets) error {
	path := filepath.Join(d.pi, "mcp.json")
	if len(p.MCPServers) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": p.MCPServers}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// piArgs 返回启动 Pi 的参数。
func piArgs(d agentDirs, snap agentconfig.Snapshot, skillDirs []string, sessionFile string) []string {
	args := []string{"--mode", "rpc", "--no-approve", "--offline", "--no-skills", "--no-extensions"}
	for _, s := range skillDirs {
		args = append(args, "--skill", s)
	}
	// prepare 写好的 Prompt 文件（Agent 的 Prompt 与“文件夹与上下文”一节）；两者都没有时不存在。
	if _, err := os.Stat(d.prompt); err == nil {
		args = append(args, "--append-system-prompt", d.prompt)
	}
	args = append(args, "--provider", snap.Provider, "--model", snap.ModelID, "--session-dir", d.sessions)
	if snap.ThinkingLevel != nil {
		args = append(args, "--thinking", *snap.ThinkingLevel)
	}
	if sessionFile != "" {
		args = append(args, "--session", sessionFile)
	} else if snap.ForkSessionFile != "" {
		args = append(args, "--fork", snap.ForkSessionFile)
	}
	return args
}

func (e *Executor) modelSecretEnv(name string) bool {
	if strings.HasPrefix(name, "LOOP_NOW_") {
		return true
	}
	if _, reserved := e.tmpl.ReservedEnvBy(name); reserved {
		return true
	}
	if slices.Contains(e.o.ModelSecretEnvNames, name) {
		return true
	}
	return strings.Contains(name, "API_KEY") || strings.Contains(name, "ACCESS_TOKEN") || strings.Contains(name, "REFRESH_TOKEN") || name == "ANTHROPIC_AUTH_TOKEN" || name == "ANTHROPIC_OAUTH_TOKEN"
}
