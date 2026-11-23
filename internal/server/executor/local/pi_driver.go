package local

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	authhelper "loop-now/auth-helper"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/preflight"
)

// piDriver is the bundled Pi runtime: a long-running process in RPC mode, with model credentials provided through the
// Agent's Pi directory (auth.json, models.json) and the auth refresh channel.
type piDriver struct{ e *Executor }

func (piDriver) name() string { return "Pi" }

func (piDriver) runtime() string { return agentruntime.Pi }

func (piDriver) identity(agentconfig.Snapshot) string { return "" }

// refresh rewrites Pi's model credentials and the models.json cache. For a plugin provider's subscription account,
// auth.json is a link to the account's state directory (created by prepare; the process identity includes
// stateIdentity, so on reuse it is necessarily still the same one) and must not be rewritten like an ordinary
// credential, or the link would be replaced by a file or the write would simply fail.
func (piDriver) refresh(snap agentconfig.Snapshot, d agentDirs) error {
	if snap.ProviderState == nil {
		if err := seedModelAuth(snap, d); err != nil {
			return fail(executor.ErrCredentialUnavailable, "Could not rebuild the model credential cache.")
		}
	}
	if err := syncModelsJSON(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "Could not rebuild the model configuration cache.")
	}
	return nil
}

// start builds Pi's arguments (built-in extensions, auth refresh channel, plugins) and starts it in RPC mode.
func (pd piDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := pd.e, spec.snap, spec.d
	env := spec.env
	args := piArgs(d, snap, spec.prep.skillDirs, spec.sessionFile)
	if snap.ServiceTier != nil {
		resolved, tierErr := agentconfig.ResolveServiceTier(snap.Provider, snap.ServiceTier)
		if tierErr != nil || resolved == nil || !authhelper.ValidateServiceTierEntry(e.o.CodexServiceTierExtension) {
			return started{}, fail(executor.ErrPiStartFailed, "The Codex speed setting or its built-in extension is not available.")
		}
		args = append(args, "-e", e.o.CodexServiceTierExtension)
		env = append(env, "LOOP_NOW_CODEX_SERVICE_TIER="+*resolved)
	}
	var bridge *modelauth.RuntimeBridge
	if snap.ModelCredential.Kind() == "oauth" {
		if snap.ModelBinding == nil || !modelauth.ValidateEntry(e.o.AuthExtension) {
			return started{}, fail(executor.ErrCredentialUnavailable, "The task authorization refresh channel is not configured.")
		}
		var err error
		bridge, err = modelauth.NewRuntimeBridge(*snap.ModelBinding, e.o.AuthResolver, e.o.AuthIPCTimeout)
		if err != nil {
			return started{}, fail(executor.ErrCredentialUnavailable, "The task authorization refresh channel is not available.")
		}
		args = append(args, "-e", e.o.AuthExtension)
		ipcTimeout := e.o.AuthIPCTimeout
		if ipcTimeout <= 0 {
			ipcTimeout = 30 * time.Second
		}
		env = append(slices.Clone(env), "LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS="+strconv.FormatInt(max(int64(1), ipcTimeout.Milliseconds()), 10))
	}
	closeBridge := func() {
		if bridge != nil {
			bridge.Close()
		}
	}
	if snap.ModelCredential.NeedsLegacyAnthropic() {
		if snap.Provider != "anthropic" || !modelauth.ValidateEntry(e.o.LegacyAuthExtension) || !filepath.IsAbs(e.o.PiPackageRoot) || !filepath.IsAbs(e.o.PiAIDir) {
			closeBridge()
			return started{}, fail(executor.ErrCredentialUnavailable, "The legacy authentication adapter is not configured.")
		}
		args = append(args, "-e", e.o.LegacyAuthExtension)
		env = append(slices.Clone(env), "LOOP_NOW_PI_PACKAGE_ROOT="+e.o.PiPackageRoot, "LOOP_NOW_PI_AI_DIR="+e.o.PiAIDir)
	}
	args = append(args, pluginArgs(snap.Plugins)...)
	cmd := preflight.PiCommand(e.o.NodeBin, e.o.PiBin, args...)
	if bridge != nil {
		cmd.ExtraFiles = bridge.ChildFiles()
	}
	cmd.Env = env
	cmd.Dir = d.workspace
	procgroup.Configure(cmd)
	if err := ctx.Err(); err != nil {
		closeBridge()
		return started{}, err
	}
	client, err := pirpc.Start(cmd)
	if err != nil {
		closeBridge()
		return started{}, fail(executor.ErrPiStartFailed, "Could not start Pi: %v", err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Pi process is not under group management; stopping can only end the process itself", "pid", client.Pid(), "err", err)
	}
	if bridge != nil {
		bridge.ChildStarted()
	}
	return started{rt: client, client: client, authBridge: bridge}, nil
}

// attach hooks up the usage and event observers, and extension dialogs.
func (pd piDriver) attach(p *proc) {
	e := pd.e
	p.client.SetObserver(procObserver{e: e, p: p})
	if e.o.Dialogs != nil {
		p.client.SetDialogs(func(ctx context.Context, req runturn.DialogRequest) runturn.DialogResponse {
			return e.dialog(ctx, p, req)
		})
	}
}

// ready reads Pi's state (the session file) and caches its command list in the background.
func (pd piDriver) ready(ctx context.Context, p *proc, _ startSpec) error {
	e := pd.e
	stateCtx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	st, err := p.client.GetState(stateCtx)
	cancel()
	if err != nil {
		return fail(executor.ErrPiStartFailed, "Pi started but did not become ready: %v %s", err, tail(p.client.Stderr(), e.o.StderrTailBytes))
	}
	p.sessionFile = st.SessionFile
	go e.cacheCommands(p)
	return nil
}

// beforeTurn applies this turn's thinking level.
func (pd piDriver) beforeTurn(ctx context.Context, p *proc, snap agentconfig.Snapshot) error {
	if snap.ThinkingLevel == nil {
		return nil
	}
	thinkingCtx, cancel := context.WithTimeout(ctx, pd.e.o.StartTimeout)
	err := p.client.SetThinkingLevel(thinkingCtx, *snap.ThinkingLevel)
	cancel()
	if err != nil {
		return fail(executor.ErrPiError, "Could not apply the thinking level for this turn.")
	}
	return nil
}

func (piDriver) afterTurn(_ *proc, tr runturn.Result) runturn.Result { return tr }

func (piDriver) exited() (string, string) { return executor.ErrPiExited, "The Pi process exited." }

func (piDriver) crashMessage(_ *proc, stderrTail string) string {
	return "The Pi process exited unexpectedly. " + stderrTail
}
