package larksidecar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Only subprocess capability probes use this branch; both targets are owned
// loopback listeners. No DNS, provider, token endpoint or host config is read.
func TestNativeNetworkCapabilityChild(t *testing.T) {
	if os.Getenv("LARK_TEST_DIAL") == "" {
		return
	}
	c, e := net.DialTimeout("tcp", os.Getenv("LARK_TEST_DIAL"), 300*time.Millisecond)
	if e != nil {
		os.Exit(42)
	}
	c.Close()
	os.Exit(0)
}
func sandboxProfile(t *testing.T, home string, ports ...string) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`(version 1)(allow default)(deny network*)`)
	for _, p := range ports {
		fmt.Fprintf(&sb, `(allow network-outbound (remote tcp "localhost:%s"))`, p)
	}
	sb.WriteString(`(deny process-exec (literal "/usr/bin/security"))(deny file-read* (subpath "/Users/ljl/.config") (subpath "/Users/ljl/.lark-cli") (subpath "/Users/ljl/Library/Keychains"))`)
	p := filepath.Join(home, "network.sb")
	if e := os.WriteFile(p, []byte(sb.String()), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func sandboxGate(t *testing.T, home, profile, allowed string) {
	t.Helper()
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		addr  string
		allow bool
	}{{allowed, true}, {ln.Addr().String(), false}} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, self, "-test.run=^TestNativeNetworkCapabilityChild$")
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LARK_TEST_DIAL=" + test.addr}
		e = cmd.Run()
		cancel()
		if test.allow && e != nil {
			t.Fatal("sandbox allow preflight failed")
		}
		if !test.allow {
			if x, ok := e.(*exec.ExitError); !ok || x.ExitCode() != 42 {
				t.Fatal("sandbox deny preflight not proven")
			}
		}
	}
}
func nativeConfig(t *testing.T) LaunchConfig {
	t.Helper()
	binary := os.Getenv("LARK_USER_VARIANT_BINARY")
	if binary == "" {
		t.Skip("explicit platform artifact required")
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Fatal("unverified platform")
	}
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(binary), "manifest.json"))
	if e != nil {
		t.Fatal("manifest unavailable")
	}
	var manifest struct {
		BinarySHA256, SourceCommit, SourceSHA256, ArtifactVersion string
		SandboxProbe                                              struct{ File, SHA256 string }
		OfficialRelease, SourceModified                           bool
		BuildTags                                                 []string
	}
	if json.Unmarshal(raw, &manifest) != nil || manifest.SourceCommit != "f065bf5b645af381f9b7475ce721451e6ca36a23" || manifest.SourceSHA256 != "997aaddb0e74cc17903ee463c9bd6554ee671d7bdd5e0aa58343e74219ee46dd" || manifest.ArtifactVersion != Version || manifest.OfficialRelease || manifest.SourceModified || strings.Join(manifest.BuildTags, ",") != "authsidecar" {
		t.Fatal("variant provenance invalid")
	}
	scope, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(scope, "home")
	workspace := filepath.Join(scope, "workspace")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(workspace, 0700); e != nil {
		t.Fatal(e)
	}
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	// Synthetic local fallback trap only; never touch host login files.
	trap := filepath.Join(home, ".lark-cli")
	if e := os.Mkdir(trap, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(trap, "config.json"), []byte(`{"apps":[{"appId":"synthetic-host-trap","appSecret":"synthetic-refresh-secret","brand":"lark","strictMode":"bot","defaultAs":"bot","users":[]}]}`), 0600); e != nil {
		t.Fatal(e)
	}
	return LaunchConfig{Binary: binary, BinarySHA256: manifest.BinarySHA256, Home: home, WorkingDir: workspace, InputRoots: []string{workspace}, ProbeBinary: filepath.Join(filepath.Dir(binary), manifest.SandboxProbe.File), ProbeSHA256: manifest.SandboxProbe.SHA256}
}
func sandboxCommand(t *testing.T, ctx context.Context, b *Broker, cfg LaunchConfig, args ...string) *exec.Cmd {
	t.Helper()
	cmd, e := b.Command(ctx, cfg, args...)
	if e != nil {
		t.Fatal(e)
	}
	// Prove the exact production launcher profile before executing native CLI.
	profile := filepath.Join(cfg.Home, "network.sb")
	if len(cmd.Args) < 4 || cmd.Args[1] != "-p" {
		t.Fatal("production sandbox absent")
	}
	if e := os.WriteFile(profile, []byte(cmd.Args[2]), 0600); e != nil {
		t.Fatal(e)
	}
	sandboxGate(t, cfg.Home, profile, strings.TrimPrefix(b.URL(), "http://"))
	return cmd
}
func runNative(t *testing.T, cmd *exec.Cmd) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	e := cmd.Run()
	s := buf.String()
	for _, bad := range []string{"synthetic-access-generation-", "synthetic-host-trap", "synthetic-refresh-secret"} {
		if strings.Contains(s, bad) {
			t.Error("native output leaked synthetic secret")
		}
	}
	return s, e
}
func TestNativeTwoHomesTwoExpiryPagination(t *testing.T) {
	cfgs := []LaunchConfig{nativeConfig(t), nativeConfig(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var mu sync.Mutex
	gen, refreshes := 0, 0
	expired := false
	source := TokenSourceFunc(func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if expired {
			gen++
			refreshes++
			expired = false
		}
		return fmt.Sprintf("synthetic-access-generation-%d", gen), nil
	})
	var pages [3]int
	barriers := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var verified atomic.Int32
	_, tr := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		g := gen
		mu.Unlock()
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer synthetic-access-generation-%d", g) {
			t.Error("uncommitted or wrong token")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			verified.Add(1)
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic User"}}`)
			return
		}
		if r.URL.Path != "/open-apis/im/v1/messages" || r.Method != "GET" {
			t.Error("unexpected native route")
			http.Error(w, "fixture route", 400)
			return
		}
		page := 0
		switch r.URL.Query().Get("page_token") {
		case "p1":
			page = 1
		case "p2":
			page = 2
		}
		mu.Lock()
		pages[page]++
		if page < 2 && pages[page] == 2 {
			expired = true
			close(barriers[page])
		}
		mu.Unlock()
		if page < 2 {
			select {
			case <-barriers[page]:
			case <-r.Context().Done():
				return
			}
			fmt.Fprintf(w, `{"code":0,"data":{"items":[{"page":%d}],"has_more":true,"page_token":"p%d"}}`, page, page+1)
		} else {
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"page":2}],"has_more":false}}`)
		}
	}))
	binding := testBinding()
	cmds := make([]*exec.Cmd, 2)
	for i, cfg := range cfgs {
		b, e := start(ctx, binding, source, Limits{}, tr)
		if e != nil {
			t.Fatal(e)
		}
		defer b.Close()
		cmds[i] = sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages", "--params", `{"container_id_type":"chat","container_id":"oc_synthetic"}`, "--page-all", "--page-delay", "0", "--page-limit", "3", "--as", "user")
	}
	var wg sync.WaitGroup
	for _, cmd := range cmds {
		wg.Add(1)
		go func(cmd *exec.Cmd) {
			defer wg.Done()
			s, e := runNative(t, cmd)
			if e != nil {
				t.Errorf("native pagination failed: %v; safe output length=%d", e, len(s))
			}
		}(cmd)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 2 || pages != [3]int{2, 2, 2} || verified.Load() != 2 {
		t.Fatalf("pagination matrix refresh=%d pages=%v userinfo=%d", refreshes, pages, verified.Load())
	}
	for _, cfg := range cfgs {
		_ = filepath.Walk(cfg.Home, func(p string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			if p == filepath.Join(cfg.Home, ".lark-cli", "config.json") {
				return nil
			}
			data, _ := os.ReadFile(p)
			if strings.Contains(string(data), "synthetic-access-generation-") || strings.Contains(string(data), "synthetic-refresh-secret") {
				t.Error("native HOME contains token")
			}
			return nil
		})
	}
	t.Log("actual tag binary: two independent HOME, 6 page requests, 2 expiry waves, one synthetic refresh per wave; no PG integration claimed")
}
func TestNativeLauncherAndFailureGates(t *testing.T) {
	cfg := nativeConfig(t)
	var forwarded atomic.Int32
	b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, args := range [][]string{{"auth", "login"}, {"config", "init"}, {"api", "GET", "/open-apis/im/v1/messages", "--profile", "trap"}, {"im", "+send", "--as=bot"}} {
		if _, e := b.Command(ctx, cfg, args...); e == nil {
			t.Fatal("unsafe launcher args accepted")
		}
	}
	bad := cfg
	bad.BinarySHA256 = strings.Repeat("0", 64)
	if _, e := b.Command(ctx, bad, "api", "GET", "/open-apis/im/v1/messages"); e == nil {
		t.Fatal("bad artifact admitted")
	}
	old := b.address
	for _, a := range []string{"", "https://127.0.0.1:1", "http://localhost:1", "http://127.0.0.1:1?trap=1"} {
		b.address = a
		if _, e := b.Command(ctx, cfg, "api", "GET", "/open-apis/im/v1/messages"); e == nil {
			t.Fatal("invalid proxy admitted")
		}
	}
	b.address = old
	// Native source remains unmodified; an unavailable private sidecar has no
	// successful authenticated direct fallback under process network deny.
	cmd := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages", "--as", "user")
	_ = b.Close()
	_, e := runNative(t, cmd)
	if e == nil || forwarded.Load() != 0 {
		t.Fatal("disconnected broker did not fail closed")
	}
	data, e := os.ReadFile(cfg.Binary)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != cfg.BinarySHA256 {
		t.Fatal("artifact changed")
	}
}

func TestNativeRequestFailureMatrix(t *testing.T) {
	for _, scenario := range []string{"post", "unknownPost", "redirect301", "redirect302", "redirect307", "redirect308", "largeBody", "truncatedResponse", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := nativeConfig(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			var business atomic.Int32
			entered := make(chan struct{}, 1)
			released := make(chan struct{}, 1)
			b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/open-apis/authen/v1/user_info" {
					_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
					return
				}
				business.Add(1)
				select {
				case entered <- struct{}{}:
				default:
				}
				switch scenario {
				case "unknownPost":
					c, _, _ := w.(http.Hijacker).Hijack()
					c.Close()
				case "redirect301", "redirect302", "redirect307", "redirect308":
					code, _ := strconv.Atoi(strings.TrimPrefix(scenario, "redirect"))
					target := "http://open.feishu.cn/open-apis/im/v1/messages"
					if code == 302 || code == 308 {
						target = "https://forbidden.invalid/open-apis/im/v1/messages"
					}
					w.Header().Set("Location", target)
					w.WriteHeader(code)
				case "truncatedResponse":
					w.Header().Set("Content-Length", "9999")
					_, _ = io.WriteString(w, `{"code":0`)
				case "cancel":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					released <- struct{}{}
				default:
					_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_synthetic"}}`)
				}
			}), Limits{RequestBytes: 1024})
			args := []string{"api", "POST", "/open-apis/im/v1/messages", "--as", "user", "--params", `{"receive_id_type":"chat_id"}`, "--data", `{"receive_id":"oc_synthetic","msg_type":"text","content":"{\"text\":\"synthetic\"}"}`}
			if scenario == "largeBody" {
				p := filepath.Join(cfg.WorkingDir, "synthetic-large.json")
				_ = os.WriteFile(p, []byte(`{"text":"`+strings.Repeat("x", 2<<20)+`"}`), 0600)
				args[len(args)-1] = "@" + p
			}
			// For the large-body native stress only, prove production admission
			// refuses first, then explicitly bypass its bounded-input check
			// inside the same network-confined synthetic fixture to exercise
			// the official sender's known prebuffer and broker's 413.
			launchArgs := args
			if scenario == "largeBody" {
				if _, e := b.Command(ctx, cfg, args...); e == nil {
					t.Fatal("large input admitted by launcher")
				}
				launchArgs = append([]string(nil), args...)
				launchArgs[len(launchArgs)-1] = `{}`
			}
			cmd := sandboxCommand(t, ctx, b, cfg, launchArgs...)
			if scenario == "largeBody" {
				cmd.Args[len(cmd.Args)-1] = args[len(args)-1]
			}
			if scenario == "cancel" {
				go func() {
					select {
					case <-entered:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			output, e := runNative(t, cmd)
			if scenario == "post" {
				if e != nil || business.Load() != 1 {
					t.Fatalf("native post failed, output length=%d", len(output))
				}
			} else {
				if e == nil {
					t.Fatal("native error appeared successful")
				}
				want := int32(1)
				if scenario == "largeBody" {
					want = 0
				}
				if business.Load() != want {
					t.Fatalf("native business forwards=%d want=%d", business.Load(), want)
				}
			}
			if scenario == "cancel" {
				select {
				case <-released:
				case <-time.After(time.Second):
					t.Fatal("native cancel leaked upstream")
				}
			}
		})
	}
}

