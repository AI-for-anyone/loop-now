package larksidecar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Incremental native coverage: use the frozen production ABI and artifact. All
// upstream traffic is mapped by the private TLS fixture dialer to loopback.
func TestNativeStreamAndExitGates(t *testing.T) {
	for _, scenario := range []string{"declaredLimit", "chunkedLimit", "idle", "totalDeadline", "consumerExit", "scopeExit", "scopeContext"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := nativeConfig(t)
			lifetime, stopScope := context.WithCancel(context.Background())
			defer stopScope()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			entered := make(chan struct{}, 1)
			released := make(chan struct{}, 1)
			var calls atomic.Int32
			_, tr := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/open-apis/authen/v1/user_info" {
					_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
					return
				}
				calls.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-stream-token" {
					t.Error("wrong bound token")
				}
				switch scenario {
				case "declaredLimit":
					w.Header().Set("Content-Length", "4096")
					w.WriteHeader(200)
					_, _ = io.WriteString(w, strings.Repeat("x", 4096))
				case "chunkedLimit":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, `{"code":0,"data":{"text":"`+strings.Repeat("x", 4096))
					w.(http.Flusher).Flush()
				default:
					w.WriteHeader(200)
					_, _ = io.WriteString(w, `{"code":0,"data":`)
					w.(http.Flusher).Flush()
					entered <- struct{}{}
					if scenario == "totalDeadline" {
						ticker := time.NewTicker(10 * time.Millisecond)
						defer ticker.Stop()
						for {
							select {
							case <-r.Context().Done():
								released <- struct{}{}
								return
							case <-ticker.C:
								if _, e := io.WriteString(w, " "); e != nil {
									released <- struct{}{}
									return
								}
								w.(http.Flusher).Flush()
							}
						}
					}
					<-r.Context().Done()
					released <- struct{}{}
				}
			}))
			limits := Limits{ResponseBytes: 1024, IdleTimeout: 120 * time.Millisecond, RequestTimeout: 500 * time.Millisecond}
			b, e := start(lifetime, testBinding(), TokenSourceFunc(func(context.Context) (string, error) { return "synthetic-stream-token", nil }), limits, tr)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			cmd := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages", "--as", "user")
			var output bytes.Buffer
			cmd.Stdout = &output
			cmd.Stderr = &output
			start := time.Now()
			if e := cmd.Start(); e != nil {
				t.Fatal("native failed to start")
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			if scenario == "consumerExit" || scenario == "scopeExit" || scenario == "scopeContext" {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("stream did not start")
				}
				switch scenario {
				case "consumerExit":
					if e := cmd.Process.Kill(); e != nil {
						t.Fatal("kill owned CLI failed")
					}
				case "scopeExit":
					_ = b.Close()
				case "scopeContext":
					stopScope()
				}
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("incomplete stream appeared successful")
				}
			case <-ctx.Done():
				t.Fatal("native failed to terminate within bound")
			}
			if calls.Load() != 1 {
				t.Fatalf("business request count %d", calls.Load())
			}
			if strings.Contains(output.String(), "synthetic-stream-token") {
				t.Fatal("token entered ordinary output")
			}
			if scenario != "declaredLimit" && scenario != "chunkedLimit" {
				select {
				case <-released:
				case <-time.After(time.Second):
					t.Fatal("upstream stream leaked")
				}
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("native exit exceeded fixture bound")
			}
			t.Logf("%s: native failed; one business request; no token output; owned resources released", scenario)
		})
	}
}

func replaceEnv(env []string, key, value string, remove bool) []string {
	out := make([]string, 0, len(env)+1)
	prefix := key + "="
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	if !remove {
		out = append(out, prefix+value)
	}
	return out
}

