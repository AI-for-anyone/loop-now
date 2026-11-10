// Package authhelper prepares the isolated Pi OAuth auxiliary process. It does
// not supervise operations, persist credentials or coordinate grant refreshes.
package authhelper

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"loop-now/internal/execenv"
	"loop-now/internal/privatefs"
)

type ProcessOptions struct {
	NodeBin string
	Entry   string
	// Home is an already-created operation-owned directory, not the user's HOME.
	Home         string
	BasePath     string
	DrainTimeout time.Duration // optional bounded IPC write-drain budget
	Input        *os.File      // child fd 3, server -> helper
	Output       *os.File      // child fd 4, helper -> server (contains credentials)
}

// Command returns an unstarted child. Never send config, tokens or grant IDs via
// argv/environment. Caller owns its private directory, pipes and process group.
// Refresh requires a coordinator-owned bounded context, never an HTTP waiter
// context: cancellation after rotation starts must not discard the new token.
func Command(ctx context.Context, o ProcessOptions) (*exec.Cmd, error) {
	for _, p := range []string{o.NodeBin, o.Entry, o.Home} {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
			return nil, errors.New("auth helper paths must be absolute")
		}
	}
	if o.DrainTimeout < 0 {
		return nil, errors.New("invalid auth helper drain timeout")
	}
	if o.BasePath == "" || strings.ContainsRune(o.BasePath, 0) || o.Input == nil || o.Output == nil {
		return nil, errors.New("invalid auth helper process options")
	}
	info, err := os.Lstat(o.Home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !privatefs.IsPrivate(o.Home, info) {
		return nil, errors.New("auth helper requires a private HOME")
	}
	tmp := filepath.Join(o.Home, "tmp")
	if err := os.Mkdir(tmp, 0700); err != nil && !os.IsExist(err) {
		return nil, errors.New("cannot prepare auth helper temporary directory")
	}
	info, err = os.Lstat(tmp)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !privatefs.IsPrivate(tmp, info) {
		return nil, errors.New("auth helper temporary directory must be private")
	}
	cmd := exec.CommandContext(ctx, o.NodeBin, o.Entry)
	cmd.Dir = o.Home
	cmd.Env = append(execenv.Base(o.Home, tmp), "HOME="+o.Home, "TMPDIR="+tmp, "PATH="+o.BasePath, "NODE_ENV=production")
	if o.DrainTimeout > 0 {
		ms := o.DrainTimeout.Milliseconds()
		if ms < 1 || ms > 2147483647 {
			return nil, errors.New("invalid auth helper drain timeout")
		}
		cmd.Env = append(cmd.Env, "LOOP_NOW_AUTH_HELPER_DRAIN_MS="+strconv.FormatInt(ms, 10))
	}
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.ExtraFiles = []*os.File{o.Input, o.Output}
	return cmd, nil
}

// ProviderProcessOptions 是插件提供方订阅登录辅助进程的参数（plugins/SPEC.md §13）。
type ProviderProcessOptions struct {
	NodeBin string
	Entry   string // provider-main.mjs
	// Home 是这次登录的新状态目录（0700，已建好）：扩展把自己的状态写在这里，登录成功后整个目录归账号。
	Home string
	// Temp 与 AgentDir 是这次操作自己的临时目录与 PI_CODING_AGENT_DIR（0700，已建好），不进状态目录。
	Temp, AgentDir string
	BasePath       string
	DrainTimeout   time.Duration
	Input          *os.File // child fd 3
	Output         *os.File // child fd 4（含 login 的返回值）
}

// ProviderCommand 返回未启动的登录辅助进程：空环境，只有平台基础变量、HOME、TMPDIR、PI_CODING_AGENT_DIR 与 PATH。
func ProviderCommand(ctx context.Context, o ProviderProcessOptions) (*exec.Cmd, error) {
	for _, p := range []string{o.NodeBin, o.Entry, o.Home, o.Temp, o.AgentDir} {
		if !filepath.IsAbs(p) || strings.ContainsRune(p, 0) {
			return nil, errors.New("provider login helper paths must be absolute")
		}
	}
	if o.DrainTimeout < 0 || o.BasePath == "" || strings.ContainsRune(o.BasePath, 0) || o.Input == nil || o.Output == nil {
		return nil, errors.New("invalid provider login helper options")
	}
	for _, dir := range []string{o.Home, o.Temp, o.AgentDir} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !privatefs.IsPrivate(dir, info) {
			return nil, errors.New("provider login helper directories must be private")
		}
	}
	cmd := exec.CommandContext(ctx, o.NodeBin, o.Entry)
	cmd.Dir = o.Home
	cmd.Env = append(execenv.Base(o.Home, o.Temp), "HOME="+o.Home, "TMPDIR="+o.Temp, "PI_CODING_AGENT_DIR="+o.AgentDir, "PATH="+o.BasePath, "NODE_ENV=production")
	if o.DrainTimeout > 0 {
		ms := o.DrainTimeout.Milliseconds()
		if ms < 1 || ms > 2147483647 {
			return nil, errors.New("invalid provider login helper drain timeout")
		}
		cmd.Env = append(cmd.Env, "LOOP_NOW_AUTH_HELPER_DRAIN_MS="+strconv.FormatInt(ms, 10))
	}
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.ExtraFiles = []*os.File{o.Input, o.Output}
	return cmd, nil
}
