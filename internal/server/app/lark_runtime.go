package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/config"
	"loop-now/internal/server/executor/local"
	"loop-now/internal/server/larkauth"
	"loop-now/internal/server/larksidecar"
	"loop-now/internal/server/tasks"
)

// A single composition owns both process registration and the task handoff
// gate. Its assets are captured from a verified installation, not from Pi,
// request headers, a writable scope descriptor, or a project credential.
type larkExecution struct {
	manager *larksidecar.Manager
	grants  *larkauth.Coordinator
	assets  larksidecar.LaunchConfig
	limits  larksidecar.Limits
}

func configureLarkExecution(ctx context.Context, cfg *config.Config, workRoot string, grants *larkauth.Coordinator, transport http.RoundTripper) (*larkExecution, error) {
	bundle := cfg.LarkUserBundleDir
	if bundle == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		bundle = filepath.Join(filepath.Dir(exe), "lark-user-variant", clicredential.LarkUserVariant, runtime.GOOS+"-"+runtime.GOARCH)
		if _, err = os.Stat(bundle); os.IsNotExist(err) {
			return nil, nil
		} else if err != nil {
			return nil, err
		}
	}
	assets, err := prepareLarkAssets(ctx, bundle, workRoot, larkManifestSHA)
	if err != nil {
		return nil, err
	}
	l := &larkExecution{manager: larksidecar.NewManagerWithOptions(larksidecar.ManagerOptions{HTTPTransport: transport}), grants: grants, assets: assets}
	// Check the real OS profile/listeners before exposing a manager that could
	// accept a task switch. This opens no provider request and owns no grant.
	tmp, err := os.MkdirTemp(filepath.Join(workRoot, "tools"), ".lark-admission-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for _, part := range []string{"home", "work"} {
		if err = os.Mkdir(filepath.Join(tmp, part), 0700); err != nil {
			return nil, err
		}
	}
	probeCfg := assets
	probeCfg.Home = filepath.Join(tmp, "home")
	probeCfg.WorkingDir = filepath.Join(tmp, "work")
	probeCfg.InputRoots = []string{probeCfg.WorkingDir}
	probe := larksidecar.Binding{ProjectID: uuid.New(), AccountID: uuid.New(), VersionID: uuid.New(), AgentID: uuid.New(), RuntimeID: uuid.NewString(), Brand: "feishu", AppID: "platform-readiness", UserID: "platform-readiness"}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scope, err := l.manager.OpenScope(probeCtx, probe, larksidecar.TokenSourceFunc(func(context.Context) (string, error) {
		return "", errors.New("readiness must not resolve a provider credential")
	}), probeCfg, l.limits)
	if err != nil {
		return nil, err
	}
	closeErr := scope.Close()
	waitErr := scope.Wait(probeCtx)
	if closeErr != nil || waitErr != nil {
		return nil, errors.Join(closeErr, waitErr)
	}
	return l, nil
}

func (l *larkExecution) open(ctx context.Context, request local.LarkRuntimeRequest) (local.LarkRuntime, error) {
	if l == nil || l.manager == nil || l.grants == nil || ctx == nil || ctx.Err() != nil || request.RuntimeID == uuid.Nil || !request.Binding.Valid() ||
		!filepath.IsAbs(request.Home) || !filepath.IsAbs(request.Workspace) || request.Home != filepath.Clean(request.Home) || request.Workspace != filepath.Clean(request.Workspace) {
		return nil, errors.New("Lark运行作用域不可用")
	}
	source, err := l.grants.TokenSource(request.Binding)
	if err != nil {
		return nil, err
	}
	binding := larksidecar.Binding{ProjectID: request.Binding.ProjectID, AccountID: request.Binding.AccountID, VersionID: request.Binding.VersionID,
		AgentID: request.Binding.AgentID, RuntimeID: request.RuntimeID.String(), Brand: request.Binding.Brand, AppID: request.Binding.AppID, UserID: request.Binding.OpenID}
	cfg := l.assets
	cfg.Home, cfg.WorkingDir = request.Home, request.Workspace
	cfg.InputRoots = []string{request.Workspace}
	scope, err := l.manager.OpenScope(ctx, binding, source, cfg, l.limits)
	if err != nil {
		return nil, err
	}
	return &larkRuntime{scope: scope}, nil
}

// Acquire implements the existing task service interface without a second
// registry. Only the exact requested runtimes are fenced; no project-wide gate.
func (l *larkExecution) Acquire(ctx context.Context, ids []uuid.UUID) (tasks.TaskLarkNativeLease, error) {
	if l == nil || l.manager == nil {
		return nil, errors.New("Lark运行交接未装配")
	}
	gate, err := l.manager.AcquireRuntimeGate(ctx, slices.Clone(ids))
	if err != nil {
		if errors.Is(err, larksidecar.ErrNativeBusy) {
			return nil, tasks.ErrTaskLarkNativeBusy
		}
		return nil, err
	}
	return &larkRuntimeGate{gate: gate}, nil
}

// RecoverPendingHandoff is the startup-only bridge for the original pre-C action.
// The task owner supplies the trusted transaction callback; it must consume
// only the unchanged pending action and its exact two persistent fences.
// Even an error may return a held gate, which must not be discarded/released.
func (l *larkExecution) RecoverPendingHandoff(ctx context.Context, action uuid.UUID, ids [2]uuid.UUID, raw json.RawMessage, consumePreCommit func(context.Context) error) (tasks.TaskLarkNativeLease, error) {
	if l == nil || l.manager == nil {
		return nil, errors.New("Lark运行恢复未装配")
	}
	var record larksidecar.NativeRecoveryRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		// A zero record is deliberately passed into the real manager: it
		// reserves the exact runtime gates but cannot produce a death receipt.
		record = larksidecar.NativeRecoveryRecord{}
		consumePreCommit = nil
	}
	gate, _, err := l.manager.RecoverPendingHandoff(ctx, larksidecar.PendingNativeHandoff{ActionID: action, RuntimeIDs: ids}, record, consumePreCommit)
	if gate == nil {
		return nil, err
	}
	return &larkRuntimeGate{gate: gate}, err
}

type larkRuntime struct{ scope *larksidecar.ManagedScope }

func (r *larkRuntime) BinDir() string { return r.scope.BinDir() }
func (r *larkRuntime) Environment() map[string]string {
	return map[string]string{larksidecar.ExecURLEnv: r.scope.ExecURL(), larksidecar.ExecKeyEnv: r.scope.ExecCapability()}
}
func (r *larkRuntime) Close() error                   { return r.scope.Close() }
func (r *larkRuntime) Wait(ctx context.Context) error { return r.scope.Wait(ctx) }

type larkRuntimeGate struct{ gate *larksidecar.RuntimeGate }

func (g *larkRuntimeGate) Seal(ctx context.Context) error { return g.gate.StopIdle(ctx) }
func (g *larkRuntimeGate) Release()                       { g.gate.Release() }

// CaptureRecovery exposes only the immutable, non-secret original gate proof.
// The task owner persists it alongside the separate local process record.
func (g *larkRuntimeGate) CaptureRecovery(ctx context.Context, action uuid.UUID) (json.RawMessage, error) {
	record, err := g.gate.CaptureRecovery(ctx, action)
	if err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

var _ tasks.TaskLarkNativeManager = (*larkExecution)(nil)
var _ local.LarkRuntime = (*larkRuntime)(nil)

var _ tasks.TaskLarkNativeRecoveryManager = (*larkExecution)(nil)
var _ tasks.TaskLarkNativeRecoveryLease = (*larkRuntimeGate)(nil)
