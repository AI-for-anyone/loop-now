// Package preflight checks the runtime dependencies before the server starts serving, and derives the configuration
// defaults that depend on the local machine.
package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/privatefs"
)

// Resolved holds the absolute local paths and derived values obtained once the checks pass.
type Resolved struct {
	PiBin        string // absolute path of PI_BIN (the symlink itself is kept, so process command lines can be recognized)
	PiRealPath   string // real path of PI_BIN with symlinks resolved
	PiPackageDir string // root directory of the Pi package
	PiAIDir      string
	NodeBin      string
	GitBin       string
	ExecBasePath string
	WorkRoot     string // absolute path with symlinks resolved
}

// HostCommonDirs are the common host machine directories that come before the system directories in the run PATH
// (only those that exist are added); taken per platform from execenv.
var HostCommonDirs = execenv.HostCommonDirs()

// SystemDirs are the system directories at the end of the run PATH; taken per platform from execenv.
var SystemDirs = execenv.SystemDirs()

// Options is the input the checks need.
type Options struct {
	PiBin        string
	PiVersion    string
	PiAIDir      string // derived when empty
	NodeBin      string
	ExecBasePath string // derived when empty
	WorkRoot     string
	// HomeDirs are the home directories that must not contain the work root; when empty, the current user's home is
	// used.
	HomeDirs []string
}

// Resolve checks Pi, node, git and the work root, and derives the run PATH and the pi-ai directory.
func Resolve(ctx context.Context, o Options) (*Resolved, error) {
	r := &Resolved{}
	var err error

	if r.NodeBin, err = lookPath(o.NodeBin); err != nil {
		return nil, fmt.Errorf("找不到 node（%s）：%w", o.NodeBin, err)
	}
	if r.GitBin, err = lookPath("git"); err != nil {
		return nil, fmt.Errorf("找不到 git：%w", err)
	}
	if r.PiBin, err = lookPath(o.PiBin); err != nil {
		return nil, fmt.Errorf("找不到 Pi（%s）：%w", o.PiBin, err)
	}
	if r.PiRealPath, err = filepath.EvalSymlinks(r.PiBin); err != nil {
		return nil, fmt.Errorf("解析 Pi 路径失败：%w", err)
	}

	r.ExecBasePath = o.ExecBasePath
	if r.ExecBasePath == "" {
		r.ExecBasePath = DeriveExecBasePath(r.NodeBin, r.GitBin)
	}

	if err := checkPiVersion(ctx, r.NodeBin, r.PiBin, o.PiVersion, r.ExecBasePath); err != nil {
		return nil, err
	}

	r.PiPackageDir = findPackageRoot(r.PiRealPath, "@earendil-works/pi-coding-agent")
	r.PiAIDir = o.PiAIDir
	if r.PiAIDir == "" {
		if r.PiAIDir, err = derivePiAIDir(r.PiPackageDir); err != nil {
			return nil, err
		}
	}

	homes := o.HomeDirs
	if len(homes) == 0 {
		homes = currentHomes()
	}
	if r.WorkRoot, err = PrepareWorkRoot(o.WorkRoot, homes); err != nil {
		return nil, err
	}
	return r, nil
}

func lookPath(name string) (string, error) {
	p, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// DeriveExecBasePath builds the base part of the run PATH in the order "node's real directory -> common host
// directories -> git's real directory -> system directories".
// node comes first: Pi starts with `#!/usr/bin/env node`, and the configured (or bundled) node must not be shadowed by
// another node installed on the host.
// The exception is a node inside a system directory: it still comes after the common host directories, so old tools
// in the system directories do not shadow the ones installed on the host.
func DeriveExecBasePath(nodeBin, gitBin string) string {
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	realDir := func(bin string) string {
		if real, err := filepath.EvalSymlinks(bin); err == nil {
			return filepath.Dir(real)
		}
		return ""
	}
	nodeDir := realDir(nodeBin)
	if !slices.Contains(SystemDirs, nodeDir) {
		add(nodeDir)
	}
	for _, d := range HostCommonDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			add(d)
		}
	}
	add(nodeDir)
	add(realDir(gitBin))
	for _, d := range SystemDirs {
		add(d)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

// PiCommand returns the command that starts Pi: when piBin is a JavaScript entry (dist/bundle/cli.js in the bundled
// layout) node runs it - Windows has no shebang, and on Unix `#!/usr/bin/env node` has the same effect; otherwise
// piBin (the pi command on PATH) is run directly.
// When nodeBin is empty, node is looked up on PATH.
func PiCommand(nodeBin, piBin string, args ...string) *exec.Cmd {
	if IsJavaScriptEntry(piBin) {
		if nodeBin == "" {
			nodeBin = "node"
		}
		return exec.Command(nodeBin, append([]string{piBin}, args...)...)
	}
	return exec.Command(piBin, args...)
}

// IsJavaScriptEntry reports whether path is a script file that node must run.
func IsJavaScriptEntry(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	return false
}

func checkPiVersion(ctx context.Context, nodeBin, piBin, want, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := PiCommand(nodeBin, piBin, "--version")
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	// Pi starts with `#!/usr/bin/env node` (or node runs cli.js directly), so node must be found on PATH; no other
	// environment is passed.
	cmd.Env = append(execenv.Base(os.TempDir(), os.TempDir()), "PATH="+path, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "HOME="+os.TempDir())
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("执行 %s --version 失败：%w", piBin, err)
	}
	got := strings.TrimSpace(string(out))
	if got != want {
		return fmt.Errorf("Pi 版本不符：期望 %s，实际 %s（可用 LOOP_NOW_PI_VERSION 或 LOOP_NOW_PI_BIN 调整）", want, got)
	}
	return nil
}

