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
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, errors.New("auth helper requires a private HOME")
	}
	tmp := filepath.Join(o.Home, "tmp")
	if err := os.Mkdir(tmp, 0700); err != nil && !os.IsExist(err) {
		return nil, errors.New("cannot prepare auth helper temporary directory")
	}
	info, err = os.Lstat(tmp)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return nil, errors.New("auth helper temporary directory must be private")
	}
	cmd := exec.CommandContext(ctx, o.NodeBin, o.Entry)
	cmd.Dir = o.Home
	cmd.Env = []string{"HOME=" + o.Home, "TMPDIR=" + tmp, "PATH=" + o.BasePath, "NODE_ENV=production"}
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
