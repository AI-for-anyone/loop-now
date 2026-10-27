// Package preflight 在服务端开始提供服务前检查运行依赖，并推导依赖本机环境的配置默认值。
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

	"github.com/redis/go-redis/v9"
)

// Resolved 是检查通过后得到的本机绝对路径与推导值。
type Resolved struct {
	PiBin        string // PI_BIN 解析后的绝对路径（保留符号链接本身，便于识别进程命令行）
	PiRealPath   string // PI_BIN 解析符号链接后的真实路径
	PiPackageDir string // Pi 安装包根目录
	PiAIDir      string
	NodeBin      string
	GitBin       string
	ExecBasePath string
	WorkRoot     string // 解析符号链接后的绝对路径
}

// HostCommonDirs 是执行 PATH 中排在系统目录之前的宿主常用目录（存在的才加入）。
var HostCommonDirs = []string{"/opt/homebrew/bin", "/usr/local/bin"}

// SystemDirs 是执行 PATH 末尾的系统目录。
var SystemDirs = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// Options 是检查所需的输入。
type Options struct {
	PiBin        string
	PiVersion    string
	PiAIDir      string // 为空时推导
	NodeBin      string
	ExecBasePath string // 为空时推导
	WorkRoot     string
	// HomeDirs 是不允许包含工作根目录的 home 目录；为空时取当前用户的 home。
	HomeDirs []string
}

// CheckRedis 确认 Redis 可连接。
func CheckRedis(ctx context.Context, redisURL string) error {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return errors.New("Redis 连接串无法解析")
	}
	client := redis.NewClient(opts)
	defer client.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis 不可用：%w", err)
	}
	return nil
}

// Resolve 检查 Pi、node、git 与工作根目录，并推导执行 PATH 与 pi-ai 目录。
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

	if err := checkPiVersion(ctx, r.PiBin, o.PiVersion, r.ExecBasePath); err != nil {
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

// DeriveExecBasePath 按“宿主常用目录 → node/git 真实所在目录 → 系统目录”拼接执行 PATH 的基础部分。
func DeriveExecBasePath(nodeBin, gitBin string) string {
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	for _, d := range HostCommonDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			add(d)
		}
	}
	for _, bin := range []string{nodeBin, gitBin} {
		if real, err := filepath.EvalSymlinks(bin); err == nil {
			add(filepath.Dir(real))
		}
	}
	for _, d := range SystemDirs {
		add(d)
	}
	return strings.Join(dirs, string(os.PathListSeparator))
}

func checkPiVersion(ctx context.Context, piBin, want, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBin, "--version")
	// Pi 以 `#!/usr/bin/env node` 启动，需要能在 PATH 中找到 node；其余环境一律不传。
	cmd.Env = []string{"PATH=" + path, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "HOME=" + os.TempDir()}
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

// findPackageRoot 从 start 向上查找 name 为 pkgName 的 package.json 所在目录。
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

// ContextFileNames 是 Pi 会从工作目录及其上级目录加载的上下文文件。
var ContextFileNames = []string{"AGENTS.md", "CLAUDE.md"}

// PrepareWorkRoot 校验并准备工作根目录，返回解析符号链接后的绝对路径：
// 不能位于 homes 中任何一个目录之内；它及其上级目录中不能有 AGENTS.md/CLAUDE.md；
// 不存在时创建，权限为 0700。
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
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(real, 0o700); err != nil {
			return "", fmt.Errorf("把工作根目录权限设为 0700 失败：%w", err)
		}
	}
	return real, nil
}

// resolveExisting 解析路径中已存在部分的符号链接，再拼上尚不存在的部分。
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
