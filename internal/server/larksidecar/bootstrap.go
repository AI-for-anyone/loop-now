package larksidecar

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const LauncherScopeEnv = "LOOP_NOW_LARK_SCOPE_FILE"
const LauncherProtocol = "loop-now-lark-launcher-v1"
const launcherDescriptorLimit = 16 << 10

// LauncherDescriptor is private scope data, NOT a public credential DTO. The
// server writes it once (0600, in a private 0700 directory outside workspace),
// pins all artifacts from the install manifest, and removes it on scope release.
// There is deliberately no grant selector, access/refresh token or app secret.
// Same-uid agents can read the scope capability, as with the native environment;
// the broker's captured Binding remains the authorization authority.
type LauncherDescriptor struct {
	Protocol       string       `json:"protocol"`
	BrokerURL      string       `json:"brokerURL"`
	Capability     string       `json:"capability"`
	AppID          string       `json:"appId"`
	UserID         string       `json:"userId"`
	Brand          string       `json:"brand"`
	RequestBytes   int64        `json:"requestBytes"`
	LauncherSHA256 string       `json:"launcherSHA256"`
	Launch         LaunchConfig `json:"launch"`
}

// LauncherScope exports only the per-scope signing capability and trusted
// launch metadata. It does not claim admission/supported: each PATH invocation
// performs the same production admission as Broker.Command before any native exec.
// launcherSHA256 must come from the installed platform manifest, never CLI args.
func (b *Broker) LauncherScope(cfg LaunchConfig, launcherSHA256 string) (LauncherDescriptor, error) {
	if cfg.HostHome == "" {
		var e error
		cfg.HostHome, e = os.UserHomeDir()
		if e != nil {
			return LauncherDescriptor{}, launcherRejected()
		}
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	d := LauncherDescriptor{Protocol: LauncherProtocol, BrokerURL: b.address, Capability: b.key, AppID: b.binding.AppID, UserID: b.binding.UserID, Brand: b.binding.Brand, RequestBytes: b.limits.RequestBytes, LauncherSHA256: launcherSHA256, Launch: cfg}
	d.Launch.InputRoots = append([]string(nil), cfg.InputRoots...)
	if closed || !d.valid() {
		return LauncherDescriptor{}, launcherRejected()
	}
	return d, nil
}

// ExportLaunchDescriptor is the server-facing name for the same descriptor
// export. LauncherScope remains available for the first integration handoff.
func (b *Broker) ExportLaunchDescriptor(cfg LaunchConfig, launcherSHA256 string) (LauncherDescriptor, error) {
	return b.LauncherScope(cfg, launcherSHA256)
}

func launcherRejected() error { return errors.New("lark user launcher rejected") }

func (d LauncherDescriptor) valid() bool {
	u, e := url.Parse(d.BrokerURL)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	host, port, e := net.SplitHostPort(u.Host)
	n, pe := strconv.ParseUint(port, 10, 16)
	if e != nil || host != "127.0.0.1" || pe != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return false
	}
	key, ke := hex.DecodeString(d.Capability)
	sha, se := hex.DecodeString(d.LauncherSHA256)
	return d.Protocol == LauncherProtocol && ke == nil && len(key) == 32 && len(d.Capability) == 64 && se == nil && len(sha) == 32 && len(d.LauncherSHA256) == 64 && d.AppID != "" && !strings.ContainsAny(d.AppID, "\x00\r\n") && d.UserID != "" && !strings.ContainsAny(d.UserID, "\x00\r\n") && (d.Brand == "feishu" || d.Brand == "lark") && d.RequestBytes > 0 && d.RequestBytes < math.MaxInt64 && filepath.IsAbs(d.Launch.HostHome) && d.Launch.HostHome == filepath.Clean(d.Launch.HostHome) && d.Launch.HostHome != d.Launch.Home
}