// This is official production-middleware fault injection compiled via a test-only
// Go overlay. It is distinct from the unmodified release CLI binary above.
func TestOfficialMiddlewareReadErrorConfinement(t *testing.T) {
	probe := os.Getenv("LARK_BODY_FAULT_PROBE")
	if probe == "" {
		t.Skip("explicit official middleware fault artifact required")
	}
	cfg := nativeConfig(t)
	var direct atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		if r.Header.Get("Authorization") != "Bearer sidecar-managed-uat" {
			t.Error("expected sentinel only")
		}
		_, _ = io.WriteString(w, "fixture-only")
	}))
	defer target.Close()
	b, _ := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("fault should not reach broker upstream") }), Limits{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	template := sandboxCommand(t, ctx, b, cfg, "api", "GET", "/open-apis/im/v1/messages")
	env := append(template.Env, "PLATFORM_TEST_TARGET="+strings.TrimPrefix(target.URL, "http://"))
	// Control proves the real official middleware's read-error branch falls
	// through and sends sentinel auth to a self-owned original target.
	control := exec.CommandContext(ctx, probe, "-test.run=^TestPlatformBodyFaultChild$")
	control.Env = env
	if _, e := runNative(t, control); e != nil || direct.Load() != 1 {
		t.Fatal("official middleware control not reproduced")
	}
	confined := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", template.Args[2], probe, "-test.run=^TestPlatformBodyFaultChild$")
	confined.Env = append(env, "PLATFORM_TEST_DENY=1")
	if _, e := runNative(t, confined); e != nil || direct.Load() != 1 {
		t.Fatal("production profile did not block fault fallback")
	}
	t.Log("official production middleware body-read-error: control reached owned target once with sentinel; exact production launcher network profile blocked second attempt")
}

