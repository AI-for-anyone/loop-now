package larksidecar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type LaunchConfig struct {
	Binary, BinarySHA256, Home, WorkingDir, ProbeBinary, ProbeSHA256 string
	// HostHome is captured by the trusted server, not inherited from Pi/bash.
	// An independent bootstrap already has the isolated HOME in its environment.
	HostHome                       string
	InputRoots                     []string
	LauncherBinary, LauncherSHA256 string
}

// Command validates the captured private launch configuration, builds a clean
// environment, and returns an unstarted command. Caller owns process-group
// termination/wait and must close the broker on runtime release. No inherited
// server environment, app secret, user token, profile, proxy or CA is passed.
func (b *Broker) Command(ctx context.Context, cfg LaunchConfig, args ...string) (*exec.Cmd, error) {
	return b.commandWithSandbox(ctx, cfg, "/usr/bin/sandbox-exec", args...)
}

// Only tests can select a missing executable; exported production entry pins it.
func (b *Broker) commandWithSandbox(ctx context.Context, cfg LaunchConfig, sandbox string, args ...string) (*exec.Cmd, error) {
	reject := func() (*exec.Cmd, error) { return nil, errors.New("invalid lark user launcher") }
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	u, e := url.Parse(b.address)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || closed || len(b.key) != 64 {
		return reject()
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, portErr := strconv.ParseUint(port, 10, 16)
	if e != nil || host != "127.0.0.1" || portErr != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return reject()
	}
	if !filepath.IsAbs(cfg.Binary) || !filepath.IsAbs(cfg.Home) || cfg.Home != filepath.Clean(cfg.Home) || cfg.WorkingDir != filepath.Clean(cfg.WorkingDir) || !filepath.IsAbs(cfg.WorkingDir) || cfg.WorkingDir == cfg.Home || len(cfg.InputRoots) != 1 || cfg.InputRoots[0] != cfg.WorkingDir || len(cfg.BinarySHA256) != 64 {
		return reject()
	}
	// Private HOME and business workspace are separate runtime directories.
	for _, pair := range [][2]string{{cfg.Home, cfg.WorkingDir}, {cfg.WorkingDir, cfg.Home}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return reject()
		}
	}
	for _, p := range []string{cfg.Home, cfg.WorkingDir, cfg.Binary} {
		real, e := filepath.EvalSymlinks(p)
		if e != nil || real != filepath.Clean(p) {
			return reject()
		}
	}
	hostHome := cfg.HostHome
	if hostHome == "" {
		var homeErr error
		hostHome, homeErr = os.UserHomeDir()
		if homeErr != nil {
			return reject()
		}
	}
	if !filepath.IsAbs(hostHome) || hostHome != filepath.Clean(hostHome) || hostHome == cfg.Home {
		return reject()
	}
	info, e := os.Stat(cfg.Home)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return reject()
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return reject()
	}
	f, e := os.Open(cfg.Binary)
	if e != nil {
		return reject()
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != cfg.BinarySHA256 {
		return reject()
	}
	args, e = boundedInputsWithRoots(args, cfg.WorkingDir, cfg.InputRoots, b.limits.RequestBytes)
	if e != nil || !safeArgs(args, b.limits.RequestBytes) {
		return reject()
	}
	// Only the proven platform is admitted. The official interceptor has
	// pass-through branches, so network confinement is a production requirement,
	// not just a fixture patch. No DNS, provider or other loopback port is allowed.
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return reject()
	}
	if _, e := os.Stat(sandbox); e != nil {
		return reject()
	}
	profile := fmt.Sprintf(`(version 1)(allow default)(deny network*)(allow network-outbound (remote tcp "localhost:%s"))(deny process-exec (literal "/usr/bin/security"))`, port)
	for _, path := range []string{filepath.Join(hostHome, ".lark-cli"), filepath.Join(hostHome, ".config", "lark-cli"), filepath.Join(hostHome, "Library", "Application Support", "lark-cli"), filepath.Join(hostHome, "Library", "Keychains")} {
		// SBPL strings require quoted escaping; no user-provided program text.
		profile += fmt.Sprintf("(deny file-read* (subpath %q))", path)
	}
	if e := b.admitSandboxWith(ctx, cfg, profile, sandbox); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, sandbox, append([]string{"-p", profile, cfg.Binary}, args...)...)
	cmd.Dir = cfg.WorkingDir
	cmd.Env = []string{"HOME=" + cfg.Home, "TMPDIR=" + cfg.Home, "PATH=/usr/bin:/bin", "GODEBUG=netdns=go", "LARKSUITE_CLI_AUTH_PROXY=" + b.address, "LARKSUITE_CLI_PROXY_KEY=" + b.key, "LARKSUITE_CLI_APP_ID=" + b.binding.AppID, "LARKSUITE_CLI_BRAND=" + b.binding.Brand, "LARKSUITE_CLI_STRICT_MODE=user", "LARKSUITE_CLI_DEFAULT_AS=user", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "LARKSUITE_CLI_REMOTE_META=off"}
	return cmd, nil
}
func safeArgs(args []string, max int64) bool {
	if len(args) == 0 {
		return false
	}
	// Management commands cannot change this process's captured identity. Support
	// business commands; the broker independently limits the authenticated API.
	switch args[0] {
	case "api", "im":
	default:
		return false
	}
	var total int64
	for i, a := range args {
		total += int64(len(a))
		if total > max {
			return false
		}
		flag, value, has := strings.Cut(a, "=")
		switch flag {
		case "--profile", "--config", "--config-dir", "--env", "--app-id", "--app-secret", "--user-access-token", "--tenant-access-token", "--brand", "--proxy", "--proxy-address", "--ca", "--ca-path":
			return false
		case "--as":
			if !has {
				if i+1 >= len(args) {
					return false
				}
				value = args[i+1]
			}
			if value != "user" {
				return false
			}
		}
	}
	return true
}

