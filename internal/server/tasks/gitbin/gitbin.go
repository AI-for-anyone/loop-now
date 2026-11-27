// Package gitbin finds a working git on this computer for task workspaces. On macOS /usr/bin/git is a stub that opens
// the "install developer tools" dialog when the tools are missing, so it is only run when `xcode-select -p` names an
// existing folder.
package gitbin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Finder answers whether a working git exists and where. A found git is remembered; a missing one is looked for
// again on the next call, so installing git later is noticed without a restart.
type Finder struct {
	// Configured is the git the server was started with (tried first); empty to rely on Dirs.
	Configured string
	// Dirs gives the folders to search for git after Configured; nil means the server's PATH.
	Dirs func(ctx context.Context) []string
	// SystemStub is the git that is only a stub without the developer tools: /usr/bin/git on macOS, none elsewhere.
	SystemStub string
	// XcodeSelect prints the developer tools folder; /usr/bin/xcode-select on macOS.
	XcodeSelect string
	// Timeout bounds each probe; 5 seconds when zero.
	Timeout time.Duration

	mu    sync.Mutex
	found string
}

// New returns a Finder with the platform's defaults.
func New(configured string, dirs func(ctx context.Context) []string) *Finder {
	f := &Finder{Configured: configured, Dirs: dirs}
	if runtime.GOOS == "darwin" {
		f.SystemStub, f.XcodeSelect = "/usr/bin/git", "/usr/bin/xcode-select"
	}
	return f
}

// Available returns the path of a git that runs `git --version` successfully, trying Configured and then each folder
// of Dirs in order; ok is false when there is none.
func (f *Finder) Available(ctx context.Context) (path string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.found != "" {
		return f.found, true
	}
	for _, c := range f.candidates(ctx) {
		if f.works(ctx, c) {
			f.found = c
			return c, true
		}
	}
	return "", false
}

// Path is the remembered working git, or Configured when none was found yet (for operations on a workspace that git
// already made).
func (f *Finder) Path() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.found != "" {
		return f.found
	}
	if f.Configured != "" {
		return f.Configured
	}
	return "git"
}

func (f *Finder) candidates(ctx context.Context) []string {
	var dirs []string
	if f.Dirs != nil {
		dirs = f.Dirs(ctx)
	} else {
		dirs = filepath.SplitList(os.Getenv("PATH"))
	}
	out, seen := []string{}, map[string]bool{}
	add := func(p string) {
		if p == "" || !filepath.IsAbs(p) || seen[p] {
			return
		}
		seen[p] = true
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			out = append(out, p)
		}
	}
	add(f.Configured)
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	for _, d := range dirs {
		add(filepath.Join(d, name))
	}
	return out
}

func (f *Finder) works(ctx context.Context, git string) bool {
	if f.isStub(git) && !f.developerToolsInstalled(ctx) {
		return false
	}
	out, err := f.run(ctx, git, "--version")
	return err == nil && strings.HasPrefix(out, "git version")
}

// isStub reports whether git is, or links to, the system stub.
func (f *Finder) isStub(git string) bool {
	if f.SystemStub == "" {
		return false
	}
	if git == f.SystemStub {
		return true
	}
	real, err := filepath.EvalSymlinks(git)
	if err != nil {
		return true // unreadable: do not risk running it
	}
	stub, err := filepath.EvalSymlinks(f.SystemStub)
	if err != nil {
		stub = f.SystemStub
	}
	return real == stub
}

// developerToolsInstalled runs `xcode-select -p`, which never opens a dialog, and checks the folder it prints.
func (f *Finder) developerToolsInstalled(ctx context.Context) bool {
	if f.XcodeSelect == "" {
		return false
	}
	out, err := f.run(ctx, f.XcodeSelect, "-p")
	if err != nil || out == "" {
		return false
	}
	info, err := os.Stat(out)
	return err == nil && info.IsDir()
}

func (f *Finder) run(ctx context.Context, name string, args ...string) (string, error) {
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