func TestNativeCapturedSignatureReplay(t *testing.T) {
	cfg := nativeConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var forwards atomic.Int32
	b, _ := fixtureBroker(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
			return
		}
		forwards.Add(1)
		_, _ = io.WriteString(w, `{"code":0,"data":{}}`)
	}), Limits{})
	// An explicit test-owned front listener records only synthetic v1 frames and
	// delegates unchanged to the real broker; production has no recording hook.
	captured := make(chan *http.Request, 1)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, e := io.ReadAll(io.LimitReader(r.Body, 1025))
			if e != nil {
				t.Error("capture read")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			copy := r.Clone(context.Background())
			u, _ := url.Parse(b.URL() + r.URL.RequestURI())
			copy.URL = u
			copy.RequestURI = ""
			copy.Body = io.NopCloser(bytes.NewReader(body))
			copy.ContentLength = int64(len(body))
			captured <- copy
		}
		b.ServeHTTP(w, r)
	}))
	defer front.Close()
	original := b.address
	b.address = front.URL
	cmd := sandboxCommand(t, ctx, b, cfg, "api", "POST", "/open-apis/im/v1/messages", "--as", "user", "--data", `{"uuid":"synthetic-native-replay"}`)
	b.address = original
	if _, e := runNative(t, cmd); e != nil {
		t.Fatal("native capture failed")
	}
	var replay *http.Request
	select {
	case replay = <-captured:
	case <-ctx.Done():
		t.Fatal("native signature missing")
	}
	if status(t, replay) != 409 || forwards.Load() != 1 {
		t.Fatal("native write signature replay forwarded")
	}
	other, calls := fixtureBroker(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("native signature selected wrong scope") }), Limits{})
	raw := []byte(`{"uuid":"synthetic-native-replay"}`)
	replay.Body = io.NopCloser(bytes.NewReader(raw))
	u, _ := url.Parse(other.URL() + replay.URL.RequestURI())
	replay.URL = u
	if status(t, replay) != 403 || calls.Load() != 0 {
		t.Fatal("native signature crossed scope")
	}
}