// Deliberately corrupt only synthetic child's env AFTER proving the production
// launcher profile; never expose this fixture mutation as a production option.
func TestNativeProviderFailureNoFallback(t *testing.T) {
	for _, scenario := range []string{"missingProxy", "badProxy", "missingKey", "badKey", "missingApp", "botIdentity", "badStrict", "badDefault"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := nativeConfig(t)
			var forwarded, userInfo atomic.Int32
			b, sourceCalls := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded.Add(1)
				if scenario == "botIdentity" && r.Method == "GET" && r.URL.Path == "/open-apis/authen/v1/user_info" {
					if r.Header.Get("Authorization") != "Bearer synthetic-broker-token" {
						t.Error("bootstrap changed captured user")
					}
					userInfo.Add(1)
					_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
					return
				}
				http.Error(w, "unexpected synthetic route", 500)
			}), Limits{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages", "--as", "user")
			switch scenario {
			case "missingProxy":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_AUTH_PROXY", "", true)
			case "badProxy":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_AUTH_PROXY", "http://127.0.0.1:0?invalid=1", false)
			case "missingKey":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_PROXY_KEY", "", true)
			case "badKey":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_PROXY_KEY", strings.Repeat("b", 64), false)
			case "missingApp":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_APP_ID", "", true)
			case "botIdentity":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_STRICT_MODE", "bot", false)
			case "badStrict":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_STRICT_MODE", "invalid", false)
			case "badDefault":
				cmd.Env = replaceEnv(cmd.Env, "LARKSUITE_CLI_DEFAULT_AS", "invalid", false)
			}
			output, e := runNative(t, cmd)
			if e == nil {
				t.Fatalf("%s native succeeded", scenario)
			}
			if ctx.Err() != nil {
				t.Fatal("provider failure only timed out")
			}
			// Official ResolveAccount verifies UAT user_info before strict
			// command identity selection (credential_provider.go197/225).
			// Strict=bot cannot change this capability to TAT: allow precisely
			// that captured-user bootstrap read, and require zero business.
			expected := int32(0)
			if scenario == "botIdentity" {
				expected = 1
			}
			if forwarded.Load() != expected || sourceCalls.Load() != expected || userInfo.Load() != expected {
				t.Fatalf("provider boundary source=%d upstream=%d userinfo=%d expected=%d", sourceCalls.Load(), forwarded.Load(), userInfo.Load(), expected)
			}
			for _, entry := range cmd.Env {
				if strings.HasPrefix(entry, "LARKSUITE_CLI_PROXY_KEY=") && strings.Contains(output, strings.TrimPrefix(entry, "LARKSUITE_CLI_PROXY_KEY=")) {
					t.Fatal("capability leaked in native error")
				}
			}
			t.Log(fmt.Sprintf("%s: explicit native failure; source/upstream=%d; business=0; no bot or host fallback success", scenario, expected))
		})
	}
}

// Compile-time guard: Command's actual type/ABI remains unchanged.
var _ func(context.Context, LaunchConfig, ...string) (*exec.Cmd, error) = (*Broker)(nil).Command

func TestNativeOfficialBotArtifactBoundary(t *testing.T) {
	archive := os.Getenv("LARK_OFFICIAL_RELEASE_ARCHIVE")
	if archive == "" {
		t.Skip("explicit fixed official release archive required")
	}
	cfg := nativeConfig(t)
	f, e := os.Open(archive)
	if e != nil {
		t.Fatal("official archive unavailable")
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil || hex.EncodeToString(h.Sum(nil)) != "cdf66d35c950c56f17cc180276c16b8ad4b7241eab715d809114cb57c88a825b" {
		t.Fatal("official release digest mismatch")
	}
	_, _ = f.Seek(0, io.SeekStart)
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	tar := tar.NewReader(gz)
	binary := filepath.Join(cfg.Home, "official-bot-cli")
	found := false
	for {
		hdr, e := tar.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal("official archive read")
		}
		if hdr.Name != "lark-cli" {
			continue
		}
		if !hdr.FileInfo().Mode().IsRegular() || hdr.Size > 128<<20 {
			t.Fatal("invalid official binary member")
		}
		out, e := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
		if e != nil {
			t.Fatal(e)
		}
		n, e := io.Copy(out, io.LimitReader(tar, (128<<20)+1))
		out.Close()
		if e != nil || n != hdr.Size {
			t.Fatal("official binary extraction failed")
		}
		found = true
		break
	}
	if !found {
		t.Fatal("official binary absent")
	}
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("old bot used user broker") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	template := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages")
	baseEnv := []string{"HOME=" + cfg.Home, "TMPDIR=" + cfg.Home, "PATH=/usr/bin:/bin", "LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1", "LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1", "LARKSUITE_CLI_REMOTE_META=off"}
	command := func(args []string, env []string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", template.Args[2], binary}, args...)...)
		cmd.Dir = cfg.Home
		cmd.Env = env
		return cmd
	}
	version, e := runNative(t, command([]string{"--version"}, baseEnv))
	if e != nil || !strings.Contains(version, "1.0.94") {
		t.Fatal("official bot version failed")
	}
	env := append(append([]string(nil), baseEnv...), "LARKSUITE_CLI_APP_ID=cli_synthetic_bot", "LARKSUITE_CLI_APP_SECRET=synthetic-bot-app-secret", "LARKSUITE_CLI_BRAND=feishu", "LARKSUITE_CLI_DEFAULT_AS=bot", "LARKSUITE_CLI_STRICT_MODE=bot")
	dry, e := runNative(t, command([]string{"api", "GET", "/open-apis/im/v1/messages", "--as", "bot", "--dry-run"}, env))
	if e != nil || !strings.Contains(dry, "/open-apis/im/v1/messages") || strings.Contains(dry, "synthetic-bot-app-secret") {
		t.Fatal("official bot dry-run boundary failed")
	}
	// The official release must refuse a user-sidecar environment rather than
	// silently acting like the separately built platform user variant.
	rejectEnv := append(env, "LARKSUITE_CLI_AUTH_PROXY="+b.URL(), "LARKSUITE_CLI_PROXY_KEY="+b.Capability())
	_, e = runNative(t, command([]string{"--version"}, rejectEnv))
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 {
		t.Fatal("official release accepted sidecar environment")
	}
	if calls.Load() != 0 {
		t.Fatal("official bot crossed into user grant")
	}
	t.Log("official archive SHA verified; version=1.0.94; bot-only dry-run success; user-sidecar env rejected exit2; no token/QA/provider call claimed")
}

