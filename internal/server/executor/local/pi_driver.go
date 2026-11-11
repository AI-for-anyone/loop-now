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

// piDriver 是随包 Pi 的运行时：RPC 模式的常驻进程，模型凭证经 Agent 的 Pi 目录（auth.json、models.json）与授权刷新通道。
type piDriver struct{ e *Executor }

func (piDriver) name() string { return "Pi" }

func (piDriver) runtime() string { return agentruntime.Pi }

func (piDriver) identity(agentconfig.Snapshot) string { return "" }

// refresh 重写 Pi 的模型凭证与 models.json 缓存。插件提供方的订阅账号：auth.json 是指向账号状态目录的链接
// （prepare 建的，进程身份含 stateIdentity，复用时必然还是同一份），不能当普通凭证重写，否则会把链接换成文件或直接失败。
func (piDriver) refresh(snap agentconfig.Snapshot, d agentDirs) error {
	if snap.ProviderState == nil {
		if err := seedModelAuth(snap, d); err != nil {
			return fail(executor.ErrCredentialUnavailable, "模型凭证缓存重建失败")
		}
	}
	if err := syncModelsJSON(snap, d); err != nil {
		return fail(executor.ErrCredentialUnavailable, "模型配置缓存重建失败")
	}
	return nil
}

// start 组装 Pi 的参数（内置扩展、授权刷新通道、插件）并以 RPC 模式启动。
func (pd piDriver) start(ctx context.Context, spec startSpec) (started, error) {
	e, snap, d := pd.e, spec.snap, spec.d
	env := spec.env
	args := piArgs(d, snap, spec.prep.skillDirs, spec.sessionFile)
	if snap.ServiceTier != nil {
		resolved, tierErr := agentconfig.ResolveServiceTier(snap.Provider, snap.ServiceTier)
		if tierErr != nil || resolved == nil || !authhelper.ValidateServiceTierEntry(e.o.CodexServiceTierExtension) {
			return started{}, fail(executor.ErrPiStartFailed, "Codex速度配置或内置扩展不可用")
		}
		args = append(args, "-e", e.o.CodexServiceTierExtension)
		env = append(env, "LOOP_NOW_CODEX_SERVICE_TIER="+*resolved)
	}
	var bridge *modelauth.RuntimeBridge
	if snap.ModelCredential.Kind() == "oauth" {
		if snap.ModelBinding == nil || !modelauth.ValidateEntry(e.o.AuthExtension) {
			return started{}, fail(executor.ErrCredentialUnavailable, "任务授权刷新通道未配置")
		}
		var err error
		bridge, err = modelauth.NewRuntimeBridge(*snap.ModelBinding, e.o.AuthResolver, e.o.AuthIPCTimeout)
		if err != nil {
			return started{}, fail(executor.ErrCredentialUnavailable, "任务授权刷新通道不可用")
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
			return started{}, fail(executor.ErrCredentialUnavailable, "旧认证适配未配置")
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
		return started{}, fail(executor.ErrPiStartFailed, "启动 Pi 失败：%v", err)
	}
	if err := procgroup.Started(cmd); err != nil {
		e.snapshotLog(snap).Warn("Pi 进程没有纳入整组管理，停止时只能结束它本身", "pid", client.Pid(), "err", err)
	}
	if bridge != nil {
		bridge.ChildStarted()
	}
	return started{rt: client, client: client, authBridge: bridge}, nil
}

// attach 接上用量与事件的观察者，以及扩展对话。
func (pd piDriver) attach(p *proc) {
	e := pd.e
	p.client.SetObserver(procObserver{e: e, p: p})
	if e.o.Dialogs != nil {
		p.client.SetDialogs(func(ctx context.Context, req pirpc.DialogRequest) pirpc.DialogResponse { return e.dialog(ctx, p, req) })
	}
}

// ready 读出 Pi 的状态（会话文件），并在后台缓存它的命令列表。
func (pd piDriver) ready(ctx context.Context, p *proc, _ startSpec) error {
	e := pd.e
	stateCtx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	st, err := p.client.GetState(stateCtx)
	cancel()
	if err != nil {
		return fail(executor.ErrPiStartFailed, "Pi 启动后未就绪：%v %s", err, tail(p.client.Stderr(), e.o.StderrTailBytes))
	}
	p.sessionFile = st.SessionFile
	go e.cacheCommands(p)
	return nil
}

// beforeTurn 应用本轮的思考等级。
func (pd piDriver) beforeTurn(ctx context.Context, p *proc, snap agentconfig.Snapshot) error {
	if snap.ThinkingLevel == nil {
		return nil
	}
	thinkingCtx, cancel := context.WithTimeout(ctx, pd.e.o.StartTimeout)
	err := p.client.SetThinkingLevel(thinkingCtx, *snap.ThinkingLevel)
	cancel()
	if err != nil {
		return fail(executor.ErrPiError, "无法应用本轮思考等级")
	}
	return nil
}

func (piDriver) afterTurn(_ *proc, tr runturn.Result) runturn.Result { return tr }

func (piDriver) exited() (string, string) { return executor.ErrPiExited, "Pi 进程已退出" }

func (piDriver) crashMessage(_ *proc, stderrTail string) string {
	return "Pi 进程异常退出 " + stderrTail
}
