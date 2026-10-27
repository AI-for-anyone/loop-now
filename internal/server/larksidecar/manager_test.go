package larksidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

func managedConfig(t *testing.T) LaunchConfig {
	t.Helper()
	cfg := nativeConfig(t)
	raw, e := os.ReadFile(filepath.Join(filepath.Dir(cfg.Binary), "manifest.json"))
	var m struct {
		Launcher struct{ EntryFile, SHA256, Protocol string }
	}
	if e != nil || json.Unmarshal(raw, &m) != nil || m.Launcher.Protocol != "loop-now-lark-exec-v1" || m.Launcher.EntryFile != "bin/lark-cli" {
		t.Fatal("managed launcher manifest unavailable")
	}
	cfg.LauncherBinary = filepath.Join(filepath.Dir(cfg.Binary), m.Launcher.EntryFile)
	cfg.LauncherSHA256 = m.Launcher.SHA256
	return cfg
}

func closeManaged(t *testing.T, s *ManagedScope) {
	t.Helper()
	_ = s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := s.Wait(ctx); e != nil {
		t.Error("owned managed scope failed to drain")
	}
}

func TestManagedNativePATHAndGroupFence(t *testing.T) {
	cfg := managedConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	firstPage := make(chan struct{}, 1)
	var pages atomic.Int32
	_, tr := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-managed-token" {
			t.Error("wrong fixed identity")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
			return
		}
		if r.URL.Path != "/open-apis/im/v1/messages" {
			t.Error("unexpected fixture request")
			w.WriteHeader(400)
			return
		}
		pages.Add(1)
		if r.URL.Query().Get("page_token") == "" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"page":1}],"has_more":true,"page_token":"next"}}`)
			firstPage <- struct{}{}
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"page":2}],"has_more":false}}`)
	}))
	m := NewManagerWithOptions(ManagerOptions{HTTPTransport: tr})
	binding := testBinding()
	id := uuid.MustParse(binding.RuntimeID)
	absent := uuid.New()
	s, e := m.OpenScope(ctx, binding, TokenSourceFunc(func(context.Context) (string, error) { return "synthetic-managed-token", nil }), cfg, Limits{})
	if e != nil {
		t.Fatalf("real managed admission failed: %v", e)
	}
	defer closeManaged(t, s)
	if len(s.Environment()) != 2 || s.Environment()[ExecKeyEnv] == s.broker.Capability() {
		t.Fatal("exec and provider capabilities not separated")
	}
	args := []string{"api", "GET", "/open-apis/im/v1/messages", "--params", `{"container_id_type":"chat","container_id":"oc_synthetic"}`, "--page-all", "--page-delay", "1200", "--page-limit", "2", "--as", "user"}
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c", `exec lark-cli "$@"`, "managed-path"}, args...)...)
	cmd.Dir = cfg.WorkingDir
	cmd.Env = []string{"HOME=" + cfg.Home, "PATH=" + s.BinDir() + ":/usr/bin:/bin"}
	for k, v := range s.Environment() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if cmd.Start() != nil {
		t.Fatal("owned PATH shim start")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-firstPage:
	case <-ctx.Done():
		t.Fatal("first page not reached")
	}
	// Wait for the business handler to finish while the actual CLI is sleeping
	// between pages. The exec RPC itself is not a business token request.
	deadline := time.Now().Add(time.Second)
	for {
		s.broker.mu.Lock()
		activeHTTP := s.broker.active
		s.broker.mu.Unlock()
		if activeHTTP == 1 && pages.Load() == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("could not observe inter-page gap")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if g, e := m.AcquireRuntimeGate(ctx, []uuid.UUID{id, absent}); !errors.Is(e, ErrNativeBusy) || g != nil {
		t.Fatal("inter-page native consumer incorrectly froze")
	}
	m.mu.Lock()
	actualConsumers := m.active[binding.RuntimeID]
	m.mu.Unlock()
	if actualConsumers != 1 {
		t.Fatal("inter-page native consumer not registered independently")
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("PATH/native failed, safe output bytes=%d", out.Len())
		}
	case <-ctx.Done():
		t.Fatal("owned native did not finish")
	}
	if pages.Load() != 2 || strings.Contains(out.String(), "synthetic-managed-token") || strings.Contains(out.String(), s.execKey) {
		t.Fatal("pagination or output secrecy failed")
	}
	var gate *RuntimeGate
	deadline = time.Now().Add(time.Second)
	for {
		gate, e = m.AcquireRuntimeGate(ctx, []uuid.UUID{id, absent})
		if e == nil {
			break
		}
		if !errors.Is(e, ErrNativeBusy) || time.Now().After(deadline) {
			t.Fatal("completed consumer did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
	defer gate.Release()
	other := binding
	other.RuntimeID = absent.String()
	if _, e = m.OpenScope(ctx, other, TokenSourceFunc(func(context.Context) (string, error) { t.Error("frozen scope resolved source"); return "", nil }), cfg, Limits{}); !errors.Is(e, ErrNativeBusy) {
		t.Fatal("gate allowed creation for absent member")
	}
	if e = gate.Seal(ctx); e != nil {
		t.Fatal("scope did not seal before C")
	}
	// Both actual listeners must be gone, not just the IPv4 CLI endpoint.
	for _, address := range []string{strings.TrimPrefix(s.broker.URL(), "http://"), s.broker.address6} {
		c, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			c.Close()
			t.Fatal("sealed listener remains reachable")
		}
	}
	if _, e = m.OpenScope(ctx, other, TokenSourceFunc(func(context.Context) (string, error) { return "", nil }), cfg, Limits{}); !errors.Is(e, ErrNativeBusy) {
		t.Fatal("Seal released creation gate before C")
	}
	gate.Release()
	gate.Release()
	t.Log("actual shell PATH -> fixed shim -> private argv RPC -> Command/sandbox -> fixed native 2 pages; inter-page native active blocks whole group; absent scope creation fenced; Seal drains both listeners before pure-memory Release")
}

func TestManagedCreationReservationAndCancellation(t *testing.T) {
	cfg := managedConfig(t)
	probe := filepath.Join(cfg.Home, "owned-blocking-probe")
	script := []byte("#!/bin/sh\n/bin/sleep 30 &\nchild=$!\nprintf '%s %s\\n' \"$$\" \"$child\" > \"$HOME/creation-pids\"\nwait\n")
	if os.WriteFile(probe, script, 0700) != nil {
		t.Fatal("owned probe fixture")
	}
	cfg.ProbeBinary = probe
	cfg.ProbeSHA256 = launcherDigest(t, probe)
	m := NewManager()
	binding := testBinding()
	id := uuid.MustParse(binding.RuntimeID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	done := make(chan error, 1)
	go func() {
		s, e := m.OpenScope(ctx, binding, TokenSourceFunc(func(context.Context) (string, error) { calls.Add(1); return "", errors.New("synthetic") }), cfg, Limits{})
		if s != nil {
			_ = s.Close()
		}
		done <- e
	}()
	var parent, child int
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, e := os.ReadFile(filepath.Join(cfg.Home, "creation-pids"))
		if e == nil {
			_, _ = fmt.Sscanf(string(raw), "%d %d", &parent, &child)
			if parent > 1 && child > 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("owned preparation did not reach barrier")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if g, e := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{id, uuid.New()}); !errors.Is(e, ErrNativeBusy) || g != nil {
		t.Fatal("scope creation was invisible to group freeze")
	}
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("cancelled preparation published scope")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled preparation failed to finish")
	}
	for _, pid := range []int{parent, child} {
		until := time.Now().Add(time.Second)
		for syscall.Kill(pid, 0) != syscall.ESRCH {
			if time.Now().After(until) {
				t.Fatal("owned preparation process remains")
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("preparation contacted provider")
	}
	g, e := m.AcquireRuntimeGate(context.Background(), []uuid.UUID{id, uuid.New()})
	if e != nil {
		t.Fatal("failed preparation leaked reservation")
	}
	g.Release()
}

func TestManagedPublishedScopeSurvivesRegistrationCancellation(t *testing.T) {
	cfg := managedConfig(t)
	registration, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := NewManager()
	binding := testBinding()
	var calls atomic.Int32
	s, e := m.OpenScope(registration, binding, TokenSourceFunc(func(ctx context.Context) (string, error) {
		if ctx.Err() != nil {
			t.Error("published scope inherited registration cancellation")
		}
		calls.Add(1)
		return "", errors.New("synthetic unavailable")
	}), cfg, Limits{})
	if e != nil {
		t.Fatal("scope registration unavailable")
	}
	defer closeManaged(t, s)
	cancel() // The local factory cancels successful bounded registration here.
	if status(t, signed(s.broker, "GET", "/open-apis/authen/v1/user_info", nil)) != 503 || calls.Load() != 1 {
		t.Fatal("published scope died with completed registration")
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	var g *RuntimeGate
	until := time.Now().Add(time.Second)
	for {
		g, e = m.AcquireRuntimeGate(ctx, []uuid.UUID{uuid.MustParse(binding.RuntimeID), uuid.New()})
		if e == nil {
			break
		}
		if !errors.Is(e, ErrNativeBusy) || time.Now().After(until) {
			t.Fatal("published scope did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
	if g.Seal(ctx) != nil {
		t.Fatal("published scope did not close under owner lifecycle")
	}
	g.Release()
}

func TestManagedNativeClientCancellationDrains(t *testing.T) {
	cfg := managedConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	entered, released := make(chan struct{}, 1), make(chan struct{}, 1)
	_, tr := tlsFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/authen/v1/user_info" {
			_, _ = io.WriteString(w, `{"code":0,"data":{"open_id":"ou_synthetic","name":"Synthetic"}}`)
			return
		}
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"code":0,"data":`)
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		<-r.Context().Done()
		released <- struct{}{}
	}))
	m := NewManager()
	binding := testBinding()
	s, e := m.openScope(ctx, binding, TokenSourceFunc(func(context.Context) (string, error) { return "synthetic-managed-token", nil }), cfg, Limits{}, tr)
	if e != nil {
		t.Fatal("real managed admission unavailable")
	}
	defer closeManaged(t, s)
	cliCtx, stopCLI := context.WithCancel(ctx)
	defer stopCLI()
	cmd := exec.CommandContext(cliCtx, filepath.Join(s.BinDir(), "lark-cli"), "api", "GET", "/open-apis/im/v1/messages", "--as", "user")
	cmd.Env = []string{"HOME=" + cfg.Home, "PATH=/usr/bin:/bin"}
	for k, v := range s.Environment() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Start() != nil {
		t.Fatal("owned shim start")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owned native did not enter fixture")
	}
	stopCLI()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("owned shim failed to exit")
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("shim disconnect failed to cancel real native/upstream")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		g, e := m.AcquireRuntimeGate(ctx, []uuid.UUID{uuid.MustParse(binding.RuntimeID), uuid.New()})
		if e == nil {
			if g.Seal(ctx) != nil {
				t.Fatal("cancelled native not drained")
			}
			g.Release()
			break
		}
		if !errors.Is(e, ErrNativeBusy) || time.Now().After(deadline) {
			t.Fatal("native completion remained unknown")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestManagedRPCRejectsWrongCapabilityAndConfiguration(t *testing.T) {
	cfg := managedConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := NewManager()
	var calls atomic.Int32
	s, e := m.OpenScope(ctx, testBinding(), TokenSourceFunc(func(context.Context) (string, error) { calls.Add(1); return "", errors.New("synthetic") }), cfg, Limits{})
	if e != nil {
		t.Fatal("managed admission unavailable")
	}
	defer closeManaged(t, s)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, test := range []struct{ key, body string }{{s.broker.Capability(), `{"args":["api"]}`}, {s.execKey, `{"args":["api"],"binary":"/untrusted"}`}, {s.execKey, `{"args":["api"],"env":{}}`}, {s.execKey, `{"args":["auth","login"]}`}} {
		r, _ := http.NewRequest("POST", s.ExecURL(), strings.NewReader(test.body))
		r.Header.Set("Authorization", "Bearer "+test.key)
		res, e := client.Do(r)
		if e != nil {
			t.Fatal("isolated RPC")
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("untrusted exec request accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected RPC reached provider")
	}
}
