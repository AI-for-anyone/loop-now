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
	"runtime"
	"slices"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/execenv"
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
	workspace string // 运行时的工作目录
	pidFile   string
	// claudePlugin 是平台给 Claude Code 的插件目录（--plugin-dir，里面是 Skills），claudeMCP 是它的 --mcp-config 文件。
	claudePlugin string
	claudeMCP    string
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

		claudePlugin: filepath.Join(root, "claude-plugin"),
		claudeMCP:    filepath.Join(root, "claude-mcp.json"),
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
// loginHome 不为空时（“用本机的登录状态”）HOME 与 Windows 上对应的一组变量取用户真实的，并透传 loginPassthrough；
// TMPDIR、PI_CODING_AGENT_DIR 等平台目录不变。
func (e *Executor) buildEnv(d agentDirs, snap agentconfig.Snapshot, cliBinDirs, userPath []string, loginHome string, log *slog.Logger) []string {
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
	home := d.home
	if loginHome != "" {
		home = loginHome
	}
	// Windows 上 node、git 与 PowerShell 还需要 SYSTEMROOT 等系统变量和与 HOME 对应的 USERPROFILE／APPDATA／TEMP；Unix 上为空。
	for _, kv := range execenv.Base(home, d.tmp) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			set(k, v)
		}
	}
	if loginHome != "" && runtime.GOOS == "windows" {
		// 用户的 APPDATA／LOCALAPPDATA 可能被重定向，取服务端进程环境里的真实值。
		for _, k := range []string{"APPDATA", "LOCALAPPDATA"} {
			if v := e.o.Getenv(k); v != "" {
				set(k, v)
			}
		}
	}
	set("HOME", home)
	set("TMPDIR", d.tmp)
	set("PI_CODING_AGENT_DIR", d.pi)
	set("PI_OFFLINE", "1")
	set("GIT_TERMINAL_PROMPT", "0")
	set("USER", e.o.Username)
	set("LOGNAME", e.o.Username)
	if loginHome != "" {
		for _, k := range loginPassthrough {
			if v := e.o.Getenv(k); v != "" {
				set(k, v)
			}
		}
	}
	var path []string
	if e.o.LoopctlBinDir != "" {
		path = append(path, e.o.LoopctlBinDir) // 同版本 loopctl 放在最前面，不会被 CLI 目录中的同名程序覆盖
	}
	path = append(append(append(path, cliBinDirs...), snap.Plugins.Bin...), e.o.ExecBasePath)
	// “用本机的 CLI”：用户的 PATH 接在最后，已在前面出现的目录不重复，随包 Node、loopctl 与模板 CLI 仍然优先。
	path = appendUserPath(path, userPath)
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

// loginPassthrough 是开着“用本机的登录状态”时另外透传的变量：ssh-agent 的套接字（带口令的密钥）与改过位置的 XDG 目录。
var loginPassthrough = []string{"SSH_AUTH_SOCK", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// loginOnlyEnv 是只因“用本机的登录状态”才出现在 Pi 环境里的变量（关着时本来就透传的不算），受管 CLI 的小脚本要去掉它们。
func (e *Executor) loginOnlyEnv() []string {
	var out []string
	for _, k := range loginPassthrough {
		if !slices.Contains(e.o.Passthrough, k) {
			out = append(out, k)
		}
	}
	return out
}

// cliShimDir 是受管 CLI 小脚本所在的目录（每次启动 Pi 时重建）。
const cliShimDir = "cli-shims"

// syncCLIShims 在开着“用本机的登录状态”时，为这个 Agent 放进执行 PATH 的每个模板 CLI 写一个同名小脚本：把 HOME 恢复成
// Agent 的独立 HOME、去掉只因登录状态才透传的变量，再 exec 原来的可执行文件。这样受管 CLI（及其子进程）的环境与开关关着时
// 一致，仍用 Loop Now 配置的账号与凭证文件（gcloud、psql、mysql 的包装按 $HOME 找凭证，lark-cli 的主密钥按 $HOME 找），
// 不会读写用户真实主目录里的东西。没配的 CLI 不生成脚本，按 PATH 落到用户本机装的那份并用真实 HOME。
// 目录每次先整体删除再按需重建，不留旧脚本；返回脚本目录，不需要时为空。只在 Unix 上做：模板 CLI 都没有 Windows 安装包。
func syncCLIShims(d agentDirs, loginHome string, bins, unset []string) (string, error) {
	dir := filepath.Join(d.root, cliShimDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if loginHome == "" || runtime.GOOS == "windows" || len(bins) == 0 {
		return "", nil
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	for _, bin := range bins {
		var b strings.Builder
		b.WriteString("#!/bin/sh\n# Loop Now：受管 CLI 以 Agent 的独立 HOME 运行（用本机的登录状态开着时）。\n")
		b.WriteString("HOME=" + shellQuote(d.home) + "\nexport HOME\n")
		if len(unset) > 0 {
			b.WriteString("unset " + strings.Join(unset, " ") + "\n")
		}
		b.WriteString("exec " + shellQuote(bin) + " \"$@\"\n")
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(bin)), []byte(b.String()), 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// shellQuote 把 s 放进单引号，供 /bin/sh 原样使用。
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// appendUserPath 把 user 中尚未出现在 path（各项可能是以分隔符连接的多个目录）里的目录依次接到末尾；
// 是否已出现按 execenv.PathKey 比较（Windows 上不分大小写）。
func appendUserPath(path, user []string) []string {
	if len(user) == 0 {
		return path
	}
	seen := map[string]bool{}
	for _, p := range path {
		for _, d := range filepath.SplitList(p) {
			if d != "" {
				seen[execenv.PathKey(d)] = true
			}
		}
	}
	for _, d := range user {
		if k := execenv.PathKey(d); d != "" && !seen[k] {
			seen[k] = true
			path = append(path, d)
		}
	}
	return path
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

// writePluginMCP 把已授权插件的 MCP 服务器写进运行时读的文件：Pi 为 Agent 的 Pi 目录 mcp.json，Claude Code 为 claude-mcp.json
// （经 --mcp-config 传入，格式相同）；没有时、以及另一个运行时的那份，都删除。
func writePluginMCP(d agentDirs, p agentconfig.PluginAssets, claude bool) error {
	path, other := filepath.Join(d.pi, "mcp.json"), d.claudeMCP
	if claude {
		path, other = other, path
	}
	if err := os.Remove(other); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
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
	if e.o.Models != nil {
		for _, provider := range e.o.Models.Snapshot().Providers {
			if slices.Contains(provider.APIKeyEnvVars, name) {
				return true
			}
		}
	}
	return strings.Contains(name, "API_KEY") || strings.Contains(name, "ACCESS_TOKEN") || strings.Contains(name, "REFRESH_TOKEN") || name == "ANTHROPIC_AUTH_TOKEN" || name == "ANTHROPIC_OAUTH_TOKEN"
}
