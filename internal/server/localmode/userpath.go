package localmode

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"loop-now/internal/execenv"
)

// DefaultUserPathTimeout 是从登录 shell 读取 PATH 的时限。
const DefaultUserPathTimeout = 10 * time.Second

// UserPath 解析并缓存用户的 PATH（每个服务端进程解析一次）。桌面版从 Finder 启动时进程自己的 PATH 很短，
// 所以 Unix 上从用户的登录 shell（$SHELL -l -i）读取，超时或失败时退回服务端进程自己的 PATH；Windows 上直接取进程的 PATH
// （即登录时系统与用户 PATH 的合并结果）。只保留绝对路径，去重，保持原顺序。
type UserPath struct {
	timeout time.Duration
	log     *slog.Logger
	once    sync.Once
	done    chan struct{}
	dirs    []string
	// resolve 读取原始 PATH 文本与来源；测试可替换。
	resolve func(ctx context.Context) (raw, source string, err error)
}

// NewUserPath 创建解析器；timeout 为 0 时取 DefaultUserPathTimeout。
func NewUserPath(log *slog.Logger, timeout time.Duration) *UserPath {
	if timeout <= 0 {
		timeout = DefaultUserPathTimeout
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &UserPath{timeout: timeout, log: log, done: make(chan struct{}), resolve: loginShellPath}
}

// Start 在后台开始解析（只解析一次）；不调用时第一次 Dirs 会触发。
func (u *UserPath) Start() {
	u.once.Do(func() { go u.run() })
}

// Dirs 等待解析完成并返回用户 PATH 的目录；ctx 先结束时返回空。
func (u *UserPath) Dirs(ctx context.Context) []string {
	u.Start()
	select {
	case <-u.done:
		return slices.Clone(u.dirs)
	case <-ctx.Done():
		return nil
	}
}

func (u *UserPath) run() {
	defer close(u.done)
	ctx, cancel := context.WithTimeout(context.Background(), u.timeout)
	defer cancel()
	raw, source, err := u.resolve(ctx)
	dirs := splitDirs(raw)
	if err != nil || len(dirs) == 0 {
		u.log.Warn("没有读到登录 shell 的 PATH，“用本机的 CLI”改用服务端进程自己的 PATH", "err", err)
		dirs, source = splitDirs(os.Getenv("PATH")), "process"
	}
	u.dirs = dirs
	u.log.Info("已解析用户的 PATH", "source", source, "dirs", len(dirs))
}

// splitDirs 按平台的分隔符拆开 PATH，只保留绝对路径（去掉 . 与相对目录），规范化并去重（Windows 上不分大小写，保留先出现的写法）。
func splitDirs(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(raw) {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		if k := execenv.PathKey(d); !seen[k] {
			seen[k] = true
			out = append(out, filepath.Clean(d))
		}
	}
	return out
}
