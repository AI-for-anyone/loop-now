package local

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/larksidecar"
)

type localLarkWorld struct {
	h             *harness
	manager       *larksidecar.Manager
	scope         *larksidecar.ManagedScope
	request       LarkRuntimeRequest
	startupCtx    context.Context
	baseFactory   func(context.Context, LarkRuntimeRequest) (LarkRuntime, error)
	calls, tokens atomic.Int32
	shimDir       string
	id            uuid.UUID
}

func newLocalLarkWorld(t *testing.T) *localLarkWorld {
	t.Helper()
	w := &localLarkWorld{manager: larksidecar.NewManager()}
	binary := os.Getenv("LARK_USER_VARIANT_BINARY")
	if binary == "" {
		t.Skip("explicit fixed Lark variant artifact required for actual manager admission")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "manifest.json"))
	var manifest struct {
		BinarySHA256 string
		Launcher     struct{ EntryFile, SHA256 string }
		SandboxProbe struct{ File, SHA256 string }
	}
	if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Launcher.EntryFile != "bin/lark-cli" {
		t.Fatal("fixed artifact manifest unavailable")
	}
	shim := filepath.Join(filepath.Dir(binary), manifest.Launcher.EntryFile)
	w.shimDir = filepath.Dir(shim)
	w.h = newHarness(t, func(o *Options) {
		o.WorkRoot, err = filepath.EvalSymlinks(o.WorkRoot)
		if err != nil {
			t.Fatal(err)
		}
		o.LoopctlBinDir = filepath.Join(w.shimDir, "loopctl-after-shim")
	}, "")
	network := &larkGateNoNetwork{}
	w.h.e.inst = clitemplate.NewInstaller(w.h.root, time.Second, &http.Client{Transport: network})
	w.id = w.h.agent(func(s *agentconfig.Snapshot) {
		s.TaskID, s.ConfigurationAgentID = uuid.New(), uuid.New()
		s.CLIs = []string{"lark-cli"}
		// Even a stale bot payload must not be installed/rendered/injected.
		s.CLIPayloads["lark-cli"] = clitemplate.Payload{Env: map[string]string{"LARKSUITE_CLI_APP_SECRET": "synthetic-old-bot"}, Files: []clitemplate.PayloadFile{{Path: ".lark-cli/config.json", Content: []byte(`{"synthetic":"bot"}`)}}}
		s.LarkBinding = &clicredential.LarkBinding{AgentID: s.ConfigurationAgentID, ProjectID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), Brand: "feishu", AppID: "test-app", OpenID: "test-user", RuntimeVariantVersion: clicredential.LarkUserVariant}
	})
	w.baseFactory = func(ctx context.Context, req LarkRuntimeRequest) (LarkRuntime, error) {
		w.calls.Add(1)
		w.request = req
		w.startupCtx = ctx
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("factory startup context has no deadline")
		}
		for _, p := range []string{req.Home, req.Workspace} {
			if info, err := os.Stat(p); err != nil || !info.IsDir() {
				return nil, errors.New("directories not prepared")
			}
		}
		b := req.Binding
		binding := larksidecar.Binding{ProjectID: b.ProjectID, AccountID: b.AccountID, VersionID: b.VersionID, AgentID: b.AgentID, RuntimeID: req.RuntimeID.String(), AppID: b.AppID, UserID: b.OpenID, Brand: b.Brand}
		cfg := larksidecar.LaunchConfig{Home: req.Home, WorkingDir: req.Workspace, InputRoots: []string{req.Workspace}, HostHome: w.shimDir, LauncherBinary: shim, LauncherSHA256: manifest.Launcher.SHA256,
			Binary: binary, BinarySHA256: manifest.BinarySHA256, ProbeBinary: filepath.Join(filepath.Dir(binary), manifest.SandboxProbe.File), ProbeSHA256: manifest.SandboxProbe.SHA256}
		// Actual pinned manager admission/probe and broker resources. No native
		// business command is executed and no TokenSource request is expected.
		s, err := w.manager.OpenScope(ctx, binding, larksidecar.TokenSourceFunc(func(context.Context) (string, error) {
			w.tokens.Add(1)
			return "", errors.New("no token request expected")
		}), cfg, larksidecar.Limits{})
		if err != nil {
			t.Errorf("actual manager registration: %v", err)
			return nil, err
		}
		w.scope = s
		return s, nil
	}
	w.h.e.o.LarkFactory = w.baseFactory
	t.Cleanup(func() {
		if network.attempts.Load() != 0 || w.tokens.Load() != 0 {
			t.Error("local resource fixture attempted install/provider access")
		}
	})
	return w
}