// The official sender pre-buffers input. Snapshot JSON file inputs with a
// bounded read into bounded argv before starting it; never feed a growing file
// or unbounded stdin/multipart reader to that sender. This is a finite QA-text
// launcher, not a promise of arbitrary streaming upload support.
func boundedInputsWithRoots(args []string, home string, roots []string, max int64) ([]string, error) {
	var total int64
	for _, a := range args {
		if int64(len(a)) > max-total {
			return nil, errors.New("input budget exceeded")
		}
		total += int64(len(a))
	}
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		flag, value, inline := strings.Cut(out[i], "=")
		if flag == "--file" || flag == "--attachment" || flag == "--image" || flag == "--video" || flag == "--video-cover" || flag == "--audio" {
			return nil, errors.New("unsupported streaming input")
		}
		if flag != "--data" && flag != "--params" {
			continue
		}
		index := i
		if !inline {
			i++
			if i >= len(out) {
				return nil, errors.New("missing input")
			}
			index = i
			value = out[i]
		}
		if value == "-" {
			return nil, errors.New("unsupported streaming input")
		}
		if strings.HasPrefix(value, "@") {
			path := strings.TrimPrefix(value, "@")
			if !filepath.IsAbs(path) {
				path = filepath.Join(home, path)
			}
			// A FIFO must not block before Stat can reject it. Nonblocking
			// open also closes the Stat/Open race; inspect this same fd.
			// The supported platform is Unix; no following an input symlink.
			f, e := openScopeInput(path, home, roots)
			if e != nil {
				return nil, errors.New("input unavailable")
			}
			info, e := f.Stat()
			if e != nil || !info.Mode().IsRegular() {
				f.Close()
				return nil, errors.New("input unavailable")
			}
			remaining := max - total + int64(len(value))
			raw, e := io.ReadAll(io.LimitReader(f, remaining+1))
			f.Close()
			if e != nil || int64(len(raw)) > remaining || !json.Valid(raw) {
				return nil, errors.New("invalid bounded input")
			}
			total += int64(len(raw)) - int64(len(value))
			value = string(raw)
			if inline {
				out[index] = flag + "=" + value
			} else {
				out[index] = value
			}
		}
	}
	return out, nil
}

func validateExecutable(path, digest string) error {
	if !filepath.IsAbs(path) || len(digest) != 64 {
		return errors.New("invalid executable")
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil || real != filepath.Clean(path) {
		return errors.New("invalid executable")
	}
	f, e := os.Open(path)
	if e != nil {
		return errors.New("invalid executable")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("invalid executable")
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("invalid executable")
	}
	return nil
}