// readLauncherDescriptor never follows a path symlink, opens FIFOs without
// blocking, and validates ownership/mode on the fd actually read. The private
// parent directory may not be the business workspace or a public temp directory.
func readLauncherDescriptor(path string) (LauncherDescriptor, error) {
	var d LauncherDescriptor
	if !filepath.IsAbs(path) || path != filepath.Clean(path) {
		return d, launcherRejected()
	}
	root, e := openScopeRoot(filepath.Dir(path))
	if e != nil {
		return d, launcherRejected()
	}
	defer unix.Close(root)
	var parent unix.Stat_t
	if unix.Fstat(root, &parent) != nil || parent.Mode&0077 != 0 {
		return d, launcherRejected()
	}
	f, e := openInputAt(root, filepath.Base(path))
	if e != nil {
		return d, launcherRejected()
	}
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || int(st.Uid) != os.Geteuid() || st.Mode&07777 != 0600 || st.Nlink != 1 || st.Size <= 0 || st.Size > launcherDescriptorLimit {
		return d, launcherRejected()
	}
	raw, e := io.ReadAll(io.LimitReader(f, launcherDescriptorLimit+1))
	if e != nil || len(raw) > launcherDescriptorLimit || !uniqueJSONKeys(raw) {
		return d, launcherRejected()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || dec.Decode(new(any)) != io.EOF || !d.valid() {
		return LauncherDescriptor{}, launcherRejected()
	}
	// Never place this capability inside the agent-editable business workspace.
	rel, e := filepath.Rel(d.Launch.WorkingDir, path)
	if e != nil || !filepath.IsAbs(d.Launch.WorkingDir) || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return LauncherDescriptor{}, launcherRejected()
	}
	return d, nil
}

// Reject duplicate object keys at every nesting level, rather than accepting a
// descriptor whose effective identity/configuration depends on decoder behavior.
func uniqueJSONKeys(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value func() bool
	value = func() bool {
		tok, e := dec.Token()
		if e != nil {
			return false
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for dec.More() {
				key, e := dec.Token()
				s, ok := key.(string)
				if e != nil || !ok || seen[s] {
					return false
				}
				seen[s] = true
				if !value() {
					return false
				}
			}
		case '[':
			for dec.More() {
				if !value() {
					return false
				}
			}
		default:
			return false
		}
		end, e := dec.Token()
		return e == nil && (delim == '{' && end == json.Delim('}') || delim == '[' && end == json.Delim(']'))
	}
	if !value() {
		return false
	}
	_, e := dec.Token()
	return e == io.EOF
}

func launcherCommand(ctx context.Context, path, self string, args []string) (*exec.Cmd, error) {
	d, e := readLauncherDescriptor(path)
	if e != nil || validateExecutable(self, d.LauncherSHA256) != nil {
		return nil, launcherRejected()
	}
	// This is a launch-only view. It cannot start a listener or resolve a token.
	// Exactly the same Command implementation performs artifact, workspace,
	// parameter, bounded-input and real OS sandbox admission.
	b := &Broker{address: d.BrokerURL, key: d.Capability, binding: Binding{AppID: d.AppID, UserID: d.UserID, Brand: d.Brand}, limits: Limits{RequestBytes: d.RequestBytes}}
	cmd, e := b.Command(ctx, d.Launch, args...)
	if e != nil || ctx.Err() != nil {
		return nil, launcherRejected()
	}
	return cmd, nil
}

// StandaloneCommand is for the separate launcher executable, not the server:
// it verifies that executable against descriptor.launcherSHA256. The returned
// command is the sandbox command; callers must never extract Env to run native.
func StandaloneCommand(ctx context.Context, path string, args ...string) (*exec.Cmd, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, launcherRejected()
	}
	self, e := os.Executable()
	if e != nil {
		return nil, launcherRejected()
	}
	return launcherCommand(ctx, path, self, args)
}

// ExecLauncher is the private PATH bootstrap entry. It replaces itself with the
// exact sandbox command, retaining the Pi/bash process group and standard fds.
// There is no extra supervising child to orphan and no Env+bare-native fallback.
// The server still owns scope/broker lifetime and process-group cancellation.
func ExecLauncher(ctx context.Context, path string, args []string) error {
	cmd, e := StandaloneCommand(ctx, path, args...)
	if e != nil || ctx.Err() != nil {
		return launcherRejected()
	}
	if syscall.Chdir(cmd.Dir) != nil || ctx.Err() != nil {
		return launcherRejected()
	}
	if syscall.Exec(cmd.Path, cmd.Args, cmd.Env) != nil {
		return launcherRejected()
	}
	return nil // Successful exec does not return.
}
