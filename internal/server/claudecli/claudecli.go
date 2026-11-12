// Package claudecli 找到并询问用户本机安装的 Claude Code（claude）：只运行它自己的命令（--version、auth status、purge），
// 不读、不复制、不保存它的凭证（不碰钥匙串，也不碰 ~/.claude 里的凭证文件）。用的是用户本人在这台机器上的登录。
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/procgroup"
)

// ErrNotFound 表示在给定的 PATH 里找不到 claude。
var ErrNotFound = errors.New("在登录 shell 的 PATH 里找不到 claude")

// commandTimeout 是 claude 自己的短命令（--version、auth status、purge）的时限。
const commandTimeout = 30 * time.Second

// names 是要找的可执行文件名：Windows 上原生安装是 claude.exe，npm 全局安装是 claude.cmd。
func names() []string {
	if runtime.GOOS == "windows" {
		return []string{"claude.exe", "claude.cmd"}
	}
	return []string{"claude"}
}

// Find 按顺序在 dirs（用户登录 shell 的 PATH）里找 claude，返回解析符号链接后的绝对路径。
func Find(dirs []string) (string, error) {
	for _, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, name := range names() {
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
				continue
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil {
				continue
			}
			return real, nil
		}
	}
	return "", ErrNotFound
}

// forbiddenEnv 报告这个环境变量不能交给 claude：平台只用本机登录，不让任何 Anthropic／Claude Code 的变量
// （API key、令牌、配置目录、嵌套会话标记等）改变它的认证或配置。Windows 上变量名不分大小写。
func forbiddenEnv(name string) bool {
	if runtime.GOOS == "windows" {
		name = strings.ToUpper(name)
	}
	return strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE")
}

// StripEnv 去掉 env（KEY=VALUE 列表）里不能交给 claude 的变量。
func StripEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !forbiddenEnv(k) {
			out = append(out, kv)
		}
	}
	return out
}

// EnvOptions 是运行 claude 自己的短命令（--version、auth status、purge）时的环境，与 Agent 的 Claude Code 进程同一套基础变量。
type EnvOptions struct {
	Home string
	Tmp  string
	// User 是 USER 与 LOGNAME：claude 按它找本机的登录，缺了 auth status 会报未登录。
	User string
	Path []string
	// Extra 是其他基础变量（K=V），如透传的 LANG、LC_ALL、TZ；不覆盖上面几项。
	Extra []string
}

// Env 是运行 claude 自己的短命令时的环境：Windows 上的系统变量、透传的基础变量、真实的 HOME、USER／LOGNAME 与给定的 PATH，
// 不带任何 Anthropic／Claude 的变量。
func Env(o EnvOptions) []string {
	env := execenv.Base(o.Home, o.Tmp)
	for _, kv := range o.Extra {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "HOME", "PATH", "TMPDIR", "USER", "LOGNAME":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+o.Home, "PATH="+strings.Join(o.Path, string(os.PathListSeparator)))
	if o.User != "" {
		env = append(env, "USER="+o.User, "LOGNAME="+o.User)
	}
	if o.Tmp != "" {
		env = append(env, "TMPDIR="+o.Tmp)
	}
	return StripEnv(env)
}

// run 运行 claude 的一条短命令，返回标准输出；退出状态不为 0 时同时返回错误（输出照样返回：auth status 未登录时以 1 退出）。
// 错误里只有退出状态，不带输出：auth status 的输出里有邮箱与组织，不能进日志、事件或错误消息。
func run(ctx context.Context, bin string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	cmd.Cancel = procgroup.Cancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	procgroup.Configure(cmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && ctx.Err() == nil {
			return stdout.Bytes(), fmt.Errorf("claude %s 失败：%s", args[0], exit.ProcessState.String())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("claude %s 超时", args[0])
		}
		return nil, fmt.Errorf("claude %s 无法运行", args[0])
	}
	return stdout.Bytes(), nil
}

var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version 运行 claude --version，返回版本号（如 2.1.322）。
func Version(ctx context.Context, bin string, env []string) (string, error) {
	out, err := run(ctx, bin, env, "--version")
	if err != nil {
		return "", err
	}
	v := versionPattern.FindString(strings.TrimSpace(string(out)))
	if v == "" {
		return "", errors.New("claude --version 的输出里没有版本号")
	}
	return v, nil
}

// Auth 是 claude auth status --json 里平台用到的字段。其余字段（email、orgId、orgName 等）在解码时就丢弃，
// 不进日志、事件或错误消息。
type Auth struct {
	LoggedIn          bool   `json:"loggedIn"`
	AuthMethod        string `json:"authMethod"`
	SubscriptionType  string `json:"subscriptionType"`
	LoginExpiresAt    string `json:"loginExpiresAt"`
	ProjectsDirectory string `json:"projectsDirectory"`
}

