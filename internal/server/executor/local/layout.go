// Package local is the local executor: on the machine the server runs on, it manages one long-running Pi process in RPC
// mode for each regular Agent.
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

// agentDirs is the directory layout of one Agent under the work root.
type agentDirs struct {
	root      string // <WORK_ROOT>/agents/<id>
	home      string // separate HOME; deleted as a whole when the Agent is deleted
	tmp       string // TMPDIR
	pi        string // PI_CODING_AGENT_DIR
	sessions  string // --session-dir
	skills    string // rebuilt from the config on every start
	prompt    string // --append-system-prompt file
	workspace string // the runtime's working directory
	pidFile   string
	// claudePlugin is the plugin directory the platform gives Claude Code (--plugin-dir, holding the Skills); claudeMCP
	// is its --mcp-config file.
	claudePlugin string
	claudeMCP    string
	// codexSkills is the Skill directory the platform gives Codex (an extra Skill root).
	codexSkills string
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
		codexSkills:  filepath.Join(root, "codex-skills"),
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

// buildEnv builds the Pi process's environment from an empty one: directory variables, the run PATH, the common
// variables allowed to pass through, and the credentials of the allowed CLIs; model secrets go only through the
// separate auth.json/private fd. No other variable of the server is inherited. When loginHome is not empty
// (“用本机的登录状态” ("Use local logins")), HOME and the matching set of variables on Windows take the user's real
// values, and loginPassthrough is passed through; platform directories such as TMPDIR and PI_CODING_AGENT_DIR do not
// change.
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
	// The templates' fixed environment variables are injected from the current template, after the credentials, so they
	// cannot be overridden by variables of the same name in the model credential.
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
	// On Windows, node, git and PowerShell also need system variables such as SYSTEMROOT, and USERPROFILE/APPDATA/TEMP
	// to match HOME; on Unix this is empty.
	for _, kv := range execenv.Base(home, d.tmp) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			set(k, v)
		}
	}
	if loginHome != "" && runtime.GOOS == "windows" {
		// The user's APPDATA/LOCALAPPDATA may be redirected, so take the real values from the server process's
		// environment.
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
		path = append(path, e.o.LoopctlBinDir) // same-version loopctl goes first, so a same-named program in a CLI directory cannot shadow it
	}
	path = append(append(append(path, cliBinDirs...), snap.Plugins.Bin...), e.o.ExecBasePath)
	// “用本机的 CLI” ("Use local CLIs"): the user's PATH goes last, directories that already appear earlier are not
	// repeated, and the bundled Node, loopctl and template CLIs still take precedence.
	path = appendUserPath(path, userPath)
	set("PATH", strings.Join(path, string(os.PathListSeparator)))
	// Config and accounts of granted plugins; they do not override the directories, PATH and reserved variables above.
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

// loginPassthrough are the variables also passed through when “用本机的登录状态” ("Use local logins") is on: the
// ssh-agent socket (for keys with a passphrase) and relocated XDG directories.
var loginPassthrough = []string{"SSH_AUTH_SOCK", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// loginOnlyEnv are the variables that appear in Pi's environment only because of
// “用本机的登录状态” ("Use local logins") (ones passed through anyway when it is off do not count); the managed CLI
// shims must remove them.
func (e *Executor) loginOnlyEnv() []string {
	var out []string
	for _, k := range loginPassthrough {
		if !slices.Contains(e.o.Passthrough, k) {
			out = append(out, k)
		}
	}
	return out
}

// cliShimDir is the directory of the managed CLI shims (rebuilt every time Pi starts).
const cliShimDir = "cli-shims"

// syncCLIShims, when “用本机的登录状态” ("Use local logins") is on, writes a shim of the same name for each template CLI this
// Agent puts on the run PATH: it restores HOME to the Agent's separate HOME, removes the variables passed through only
// because of the login setting, then execs the original executable. That way the environment of a managed CLI (and its
// child processes) is the same as with the switch off, and it still uses the accounts and credential files configured
// in Loop Now (the gcloud, psql and mysql wrappers find credentials via $HOME, and lark-cli finds its master key via
// $HOME) without reading or writing anything in the user's real home directory. CLIs that are not configured get no
// shim, so PATH resolves them to the user's locally installed copy, which uses the real HOME. The directory is deleted
// as a whole and rebuilt as needed every time, leaving no old shims; it returns the shim directory, or empty when none
// is needed. Only done on Unix: none of the template CLIs has a Windows install package.
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
		b.WriteString("#!/bin/sh\n# Loop Now: a managed CLI runs with the Agent's own HOME (while \"Use local logins\" is on).\n")
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

// shellQuote puts s in single quotes so /bin/sh uses it verbatim.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// appendUserPath appends, in order, the directories in user that do not yet appear in path (each item may be several
// directories joined by the separator); whether a directory already appears is compared with execenv.PathKey
// (case-insensitive on Windows).
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

// pluginArgs returns the Pi arguments for granted plugins (plugins/SPEC.md §2.1), placed after the platform's own -e:
// one -e per extension and one --skill per Skill directory; when there are MCP servers it adds -e builtin:mcp
// (--no-extensions also turns off the built-in MCP).
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

// writeMCPConfig writes the MCP servers of granted plugins into the file a runtime reads ({"mcpServers": ...}); when
// there are none, the file is deleted.
func writeMCPConfig(path string, p agentconfig.PluginAssets) error {
	if len(p.MCPServers) == 0 {
		return removeIfExists(path)
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": p.MCPServers}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// removeIfExists deletes a file; one that does not exist is not an error.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// piArgs returns the arguments for starting Pi.
func piArgs(d agentDirs, snap agentconfig.Snapshot, skillDirs []string, sessionFile string) []string {
	args := []string{"--mode", "rpc", "--no-approve", "--offline", "--no-skills", "--no-extensions"}
	for _, s := range skillDirs {
		args = append(args, "--skill", s)
	}
	// The Prompt file written by prepare (the Agent's Prompt and the "Folders and context" section); it does not exist
	// when there is neither.
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