func TestLarkFactoryActualManagerMappingRetainedAndStopped(t *testing.T) {
	w := newLocalLarkWorld(t)
	dump := filepath.Join(t.TempDir(), "pi.json")
	w.h.env["FAKEPI_DUMP"] = dump
	if result, _ := w.h.exec(w.id, "first"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal("real manager registration failed", result.ErrorCode)
	}
	if !errors.Is(w.startupCtx.Err(), context.Canceled) {
		t.Fatal("factory startup context was not cancelled after registration")
	}
	d := w.h.e.dirs(w.id)
	w.h.src.mu.Lock()
	binding := *w.h.src.snaps[w.id].LarkBinding
	w.h.src.mu.Unlock()
	if w.request.RuntimeID != w.id || w.request.Binding != binding || w.request.Home != d.home || w.request.Workspace != d.workspace || binding.AgentID == w.id {
		t.Fatal("runtime/configuration identity or trusted directory mapping wrong")
	}
	got := readDump(t, dump)
	if !strings.HasPrefix(got.Env["PATH"], w.shimDir+string(os.PathListSeparator)) || got.Env[larkExecURL] != w.scope.ExecURL() || got.Env[larkExecKey] != w.scope.ExecCapability() || got.Env["LARKSUITE_CLI_APP_SECRET"] != "" {
		t.Fatal("shim PATH/independent environment mapping wrong")
	}
	if _, err := os.Stat(filepath.Join(d.home, ".lark-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatal("dynamic runtime rendered old bot credentials")
	}
	// Execute has returned. The retained scope must still be
	// alive; an unauthenticated request must fail without starting a consumer.
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Get(w.scope.ExecURL())
	if err != nil {
		t.Fatal("scope lifetime incorrectly tied to one Execute")
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	client.CloseIdleConnections()
	pid := w.h.pid(w.id)
	w.h.exec(w.id, "reuse")
	if w.calls.Load() != 1 || w.h.pid(w.id) != pid {
		t.Fatal("unchanged binding replaced retained scope/Pi")
	}
	native, err := w.manager.AcquireRuntimeGate(context.Background(), []uuid.UUID{w.id, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	lease := handoffLease(t, w.h.e, w.id)
	if err := lease.StopIdle(context.Background(), w.id, handoffHandle(t, lease, w.id)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.scope.Wait(ctx); err != nil {
		t.Fatal("StopIdle did not close/drain real scope", err)
	}
	if err := native.StopIdle(ctx); err != nil {
		t.Fatal("native gate could not seal already idle resources", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	native.Release()
}

func TestLarkFactoryFailureIdleAndShutdownCloseActualResources(t *testing.T) {
	for _, mode := range []string{"factory_error", "pi_start_error", "idle", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			w := newLocalLarkWorld(t)
			if mode == "factory_error" {
				w.h.e.o.LarkFactory = func(ctx context.Context, req LarkRuntimeRequest) (LarkRuntime, error) {
					s, err := w.baseFactory(ctx, req)
					if err != nil {
						return nil, err
					}
					return s, errors.New("TEST registration failed after allocation")
				}
			}
			if mode == "pi_start_error" {
				w.h.e.o.PiBin = filepath.Join(t.TempDir(), "absent-pi")
			}
			result, err := w.h.e.Execute(context.Background(), executor.Request{AgentID: w.id, RunID: uuid.New(), Message: "scope"}, newSink())
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(mode, "error") && result.Outcome != executor.OutcomeFailed {
				t.Fatal("failed startup succeeded")
			}
			if mode == "idle" {
				w.h.e.mu.Lock()
				p := w.h.e.procs[w.id]
				w.h.e.mu.Unlock()
				if p == nil {
					t.Fatal("no retained Pi")
				}
				w.h.e.idleExpired(p)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "shutdown" {
				if err := w.h.e.Shutdown(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if w.scope == nil || w.scope.Wait(ctx) != nil {
				t.Fatal("failed/idle/shutdown resource did not drain")
			}
		})
	}
}

type larkWaitBarrier struct {
	LarkRuntime
	entered, release chan struct{}
	once             sync.Once
}

func (b *larkWaitBarrier) Wait(ctx context.Context) error {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.LarkRuntime.Wait(ctx)
}

func TestLarkFactoryStopIdleDoesNotReleaseBeforeWait(t *testing.T) {
	w := newLocalLarkWorld(t)
	barrier := &larkWaitBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })
	w.h.e.o.LarkFactory = func(ctx context.Context, req LarkRuntimeRequest) (LarkRuntime, error) {
		r, err := w.baseFactory(ctx, req)
		if err != nil {
			return r, err
		}
		barrier.LarkRuntime = r
		return barrier, err
	}
	w.h.exec(w.id, "ready")
	lease := handoffLease(t, w.h.e, w.id)
	handle := handoffHandle(t, lease, w.id)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lease.StopIdle(ctx, w.id, handle) }()
	select {
	case <-barrier.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("resource Wait barrier missing")
	}
	cancel()
	if err := handoffResult(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := lease.Close(); !errors.Is(err, ErrRuntimeStopUnconfirmed) {
		t.Fatal("gate released before scope Wait", err)
	}
	release.Do(func() { close(barrier.release) })
	if err := lease.StopIdle(context.Background(), w.id, handle); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

// Cancellation is observed at a deterministic registration barrier, not by
// guessing when startup is ready. The real allocated manager scope must drain.
func TestLarkFactoryCancellationDuringRegistration(t *testing.T) {
	w := newLocalLarkWorld(t)
	ready := make(chan struct{})
	w.h.e.o.LarkFactory = func(ctx context.Context, req LarkRuntimeRequest) (LarkRuntime, error) {
		r, err := w.baseFactory(ctx, req)
		if err != nil {
			return r, err
		}
		close(ready)
		<-ctx.Done()
		return r, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan executor.Result, 1)
	go func() {
		result, _ := w.h.e.Execute(ctx, executor.Request{AgentID: w.id, RunID: uuid.New(), Message: "cancel during registration"}, newSink())
		done <- result
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("registration barrier missing")
	}
	cancel()
	select {
	case result := <-done:
		if result.Outcome != executor.OutcomeFailed {
			t.Fatal("cancelled registration succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled registration did not finish")
	}
	drain, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if w.scope.Wait(drain) != nil {
		t.Fatal("cancelled registration leaked actual scope")
	}
	w.h.e.mu.Lock()
	retained := len(w.h.e.procs) + len(w.h.e.live) + len(w.h.e.runs)
	w.h.e.mu.Unlock()
	if retained != 0 {
		t.Fatal("cancelled registration started/retained Pi")
	}
	if _, err := os.Stat(w.h.e.dirs(w.id).pidFile); !os.IsNotExist(err) {
		t.Fatal("cancelled registration wrote Pi PID")
	}
}