func TestOfficialMiddlewareNoSentinelConfinement(t *testing.T) {
	probe := os.Getenv("LARK_ROUTE_FAULT_PROBE")
	if probe == "" {
		t.Skip("explicit official route probe required")
	}
	cfg := nativeConfig(t)
	var originalRequests atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalRequests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get(headerSignature) != "" {
			t.Error("non-sentinel control contained auth")
		}
		_, _ = io.WriteString(w, "fixture-only")
	}))
	defer original.Close()
	b, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("non-sentinel used captured token") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	template := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages")
	env := append(template.Env, "PLATFORM_TEST_TARGET="+strings.TrimPrefix(original.URL, "http://"))
	control := exec.CommandContext(ctx, probe, "-test.run=^TestPlatformNoSentinelChild$")
	control.Env = env
	if _, e := runNative(t, control); e != nil || originalRequests.Load() != 1 {
		t.Fatal("official no-sentinel control not proven")
	}
	confined := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", template.Args[2], probe, "-test.run=^TestPlatformNoSentinelChild$")
	confined.Env = append(env, "PLATFORM_TEST_DENY=1")
	if _, e := runNative(t, confined); e != nil || originalRequests.Load() != 1 || calls.Load() != 0 {
		t.Fatal("production profile did not confine no-sentinel route")
	}
	t.Log("official production middleware no-sentinel control=1 owned unauthenticated request; confined=0 further requests; source=0; not a normal CLI subcommand injection")
}

func TestNativeBoundedInputSnapshotMemory(t *testing.T) {
	cfg := nativeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var business atomic.Int32
	payload := `{"text":"` + strings.Repeat("s", 32<<10) + `"}`
	input := filepath.Join(cfg.WorkingDir, "input.json")
	if e := os.WriteFile(input, []byte(payload), 0600); e != nil {
		t.Fatal(e)
	}
	b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
			return
		}
		business.Add(1)
		body, e := io.ReadAll(r.Body)
		if e != nil || !bytes.Contains(body, []byte(strings.Repeat("s", 32<<10))) || bytes.Contains(body, []byte("changed-input")) {
			t.Error("native did not use bounded snapshot")
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}), Limits{RequestBytes: 64 << 10})
	cmd := sandboxCommand(t, ctx, b, cfg, "api", "POST", "/open-apis/im/v1/messages", "--as", "user", "--data", "@"+input)
	// Growing/replacing the source after Command cannot increase the native input:
	// production now holds only the already-bounded literal snapshot in argv.
	if e := os.WriteFile(input, []byte(`{"text":"changed-input`+strings.Repeat("x", 2<<20)+`"}`), 0600); e != nil {
		t.Fatal(e)
	}
	output, e := runNative(t, cmd)
	if e != nil || business.Load() != 1 {
		t.Fatal("bounded native input failed")
	}
	if strings.Contains(output, "synthetic-broker-token") {
		t.Fatal("token in native output")
	}
	usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || usage.Maxrss <= 0 {
		t.Fatal("native resource observation unavailable")
	}
	// On the sole admitted platform (darwin), ru_maxrss is bytes. This is an
	// observed fixture upper guard, not a production RLIMIT/SDK memory guarantee.
	if usage.Maxrss > 512<<20 {
		t.Fatal("32KiB fixture exceeded conservative observed RSS guard")
	}
	t.Logf("actual native input=32KiB snapshot; source grew past 2MiB after capture; business=1; darwin maxrss=%d bytes (observation, not production hard quota)", usage.Maxrss)
}
