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

// DefaultUserPathTimeout is the time limit for reading PATH from the login shell.
const DefaultUserPathTimeout = 10 * time.Second

// UserPath resolves and caches the user's PATH (once per server process). When the desktop app is started from Finder,
// the process's own PATH is very short, so on Unix it is read from the user's login shell ($SHELL -l -i), falling back
// to the server process's own PATH on timeout or failure; on Windows the process's PATH is used directly (that is, the
// system and user PATH merged at login). Only absolute paths are kept, deduplicated, in their original order.
type UserPath struct {
	timeout time.Duration
	log     *slog.Logger
	once    sync.Once
	done    chan struct{}
	dirs    []string
	// resolve reads the raw PATH text and its source; tests can replace it.
	resolve func(ctx context.Context) (raw, source string, err error)
}

// NewUserPath creates the resolver; a timeout of 0 means DefaultUserPathTimeout.
func NewUserPath(log *slog.Logger, timeout time.Duration) *UserPath {
	if timeout <= 0 {
		timeout = DefaultUserPathTimeout
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &UserPath{timeout: timeout, log: log, done: make(chan struct{}), resolve: loginShellPath}
}

// Start begins resolving in the background (only once); if it is never called, the first Dirs call triggers it.
func (u *UserPath) Start() {
	u.once.Do(func() { go u.run() })
}

// Dirs waits for resolution to finish and returns the directories of the user's PATH; empty if ctx ends first.
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

// splitDirs splits PATH by the platform's separator, keeps only absolute paths (dropping . and relative directories),
// and normalizes and deduplicates them (case-insensitive on Windows, keeping the spelling that comes first).
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