// findPackageRoot walks up from start looking for the directory of a package.json whose name is pkgName.
func findPackageRoot(start, pkgName string) string {
	dir := filepath.Dir(start)
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
			var pkg struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(b, &pkg) == nil && pkg.Name == pkgName {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func derivePiAIDir(piPackageDir string) (string, error) {
	if piPackageDir == "" {
		return "", errors.New("无法定位 Pi 安装包根目录，请用 LOOP_NOW_PI_AI_DIR 指定 pi-ai 目录")
	}
	for _, c := range []string{
		filepath.Join(piPackageDir, "node_modules", "@earendil-works", "pi-ai"),
		filepath.Join(filepath.Dir(piPackageDir), "pi-ai"),
	} {
		if _, err := os.Stat(filepath.Join(c, "package.json")); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("在 %s 附近找不到 @earendil-works/pi-ai，请用 LOOP_NOW_PI_AI_DIR 指定", piPackageDir)
}

func currentHomes() []string {
	var homes []string
	if h, err := os.UserHomeDir(); err == nil {
		homes = append(homes, h)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		homes = append(homes, u.HomeDir)
	}
	return homes
}

// ContextFileNames are the context files Pi loads from the working directory and its parent directories.
var ContextFileNames = []string{"AGENTS.md", "CLAUDE.md"}

// PrepareWorkRoot validates and prepares the work root and returns its absolute path with symlinks resolved:
// it must not be inside any of the homes directories; neither it nor its parent directories may contain
// AGENTS.md/CLAUDE.md; it is created with mode 0700 when missing.
func PrepareWorkRoot(workRoot string, homes []string) (string, error) {
	abs, err := filepath.Abs(workRoot)
	if err != nil {
		return "", err
	}
	real, err := resolveExisting(abs)
	if err != nil {
		return "", err
	}
	for _, h := range homes {
		hr, err := resolveExisting(h)
		if err != nil {
			continue
		}
		if within(real, hr) {
			return "", fmt.Errorf("工作根目录 %s 位于用户 home（%s）之内：Pi 会加载工作目录及其所有上级目录中的 AGENTS.md/CLAUDE.md，请改用 home 之外的目录", real, hr)
		}
	}
	for dir := real; ; dir = filepath.Dir(dir) {
		for _, name := range ContextFileNames {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				return "", fmt.Errorf("工作根目录的上级目录中有 %s：Pi 会把它加载进每个 Agent 的上下文，请移走该文件或更换工作根目录", p)
			}
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if err := os.MkdirAll(real, 0o700); err != nil {
		return "", fmt.Errorf("创建工作根目录失败：%w", err)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("工作根目录 %s 不是目录", real)
	}
	if !privatefs.IsPrivate(real, fi) {
		if err := privatefs.MakePrivate(real); err != nil {
			return "", fmt.Errorf("把工作根目录设为只有当前用户可访问失败：%w", err)
		}
	}
	return real, nil
}

// resolveExisting resolves symlinks in the part of the path that exists, then appends the part that does not exist
// yet.
func resolveExisting(p string) (string, error) {
	p = filepath.Clean(p)
	var rest []string
	for {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", fmt.Errorf("无法解析路径 %s", p)
		}
		rest = append(rest, filepath.Base(p))
		p = parent
	}
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