// AuthStatus 运行 claude auth status --json：由 claude 自己判断是否登录，平台拿不到令牌。未登录时它以 1 退出但照常输出 JSON，
// 所以只要输出可以识别就按输出判断；运行不了或输出无法识别时返回错误（调用方当作“不知道”）。
func AuthStatus(ctx context.Context, bin string, env []string) (Auth, error) {
	out, runErr := run(ctx, bin, env, "auth", "status", "--json")
	if len(bytes.TrimSpace(out)) == 0 && runErr != nil {
		return Auth{}, runErr
	}
	var raw struct {
		LoggedIn          *bool  `json:"loggedIn"`
		AuthMethod        string `json:"authMethod"`
		SubscriptionType  string `json:"subscriptionType"`
		LoginExpiresAt    string `json:"loginExpiresAt"`
		ProjectsDirectory string `json:"projectsDirectory"`
	}
	if json.Unmarshal(out, &raw) != nil || raw.LoggedIn == nil {
		clear(out)
		if runErr != nil {
			return Auth{}, runErr
		}
		return Auth{}, errors.New("claude auth status 的输出不是预期的 JSON")
	}
	clear(out)
	a := Auth{LoggedIn: *raw.LoggedIn, AuthMethod: raw.AuthMethod, SubscriptionType: raw.SubscriptionType, LoginExpiresAt: raw.LoginExpiresAt}
	if filepath.IsAbs(raw.ProjectsDirectory) {
		a.ProjectsDirectory = filepath.Clean(raw.ProjectsDirectory)
	}
	return a, nil
}

// Purge 运行 claude purge <path> -y：删除 claude 为这个目录留下的会话记录（只是这个路径对应的数据）。
func Purge(ctx context.Context, bin string, env []string, path string) error {
	_, err := run(ctx, bin, env, "purge", path, "-y")
	return err
}

// DefaultProjectsDir 是没有设置 CLAUDE_CONFIG_DIR 时 claude 存放会话记录的目录。
func DefaultProjectsDir(home string) string { return filepath.Join(home, ".claude", "projects") }

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// EncodeProjectDir 是 claude 把工作目录的路径编成会话记录子目录名的规则：非字母数字都换成 -（/a/b.c → -a-b-c，
// C:\Users\x → C--Users-x）。路径要不要先解析由调用方决定，见 ProjectDirNames。
func EncodeProjectDir(path string) string { return nonAlnum.ReplaceAllString(path, "-") }

// ProjectDirNames 返回工作目录 cwd 可能对应的会话记录子目录名：先是解析之后的（符号链接；Windows 上还会把 8.3 短名展开为长名），
// 再是原样的；两者相同时只有一个。macOS 上实测 claude 用解析之后的（/tmp 记成 /private/tmp）；Windows 上它用哪一种没有核实，
// 所以两种都认。调用方只看这一两个确定的路径，不扫描。
func ProjectDirNames(cwd string) []string { return projectDirNames(cwd, filepath.EvalSymlinks) }

func projectDirNames(cwd string, resolve func(string) (string, error)) []string {
	raw := EncodeProjectDir(filepath.Clean(cwd))
	if real, err := resolve(cwd); err == nil {
		if resolved := EncodeProjectDir(filepath.Clean(real)); resolved != raw {
			return []string{resolved, raw}
		}
	}
	return []string{raw}
}

// ProjectDirName 是 ProjectDirNames 的第一个（解析之后的）。
func ProjectDirName(cwd string) string { return ProjectDirNames(cwd)[0] }

// TranscriptPath 返回 projects 目录里会话 id 在工作目录 cwd 下的会话记录：按 ProjectDirNames 的顺序取第一个存在的普通文件，
// 都不存在时为空。只看这一两个文件，不扫描别的目录。
func TranscriptPath(projects, cwd, id string) string {
	if !ValidSessionID(id) {
		return ""
	}
	for _, name := range ProjectDirNames(cwd) {
		p := filepath.Join(projects, name, id+".jsonl")
		if info, err := os.Lstat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidSessionID 报告 id 是平台给 claude 的会话 ID（小写 UUID）。
func ValidSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

// InProjects 报告 path 是 projects 目录下一层子目录里的 <会话 ID>.jsonl（claude 会话记录的位置）。
func InProjects(projects, path string) bool {
	rel, err := filepath.Rel(projects, path)
	if err != nil || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 2 && parts[0] != "." && parts[0] != ".." && strings.HasSuffix(parts[1], ".jsonl") && ValidSessionID(strings.TrimSuffix(parts[1], ".jsonl"))
}

// Probe 缓存 claude 的版本（按可执行文件的路径、大小与修改时间）与 projects 目录，避免每次启动都运行一遍。
type Probe struct {
	mu       sync.Mutex
	versions map[string]string
	projects map[string]string
}

func fileKey(bin string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return bin
	}
	return fmt.Sprintf("%s|%d|%d", bin, info.Size(), info.ModTime().UnixNano())
}

// Version 返回 bin 的版本号，按文件身份缓存。
func (p *Probe) Version(ctx context.Context, bin string, env []string) (string, error) {
	key := fileKey(bin)
	p.mu.Lock()
	v, ok := p.versions[key]
	p.mu.Unlock()
	if ok {
		return v, nil
	}
	v, err := Version(ctx, bin, env)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	if p.versions == nil {
		p.versions = map[string]string{}
	}
	p.versions[key] = v
	p.mu.Unlock()
	return v, nil
}

// ProjectsDir 返回 claude 存放会话记录的目录：以 claude auth status 报告的 projectsDirectory 为准，拿不到时按默认规则推。
func (p *Probe) ProjectsDir(ctx context.Context, bin string, env []string, home string) string {
	key := fileKey(bin) + "|" + home
	p.mu.Lock()
	dir, ok := p.projects[key]
	p.mu.Unlock()
	if ok {
		return dir
	}
	dir = DefaultProjectsDir(home)
	if a, err := AuthStatus(ctx, bin, env); err == nil && a.ProjectsDirectory != "" {
		dir = a.ProjectsDirectory
	}
	p.mu.Lock()
	if p.projects == nil {
		p.projects = map[string]string{}
	}
	p.projects[key] = dir
	p.mu.Unlock()
	return dir
}
