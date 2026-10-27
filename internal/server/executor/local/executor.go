package local

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	authhelper "loop-now/auth-helper"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/toolcommand"
)

// Source 是执行器读取配置与回写状态的数据源，由 agentconfig.Service 实现。
type Source interface {
	ExecutionSnapshot(ctx context.Context, agentID uuid.UUID) (agentconfig.Snapshot, error)
	ReadBlob(ctx context.Context, sha string) ([]byte, error)
	SourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]store.SyncFile, error)
	MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []store.SyncFile) error
	RecordSessionFile(ctx context.Context, agentID uuid.UUID, path string) error
	MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error
	DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Options 是本机执行器的参数，全部来自配置与启动检查。
type Options struct {
	Models *modelcatalog.Catalog
	// Hooks 在每一轮开始前调用插件的 run.before_start；为空时不调用。
	Hooks pluginhost.Hooker
	// Dialogs 回答扩展在一轮之中发来的 confirm、select、input（装配为控制台提问）；为空时一律按取消回应。
	// agentID 是执行的 Agent（任务轮次为任务实例），ctx 在这一轮不再是进程的当前执行或进程退出时结束。
	Dialogs                   func(ctx context.Context, agentID, runID uuid.UUID, req pirpc.DialogRequest) pirpc.DialogResponse
	LarkFactory               func(context.Context, LarkRuntimeRequest) (LarkRuntime, error)
	GoogleResolver            cliauth.GrantResolver
	GoogleRuntimeEntry        string
	AuthResolver              modelauth.RuntimeResolver
	AuthExtension             string
	CodexServiceTierExtension string
	LegacyAuthExtension       string
	PiPackageRoot             string
	AuthIPCTimeout            time.Duration
	ModelSecretEnvNames       []string
	WorkRoot                  string
	PiBin                     string
	GitBin                    string
	ExecBasePath              string
	Passthrough               []string
	Getenv                    func(string) string
	Username                  string
	MaxProcesses              int
	IdleTimeout               time.Duration
	StartTimeout              time.Duration
	StopGrace                 time.Duration
	AbortTimeout              time.Duration
	CloneTimeout              time.Duration
	InstanceID                uuid.UUID
	Hostname                  string
	// StderrTailBytes 是写入错误信息与运行记录的 Pi 错误输出末尾长度；为 0 时取 1000。
	StderrTailBytes int
	// Observer 接收运行记录；为空时丢弃。
	Observer executor.Observer
	// Tokens 为每次启动的 Pi 进程签发 Agent 令牌；为空时不注入令牌（测试用）。
	Tokens TokenIssuer
	// AgentURL 是注入给 Pi 的 LOOPCTL_AGENT_URL：回环监听器上 Agent 专用接口的完整基址。
	AgentURL string
	// LoopctlBinDir 是已安装的同版本 loopctl 所在目录，放在 Agent 执行 PATH 的最前面；为空时不加入（测试用）。
	LoopctlBinDir string
	// ContextDir 是项目与全局上下文文件夹的根；为空时不注入上下文入口与路径说明（测试用）。
	ContextDir string
}

// TokenIssuer 签发与作废 Agent 令牌。令牌在启动 Pi 前签发，进程停止、退出或被丢弃时作废。
type TokenIssuer interface {
	IssueAgentToken(ctx context.Context, agentID uuid.UUID) (id uuid.UUID, token string, err error)
	RevokeAgentToken(ctx context.Context, id uuid.UUID) error
}

// Executor 是本机执行器。
type Executor struct {
	o    Options
	src  Source
	tmpl *clitemplate.Catalog
	inst *clitemplate.Installer
	log  *slog.Logger
	obs  executor.Observer

	mu       sync.Mutex
	procs    map[uuid.UUID]*proc
	starting int
	runs     map[uuid.UUID]*run
	waiters  []chan struct{}
	closed   bool
	handoffs map[uuid.UUID]*RuntimeHandoff
	live     map[*proc]struct{} // includes detached processes until stop/bridge cleanup is confirmed
	pending  map[uuid.UUID]int
	closedCh chan struct{}
}

type proc struct {
	recoveryIdentity   *RuntimeProcessIdentity // immutable kernel identity captured at launch
	larkRuntime        *larkResource
	larkBinding        *clicredential.LarkBinding
	googleBridge       *cliauth.Broker
	authBridge         *modelauth.RuntimeBridge
	log                *slog.Logger
	agentID            uuid.UUID
	client             *pirpc.Client
	version            int64
	promptIdentity     string
	credentialIdentity string
	provider           string // 启动时的提供方与模型，用于压缩用量
	model              string
	startedAt          time.Time
	sessionFile        string // Pi 报告的会话文件路径
	sessMu             sync.Mutex
	recorded           string // 已写入数据库的会话文件路径（受 sessMu 保护）
	busy               bool
	lastUsed           time.Time
	idleTimer          *time.Timer
	dirs               agentDirs
	stopOnce           sync.Once
	exitOnce           sync.Once
	stopping           atomic.Bool               // 平台主动停止，退出不算意外
	curRun             atomic.Pointer[uuid.UUID] // 正在这个进程上回复的执行
	exitRun            atomic.Pointer[uuid.UUID] // 进程在哪次执行中退出（在 curRun 清空前设置）
	tokenID            uuid.UUID                 // 为这个进程签发的 Agent 令牌；未签发时为零值
	revokeOnce         sync.Once
	revokeErr          error
	stopDone           chan struct{}
	stopErr            error
}

type run struct {
	id          uuid.UUID
	cancel      context.CancelFunc
	done        chan struct{}
	interrupted atomic.Bool
	mu          sync.Mutex
	proc        *proc
	prompting   bool
}

// stopCause 说明为什么停止进程；reason 为空时不记录停止事件（例如启动失败或进程已退出）。
type stopCause struct {
	reason   string
	runID    *uuid.UUID
	forAgent *uuid.UUID // 为哪个 Agent 腾出名额（reason 为 evicted 时）
}

// 停止原因（pi.stopped 的 reason）。
const (
	stopIdleTimeout   = "idle_timeout"
	stopConfigChanged = "config_changed"
	stopEvicted       = "evicted"
	stopReleased      = "released"
	stopShutdown      = "shutdown"
	stopAbortTimeout  = "abort_timeout"
)

var _ executor.Executor = (*Executor)(nil)

// New 创建本机执行器。
func New(o Options, src Source, tmpl *clitemplate.Catalog, inst *clitemplate.Installer, log *slog.Logger) *Executor {
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.StderrTailBytes <= 0 {
		o.StderrTailBytes = 1000
	}
	obs := o.Observer
	if obs == nil {
		obs = executor.NopObserver{}
	}
	return &Executor{o: o, src: src, tmpl: tmpl, inst: inst, log: log, obs: obs,
		procs: map[uuid.UUID]*proc{}, runs: map[uuid.UUID]*run{}, handoffs: map[uuid.UUID]*RuntimeHandoff{},
		live: map[*proc]struct{}{}, pending: map[uuid.UUID]int{}, closedCh: make(chan struct{})}
}

func (e *Executor) event(agentID uuid.UUID, runID *uuid.UUID, typ string, data map[string]any) {
	e.obs.Event(agentID, runID, executor.Event{Type: typ, At: time.Now(), Data: data})
}

// ---- Execute ----

// Execute 实现 executor.Executor。
func (e *Executor) Execute(ctx context.Context, req executor.Request, sink executor.Sink) (executor.Result, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := &run{id: req.RunID, cancel: cancel, done: make(chan struct{})}
	defer close(r.done)

	if err := e.admitRun(runCtx, req.AgentID, r); err != nil {
		return executor.Result{}, err
	}
	defer func() {
		e.mu.Lock()
		delete(e.runs, req.AgentID)
		e.mu.Unlock()
	}()

	snap, err := e.src.ExecutionSnapshot(runCtx, req.AgentID)
	if errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		return failed(executor.ErrCredentialUnavailable, "该 Agent 有凭证不可用或需要重新录入，请重新录入后再试", 0), nil
	}
	if err != nil {
		return executor.Result{}, err
	}
	if blocked, reason, err := e.beforeRun(runCtx, snap, req); err != nil {
		return executor.Result{}, err
	} else if blocked {
		return failed(executor.ErrPluginBlocked, reason, snap.ConfigVersion), nil
	}
	if len(req.Images) > 0 && !e.o.Models.SupportsImages(snap.Provider, snap.ModelID) {
		return failed(executor.ErrImageUnsupported, "本轮选定模型不支持图片，图片未发送", snap.ConfigVersion), nil
	}
	// Dynamic Lark credentials must never reach the legacy bot/direct launch
	// path. Keep this closed until the concrete captured-scope runtime factory
	// and private exec manager are wired; no boolean/mock readiness bypass.
	if snap.LarkBinding != nil && e.o.LarkFactory == nil {
		return failed(executor.ErrCredentialUnavailable, "Lark 用户运行通道尚未装配", snap.ConfigVersion), nil
	}
	if snap.Session.State == "missing" {
		return failed(executor.ErrSessionMissing, "会话文件丢失，请在控制台确认重开会话", snap.ConfigVersion), nil
	}

	p, err := e.acquire(runCtx, r, snap, sink)
	if err != nil {
		if r.interrupted.Load() {
			return executor.Result{Outcome: executor.OutcomeInterrupted, ConfigVersion: snap.ConfigVersion}, nil
		}
		var f *failure
		if errors.As(err, &f) {
			return failed(f.code, f.msg, snap.ConfigVersion), nil
		}
		return executor.Result{}, err
	}

	r.mu.Lock()
	if r.interrupted.Load() {
		r.mu.Unlock()
		e.turnFinished(p)
		return executor.Result{Outcome: executor.OutcomeInterrupted, ConfigVersion: snap.ConfigVersion}, nil
	}
	r.proc, r.prompting = p, false
	r.mu.Unlock()

	runID := req.RunID
	p.curRun.Store(&runID)
	if snap.ThinkingLevel != nil {
		thinkingCtx, cancel := context.WithTimeout(runCtx, e.o.StartTimeout)
		err := p.client.SetThinkingLevel(thinkingCtx, *snap.ThinkingLevel)
		cancel()
		if err != nil {
			e.turnFinished(p)
			if r.interrupted.Load() || runCtx.Err() != nil {
				return executor.Result{Outcome: executor.OutcomeInterrupted, ConfigVersion: snap.ConfigVersion}, nil
			}
			return failed(executor.ErrPiError, "无法应用本轮思考等级", snap.ConfigVersion), nil
		}
	}
	r.mu.Lock()
	if r.interrupted.Load() || runCtx.Err() != nil {
		r.mu.Unlock()
		e.turnFinished(p)
		return executor.Result{Outcome: executor.OutcomeInterrupted, ConfigVersion: snap.ConfigVersion}, nil
	}
	r.prompting = true
	r.mu.Unlock()
	sink.Running(snap.ConfigVersion)
	tr, err := p.client.Prompt(ctx, req.Message, sinkAdapter{s: sink, command: func(id, name, command string) {
		if command = toolcommand.Display(name, command); command != "" {
			e.event(snap.AgentID, &runID, "tool.command", map[string]any{"toolCallId": id, "toolName": name, "command": command})
		}
	}}, req.Images...)
	if errors.Is(err, pirpc.ErrExited) {
		tr, err = pirpc.TurnResult{Outcome: pirpc.OutcomeFailed, ErrorCode: executor.ErrPiExited, ErrorMessage: "Pi 进程已退出"}, nil
	}
	if tr.ErrorCode == executor.ErrPiExited {
		// 进程已退出：立刻移出进程表，下一条消息启动新进程（不等读协程确认 EOF）。
		e.discard(p, &runID)
	}
	sessionErr := e.turnFinished(p)
	if err == nil && sessionErr != nil && tr.Outcome == pirpc.OutcomeCompleted {
		return failed(executor.ErrInternal, "会话记录保存失败", snap.ConfigVersion), nil
	}
	if err != nil {
		return executor.Result{}, err
	}
	res := executor.Result{Text: tr.Text, ConfigVersion: snap.ConfigVersion}
	switch {
	case tr.Outcome == pirpc.OutcomeAborted, r.interrupted.Load() && tr.Outcome == pirpc.OutcomeFailed:
		res.Outcome = executor.OutcomeInterrupted
	case tr.Outcome == pirpc.OutcomeCompleted:
		res.Outcome = executor.OutcomeCompleted
	default:
		res.Outcome = executor.OutcomeFailed
		res.ErrorCode, res.ErrorMessage = tr.ErrorCode, tr.ErrorMessage
		if res.ErrorCode == executor.ErrPiExited {
			res.ErrorMessage = strings.TrimSpace("Pi 进程异常退出 " + tail(p.client.Stderr(), e.o.StderrTailBytes))
		}
	}
	return res, nil
}

func failed(code, msg string, version int64) executor.Result {
	return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: code, ErrorMessage: msg, ConfigVersion: version}
}

type sinkAdapter struct {
	s       executor.Sink
	command func(string, string, string)
}

func (a sinkAdapter) ToolCommand(id, name, command string) {
	if a.command != nil {
		a.command(id, name, command)
	}
}

func (a sinkAdapter) TextDelta(d string) { a.s.TextDelta(d) }
func (a sinkAdapter) ToolStarted(id, name string) {
	a.s.Tool(id, name, executor.ToolStarted)
}
func (a sinkAdapter) ToolEnded(id, name string, isError bool) {
	state := executor.ToolSucceeded
	if isError {
		state = executor.ToolFailed
	}
	a.s.Tool(id, name, state)
}

// acquire 返回可用于该 Agent 的进程：复用配置版本一致的存活进程，否则在名额内启动新进程。
func (e *Executor) acquire(ctx context.Context, r *run, snap agentconfig.Snapshot, sink executor.Sink) (*proc, error) {
	waitingReported := false
	startReason := "no_process"
	for {
		e.mu.Lock()
		if p := e.procs[snap.AgentID]; p != nil {
			if p.version == snap.ConfigVersion && p.credentialIdentity == snap.CredentialIdentity && p.promptIdentity == prompts.Identity(snap.PromptLayers) && sameLarkBinding(p.larkBinding, snap.LarkBinding) && alive(p) {
				p.busy = true
				if p.idleTimer != nil {
					p.idleTimer.Stop()
				}
				e.mu.Unlock()
				if err := e.syncCredentialFiles(snap, p.dirs); err != nil {
					e.turnFinished(p)
					return nil, fail(executor.ErrCredentialUnavailable, "CLI凭证缓存重建失败")
				}
				if err := seedModelAuth(snap, p.dirs); err != nil {
					e.turnFinished(p)
					return nil, fail(executor.ErrCredentialUnavailable, "模型凭证缓存重建失败")
				}
				e.event(snap.AgentID, &r.id, store.EventPiReused, map[string]any{"pid": p.client.Pid(), "startedAt": p.startedAt})
				e.obs.ProcessChanged(snap.AgentID)
				return p, nil
			}
			delete(e.procs, snap.AgentID)
			e.mu.Unlock()
			if !alive(p) {
				// 进程已退出但还没被 watch 移出：记下意外退出后清理。
				e.recordExit(p, nil)
				e.stop(p, stopCause{})
			} else {
				// 配置变化：停止旧进程后按新配置重启。
				e.stop(p, stopCause{reason: stopConfigChanged, runID: &r.id})
				startReason = stopConfigChanged
			}
			continue
		}
		if e.o.MaxProcesses == 0 || len(e.procs)+e.starting < e.o.MaxProcesses {
			e.starting++
			e.mu.Unlock()
			break
		}
		if victim := e.lruIdleLocked(); victim != nil {
			delete(e.procs, victim.agentID)
			e.mu.Unlock()
			victim.log.Info("进程数已到上限，关闭最久未用的空闲 Pi 进程")
			e.stop(victim, stopCause{reason: stopEvicted, forAgent: &snap.AgentID})
			continue
		}
		ch := make(chan struct{}, 1)
		if waitingReported {
			e.waiters = append([]chan struct{}{ch}, e.waiters...) // 被唤醒后仍未拿到名额，保持原位次
		} else {
			e.waiters = append(e.waiters, ch)
		}
		e.mu.Unlock()
		if !waitingReported {
			sink.Phase(executor.PhaseWaitingSlot)
			waitingReported = true
		}
		select {
		case <-ch:
		case <-ctx.Done():
			e.removeWaiter(ch)
			return nil, ctx.Err()
		}
	}

	p, err := e.launch(ctx, snap, sink, r.id, startReason)
	e.mu.Lock()
	e.starting--
	if err == nil {
		if e.closed {
			e.mu.Unlock()
			e.stop(p, stopCause{reason: stopShutdown})
			return nil, errors.New("执行器已关闭")
		}
		p.busy = true
		e.procs[snap.AgentID] = p
	}
	e.mu.Unlock()
	if err != nil {
		e.wakeOne()
		return nil, err
	}
	go e.watch(p)
	e.obs.ProcessChanged(snap.AgentID)
	return p, nil
}

func alive(p *proc) bool {
	select {
	case <-p.client.Done():
		return false
	default:
		return true
	}
}

func (e *Executor) lruIdleLocked() *proc {
	var victim *proc
	for _, p := range e.procs {
		if p.busy || e.handoffs[p.agentID] != nil {
			continue
		}
		if victim == nil || p.lastUsed.Before(victim.lastUsed) {
			victim = p
		}
	}
	return victim
}

func (e *Executor) wakeOne() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.waiters) == 0 {
		return
	}
	ch := e.waiters[0]
	e.waiters = e.waiters[1:]
	ch <- struct{}{}
}

func (e *Executor) removeWaiter(ch chan struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, w := range e.waiters {
		if w == ch {
			e.waiters = append(e.waiters[:i], e.waiters[i+1:]...)
			return
		}
	}
	// 已被唤醒但放弃了名额：转交给下一位。
	select {
	case <-ch:
		if len(e.waiters) > 0 {
			next := e.waiters[0]
			e.waiters = e.waiters[1:]
			next <- struct{}{}
		}
	default:
	}
}

// launch 准备配置并启动 Pi 进程；startReason 为 no_process 或 config_changed。
func (e *Executor) launch(ctx context.Context, snap agentconfig.Snapshot, sink executor.Sink, runID uuid.UUID, startReason string) (result *proc, err error) {
	d := e.dirs(snap.AgentID)
	sink.Phase(executor.PhasePreparing)
	if err := d.ensure(); err != nil {
		return nil, err
	}
	lark, shim, larkEnv, err := e.openLarkRuntime(ctx, snap, d)
	if err != nil {
		return nil, err
	}
	attached := false
	defer func() {
		if !attached && e.closeLarkRuntime(lark) != nil {
			result, err = nil, fail(executor.ErrCredentialUnavailable, "Lark 用户运行通道回收未确认")
		}
	}()
	prep, err := e.prepare(ctx, snap, d, runID, shim)
	if err != nil {
		return nil, err
	}
	sink.Phase(executor.PhaseLaunching)
	sessionFile := ""
	if snap.Session.State == "active" && snap.Session.SessionFile != "" {
		// Pi 遇到不存在的会话文件路径会悄悄新建会话，所以必须先自己检查。
		if _, err := os.Stat(snap.Session.SessionFile); err != nil {
			if mErr := e.src.MarkSessionMissing(ctx, snap.AgentID, &runID); mErr != nil {
				return nil, mErr
			}
			return nil, fail(executor.ErrSessionMissing, "记录的会话文件 %s 已不存在，请在控制台确认重开会话", snap.Session.SessionFile)
		}
		sessionFile = snap.Session.SessionFile
	}
	env := append(slices.Clone(prep.env), larkEnv...)
	var tokenID uuid.UUID
	if e.o.Tokens != nil {
		id, tok, err := e.o.Tokens.IssueAgentToken(ctx, snap.AgentID)
		if err != nil {
			return nil, fail(executor.ErrInternal, "签发 Agent 令牌失败：%v", err)
		}
		tokenID = id
		env = append(slices.Clone(env), "LOOPCTL_AGENT_URL="+e.o.AgentURL, "LOOPCTL_AGENT_TOKEN="+tok)
	}
	args := piArgs(d, snap, prep.skillDirs, sessionFile)
	if snap.ServiceTier != nil {
		resolved, tierErr := agentconfig.ResolveServiceTier(snap.Provider, snap.ServiceTier)
		if tierErr != nil || resolved == nil || !authhelper.ValidateServiceTierEntry(e.o.CodexServiceTierExtension) {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrPiStartFailed, "Codex速度配置或内置扩展不可用")
		}
		args = append(args, "-e", e.o.CodexServiceTierExtension)
		env = append(env, "LOOP_NOW_CODEX_SERVICE_TIER="+*resolved)
	}
	var bridge *modelauth.RuntimeBridge
	if snap.ModelCredential.Kind() == "oauth" {
		if snap.ModelBinding == nil || !modelauth.ValidateEntry(e.o.AuthExtension) {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "任务授权刷新通道未配置")
		}
		bridge, err = modelauth.NewRuntimeBridge(*snap.ModelBinding, e.o.AuthResolver, e.o.AuthIPCTimeout)
		if err != nil {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "任务授权刷新通道不可用")
		}
		args = append(args, "-e", e.o.AuthExtension)
		ipcTimeout := e.o.AuthIPCTimeout
		if ipcTimeout <= 0 {
			ipcTimeout = 30 * time.Second
		}
		env = append(slices.Clone(env), "LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS="+strconv.FormatInt(max(int64(1), ipcTimeout.Milliseconds()), 10))
	}
	if snap.ModelCredential.NeedsLegacyAnthropic() {
		if snap.Provider != "anthropic" || !modelauth.ValidateEntry(e.o.LegacyAuthExtension) || !filepath.IsAbs(e.o.PiPackageRoot) {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "旧认证适配未配置")
		}
		args = append(args, "-e", e.o.LegacyAuthExtension)
		env = append(slices.Clone(env), "LOOP_NOW_PI_PACKAGE_ROOT="+e.o.PiPackageRoot)
	}
	var googleBridge *cliauth.Broker
	if snap.GoogleBinding != nil {
		if !filepath.IsAbs(e.o.GoogleRuntimeEntry) {
			if bridge != nil {
				bridge.Close()
			}
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "Google刷新通道未配置")
		}
		googleBridge, err = cliauth.NewBroker(*snap.GoogleBinding, e.o.GoogleResolver)
		if err != nil {
			if bridge != nil {
				bridge.Close()
			}
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "Google刷新通道不可用")
		}
		for key, value := range googleBridge.Environment() {
			env = append(env, key+"="+value)
		}
		env = append(env, "LOOP_NOW_GCLOUD_RUNTIME_ENTRY="+e.o.GoogleRuntimeEntry)
	}
	args = append(args, pluginArgs(snap.Plugins)...)
	cmd := exec.Command(e.o.PiBin, args...)
	if bridge != nil {
		cmd.ExtraFiles = bridge.ChildFiles()
	}
	cmd.Env = env
	cmd.Dir = d.workspace
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := ctx.Err(); err != nil {
		if googleBridge != nil {
			closeGoogleBroker(googleBridge)
		}
		if bridge != nil {
			bridge.Close()
		}
		e.revokeToken(tokenID, e.snapshotLog(snap))
		return nil, err
	}
	begin := time.Now()
	client, err := pirpc.Start(cmd)
	if err != nil {
		if googleBridge != nil {
			closeGoogleBroker(googleBridge)
		}
		if bridge != nil {
			bridge.Close()
		}
		e.revokeToken(tokenID, e.snapshotLog(snap))
		return nil, fail(executor.ErrPiStartFailed, "启动 Pi 失败：%v", err)
	}
	if bridge != nil {
		bridge.ChildStarted()
	}
	p := &proc{larkRuntime: lark, googleBridge: googleBridge, authBridge: bridge, log: e.snapshotLog(snap), agentID: snap.AgentID, client: client, version: snap.ConfigVersion, promptIdentity: prompts.Identity(snap.PromptLayers), credentialIdentity: snap.CredentialIdentity, provider: snap.Provider, model: snap.ModelID,
		startedAt: begin, dirs: d, lastUsed: time.Now(), recorded: snap.Session.SessionFile, tokenID: tokenID, stopDone: make(chan struct{})}
	if identity, identityErr := readRecoveryProcess(client.Pid()); identityErr == nil && validRecoveryProcess(&identity) {
		p.recoveryIdentity = &identity
	}
	if snap.LarkBinding != nil {
		binding := *snap.LarkBinding
		p.larkBinding = &binding
	}
	attached = true
	e.mu.Lock()
	e.live[p] = struct{}{}
	e.mu.Unlock()
	client.SetObserver(procObserver{e: e, p: p})
	if e.o.Dialogs != nil {
		client.SetDialogs(func(ctx context.Context, req pirpc.DialogRequest) pirpc.DialogResponse { return e.dialog(ctx, p, req) })
	}
	e.writePidFile(p)
	stateCtx, cancel := context.WithTimeout(ctx, e.o.StartTimeout)
	st, err := client.GetState(stateCtx)
	cancel()
	if err != nil {
		e.stop(p, stopCause{})
		return nil, fail(executor.ErrPiStartFailed, "Pi 启动后未就绪：%v %s", err, tail(client.Stderr(), e.o.StderrTailBytes))
	}
	p.sessionFile = st.SessionFile
	startup := time.Since(begin)
	e.event(snap.AgentID, &runID, store.EventPiStarted, map[string]any{
		"pid": client.Pid(), "configVersion": snap.ConfigVersion, "startupMs": startup.Milliseconds(), "reason": startReason})
	e.snapshotLog(snap).Info("Pi 进程启动", "runId", runID, "pid", client.Pid(), "startup", startup, "reason", startReason)
	return p, nil
}

// discard 把已退出的进程移出进程表，记下意外退出，并确保其进程组结束。
func (e *Executor) discard(p *proc, runID *uuid.UUID) {
	p.exitRun.Store(runID)
	e.mu.Lock()
	if e.procs[p.agentID] == p {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	go func() {
		if !p.stopping.Load() { // 打断超时等平台主动结束的，不算意外退出
			e.recordExit(p, runID)
		}
		e.stop(p, stopCause{})
	}()
}

// recordExit 等进程结束后记下一次意外退出（每个进程至多一次）。
func (e *Executor) recordExit(p *proc, runID *uuid.UUID) {
	p.exitOnce.Do(func() {
		select {
		case <-p.client.Done():
		case <-time.After(e.o.StopGrace):
		}
		exit := "进程仍未结束"
		if !alive(p) {
			exit = "exit status 0"
			if err := p.client.ExitErr(); err != nil {
				exit = err.Error()
			}
		}
		stderr := tail(p.client.Stderr(), e.o.StderrTailBytes)
		e.event(p.agentID, runID, store.EventPiExited, map[string]any{
			"pid": p.client.Pid(), "uptimeMs": time.Since(p.startedAt).Milliseconds(), "exit": exit, "stderrTail": stderr})
		p.log.Warn("Pi 进程意外退出", "runId", runID, "pid", p.client.Pid(), "exit", exit)
		e.obs.ProcessChanged(p.agentID)
	})
}

// turnFinished 在一轮结束后记录会话文件、把进程标为空闲并启动空闲计时。
func (e *Executor) turnFinished(p *proc) error {
	sessionErr := e.recordSession(p)
	p.curRun.Store(nil)
	e.mu.Lock()
	p.busy = false
	p.lastUsed = time.Now()
	if e.procs[p.agentID] == p && alive(p) {
		if p.idleTimer == nil {
			p.idleTimer = time.AfterFunc(e.o.IdleTimeout, func() { e.idleExpired(p) })
		} else {
			p.idleTimer.Reset(e.o.IdleTimeout)
		}
	}
	e.mu.Unlock()
	e.wakeOne()
	e.obs.ProcessChanged(p.agentID)
	return sessionErr
}

// recordSession 只在会话文件已写入磁盘时记录路径。
func (e *Executor) recordSession(p *proc) error {
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	if p.sessionFile == "" {
		return errors.New("session_file_unconfirmed")
	}
	if _, err := os.Stat(p.sessionFile); err != nil {
		return errors.New("session_file_unconfirmed")
	}
	if p.sessionFile == p.recorded {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.o.StartTimeout)
	defer cancel()
	if err := e.src.RecordSessionFile(ctx, p.agentID, p.sessionFile); err != nil {
		p.log.Error("记录会话文件失败", "runId", p.curRun.Load(), "code", "session_record_failed")
		return errors.New("session_record_failed")
	}
	p.recorded = p.sessionFile
	return nil
}

func (e *Executor) idleExpired(p *proc) {
	e.mu.Lock()
	if e.procs[p.agentID] != p || p.busy || e.handoffs[p.agentID] != nil {
		e.mu.Unlock()
		return
	}
	delete(e.procs, p.agentID)
	e.mu.Unlock()
	e.stop(p, stopCause{reason: stopIdleTimeout})
}

// watch 在进程意外退出时把它移出表，记下退出，并让出名额。
func (e *Executor) watch(p *proc) {
	<-p.client.Done()
	e.revokeProcToken(p)
	e.mu.Lock()
	removed := e.procs[p.agentID] == p
	if removed {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	if !p.stopping.Load() {
		// 先读 curRun：discard 在清空 curRun 之前设置 exitRun，两者至少有一个能对上。
		run := p.curRun.Load()
		if run == nil {
			run = p.exitRun.Load()
		}
		e.recordExit(p, run)
	}
	if removed {
		e.removeProcPidFile(p)
		e.wakeOne()
	}
	e.stop(p, stopCause{})
}

// stop 依次关闭 stdin、SIGTERM、SIGKILL 结束进程组，前一步未生效才进入下一步；
// 原因不为空时，进程结束后记录一次停止事件。
func (e *Executor) stop(p *proc, why stopCause) {
	p.stopOnce.Do(func() {
		defer func() {
			e.mu.Lock()
			if p.stopErr == nil {
				delete(e.live, p)
			}
			e.mu.Unlock()
			close(p.stopDone)
		}()
		if p.larkRuntime != nil {
			defer func() {
				if e.closeLarkRuntime(p.larkRuntime) != nil {
					p.stopErr = ErrRuntimeStopUnconfirmed
				}
			}()
		}
		if p.googleBridge != nil {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if p.googleBridge.Close(ctx) != nil {
					p.stopErr = ErrRuntimeStopUnconfirmed
				}
			}()
		}
		if p.authBridge != nil {
			defer p.authBridge.Close()
		}
		begin := time.Now() // 停止事件记在开始停止的时刻，而不是进程结束之后
		p.stopping.Store(true)
		e.revokeProcToken(p) // 先作废令牌：进程开始停止后不能再调用 Agent 接口
		if p.revokeErr != nil {
			p.stopErr = ErrRuntimeStopUnconfirmed
		}
		if p.idleTimer != nil {
			p.idleTimer.Stop()
		}
		e.recordSession(p)
		_ = p.client.CloseStdin()
		if !waitDone(p, e.o.StopGrace) {
			_ = syscall.Kill(-p.client.Pid(), syscall.SIGTERM)
			if !waitDone(p, e.o.StopGrace) {
				_ = syscall.Kill(-p.client.Pid(), syscall.SIGKILL)
				if !waitDone(p, e.o.StopGrace) {
					p.stopErr = ErrRuntimeStopUnconfirmed
				}
			}
		}
		e.removeProcPidFile(p)
		if why.reason != "" {
			e.recordStop(p, why, begin)
		}
		e.obs.ProcessChanged(p.agentID)
	})
	e.wakeOne()
}

// revokeProcToken 作废进程的 Agent 令牌，每个进程至多一次。
func (e *Executor) revokeProcToken(p *proc) {
	p.revokeOnce.Do(func() { p.revokeErr = e.revokeToken(p.tokenID, p.log) })
}

func (e *Executor) revokeToken(tokenID uuid.UUID, log *slog.Logger) error {
	if e.o.Tokens == nil || tokenID == uuid.Nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.handoffTimeout())
	defer cancel()
	if err := e.o.Tokens.RevokeAgentToken(ctx, tokenID); err != nil {
		log.Error("作废 Agent 令牌失败", "tokenId", tokenID, "err", err)
		return ErrRuntimeStopUnconfirmed
	}
	return nil
}

// recordStop 记录一次平台主动停止进程，时间为开始停止的时刻。
func (e *Executor) recordStop(p *proc, why stopCause, at time.Time) {
	data := map[string]any{"pid": p.client.Pid(), "reason": why.reason, "uptimeMs": at.Sub(p.startedAt).Milliseconds()}
	if why.forAgent != nil {
		data["forAgentId"] = why.forAgent.String()
	}
	e.obs.Event(p.agentID, why.runID, executor.Event{Type: store.EventPiStopped, At: at, Data: data})
	p.log.Info("Pi 进程停止", "runId", why.runID, "pid", p.client.Pid(), "reason", why.reason)
}

func waitDone(p *proc, d time.Duration) bool {
	select {
	case <-p.client.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// procObserver 把 Pi 报告的用量与事件转给执行器的观察者：一轮之中的关联到当前执行，其余只关联到 Agent。
type procObserver struct {
	e *Executor
	p *proc
}

func (o procObserver) runID(inTurn bool) *uuid.UUID {
	if !inTurn {
		return nil
	}
	return o.p.curRun.Load()
}

func (o procObserver) Usage(u pirpc.Usage, inTurn bool) {
	eu := executor.Usage{Kind: u.Kind, Provider: u.Provider, Model: u.Model, ResponseModel: u.ResponseModel, StopReason: u.StopReason,
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, DurationMs: u.DurationMs}
	if u.Kind == pirpc.UsageCompaction && eu.Provider == "" {
		// Pi 自动压缩使用会话当前模型；平台启动时指定模型且不中途切换。
		eu.Provider, eu.Model = o.p.provider, o.p.model
	}
	if eu.Provider == "" || eu.Model == "" {
		eu.Provider, eu.Model = "", ""
	}
	o.e.obs.Usage(o.p.agentID, o.runID(inTurn), eu)
}

// dialog 把对话交给 Options.Dialogs，限定在发出它的那一轮：这一轮结束（进程转去执行别的）时不再等待回答。
func (e *Executor) dialog(ctx context.Context, p *proc, req pirpc.DialogRequest) pirpc.DialogResponse {
	run := p.curRun.Load()
	if run == nil {
		return pirpc.DialogResponse{Cancelled: true, Outcome: "cancelled"}
	}
	runID := *run
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if cur := p.curRun.Load(); cur == nil || *cur != runID {
					cancel()
					return
				}
			}
		}
	}()
	return e.o.Dialogs(ctx, p.agentID, runID, req)
}

func (o procObserver) Notice(n pirpc.Notice, inTurn bool) {
	typ := ""
	switch n.Type {
	case pirpc.NoticeRetryStarted:
		typ = store.EventModelRetryStarted
	case pirpc.NoticeRetryEnded:
		typ = store.EventModelRetryEnded
	case pirpc.NoticeCompaction:
		typ = store.EventContextCompaction
	case pirpc.NoticeExtensionUI:
		typ = store.EventExtensionUI
	default:
		return
	}
	o.e.event(o.p.agentID, o.runID(inTurn), typ, n.Data)
}

// ---- Interrupt / Release / 状态 / 关闭 ----

// Interrupt 实现 executor.Executor：异步执行，立即返回。只打断 ID 为 runID 的那一次执行。
func (e *Executor) Interrupt(_ context.Context, agentID, runID uuid.UUID) error {
	e.mu.Lock()
	r := e.runs[agentID]
	e.mu.Unlock()
	if r == nil || r.id != runID {
		return nil // 没有执行，或当前已是另一次执行：迟到的打断不作用到它上面
	}
	r.mu.Lock()
	if r.interrupted.Swap(true) {
		r.mu.Unlock()
		return nil // 已在打断中
	}
	p, prompting := r.proc, r.prompting
	r.mu.Unlock()
	if !prompting {
		r.cancel() // 尚在等待名额或准备阶段：直接放弃
		return nil
	}
	go e.abort(r, p)
	return nil
}

// abort 先清空 Pi 的内部队列再中止当前执行；在打断超时内反复尝试，超时后强制结束进程（会话文件保留）。
func (e *Executor) abort(r *run, p *proc) {
	deadline := time.Now().Add(e.o.AbortTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err := p.client.Abort(ctx)
		cancel()
		if err == nil {
			select {
			case <-r.done:
				return
			case <-time.After(300 * time.Millisecond):
				// Prompt 可能刚好还没发出，再试一次。
				continue
			}
		}
		select {
		case <-r.done:
			return
		default:
		}
		if errors.Is(err, pirpc.ErrExited) {
			return
		}
	}
	select {
	case <-r.done:
		return
	default:
	}
	p.log.Warn("打断超时，强制结束 Pi 进程", "runId", r.id)
	e.mu.Lock()
	if e.procs[p.agentID] == p {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	// 先记下停止再强杀：强杀后执行可能先以进程退出结束并清理进程，停止事件不能依赖之后的 stop 调用。
	e.recordStop(p, stopCause{reason: stopAbortTimeout, runID: &r.id}, time.Now())
	p.stopping.Store(true)
	_ = syscall.Kill(-p.client.Pid(), syscall.SIGKILL)
	e.stop(p, stopCause{})
}

// Release 实现 executor.Executor：停止进程并删除独立 HOME（工作区与会话文件保留）。
func (e *Executor) Release(ctx context.Context, agentID uuid.UUID) error {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return err
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	delete(e.procs, agentID)
	e.mu.Unlock()
	if p != nil {
		e.stop(p, stopCause{reason: stopReleased})
	}
	return releaseHome(e.o.WorkRoot, agentID)
}

// beforeRun 调用拦截类 Hook run.before_start（plugins/SPEC.md §4）：被阻止时这一轮以 plugin_blocked 失败，不启动 Pi。
func (e *Executor) beforeRun(ctx context.Context, snap agentconfig.Snapshot, req executor.Request) (bool, string, error) {
	if e.o.Hooks == nil {
		return false, "", nil
	}
	agent, run := snap.AgentID, req.RunID
	if snap.ConfigurationAgentID != uuid.Nil {
		agent = snap.ConfigurationAgentID
	}
	var task *uuid.UUID
	if snap.TaskID != uuid.Nil {
		t := snap.TaskID
		task = &t
	}
	fields := map[string]any{"origin": req.Origin, "messageId": nil}
	if req.MessageID != uuid.Nil {
		fields["messageId"] = req.MessageID
	}
	o, err := e.o.Hooks.Hook(ctx, pluginhost.Intercept, pluginhost.Event("run.before_start", snap.ContextProjectID, &agent, task, &run, fields), snap.ContextProjectID)
	if err != nil || !o.Blocked {
		return false, "", err
	}
	reason := o.Reason
	if reason == "" {
		reason = "没有说明原因"
	}
	return true, "插件 " + o.Plugin + " 阻止了这一轮：" + reason, nil
}

// Process 实现 executor.Executor。
// FreezeForFork 由持有常态派发门禁的任务准备器调用：当前轮结束后停止空闲Pi，稳定源会话。
// 保留HOME/工作区/会话，下条常态消息照常重启接续。
func (e *Executor) FreezeForFork(ctx context.Context, agentID uuid.UUID) error {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return err
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	if e.runs[agentID] != nil || p != nil && p.busy {
		e.mu.Unlock()
		return errors.New("常态轮次尚未完全结束")
	}
	delete(e.procs, agentID)
	e.mu.Unlock()
	if p != nil {
		e.stop(p, stopCause{reason: "fork_snapshot"})
		select {
		case <-p.client.Done():
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (e *Executor) Process(agentID uuid.UUID) executor.ProcessInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p := e.procs[agentID]; p != nil {
		started := p.startedAt
		info := executor.ProcessInfo{State: executor.ProcessIdle, PID: p.client.Pid(), StartedAt: &started}
		if p.busy {
			info.State = executor.ProcessBusy
		}
		return info
	}
	if e.runs[agentID] != nil {
		return executor.ProcessInfo{State: executor.ProcessStarting}
	}
	return executor.ProcessInfo{State: executor.ProcessStopped}
}

// Shutdown 实现 executor.Executor：结束全部进程。
func (e *Executor) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	if !e.closed {
		close(e.closedCh)
	}
	e.closed = true
	procs := make([]*proc, 0, len(e.procs))
	for id, p := range e.procs {
		procs = append(procs, p)
		delete(e.procs, id)
	}
	for _, r := range e.runs {
		r.cancel()
	}
	e.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Go(func() { e.stop(p, stopCause{reason: stopShutdown}) })
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- 遗留进程与已删除 Agent 的清理 ----

type pidRecord struct {
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
	InstanceID string    `json:"instanceId"`
	Hostname   string    `json:"hostname"`
	SessionDir string    `json:"sessionDir"`
}

func (e *Executor) writePidFile(p *proc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, _ := json.Marshal(pidRecord{PID: p.client.Pid(), StartedAt: p.startedAt, InstanceID: e.o.InstanceID.String(), Hostname: e.o.Hostname, SessionDir: p.dirs.sessions})
	if err := os.WriteFile(p.dirs.pidFile, b, 0o600); err != nil {
		p.log.Warn("写入 pi.pid 失败", "err", err)
	}
}

// A late watcher/stop may run after the next generation wrote its pid record.
// Serialize with writes and only unlink the exact process epoch's record.
func (e *Executor) removeProcPidFile(p *proc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	raw, err := os.ReadFile(p.dirs.pidFile)
	if err != nil {
		return
	}
	var record pidRecord
	if json.Unmarshal(raw, &record) != nil || record.PID != p.client.Pid() || !record.StartedAt.Equal(p.startedAt) || record.InstanceID != e.o.InstanceID.String() {
		return
	}
	_ = os.Remove(p.dirs.pidFile)
}

// CleanupStale 在服务端启动时结束上一次运行遗留、仍存活的 Pi 进程，并删除已删除 Agent 的独立 HOME。
func (e *Executor) CleanupStale(ctx context.Context) error {
	return e.CleanupStaleExceptPending(ctx, nil)
}

// CleanupStaleExceptPending preserves exact pending-switch runtime evidence for
// the separate strict recovery proof, before any workers are admitted. It does
// not turn the legacy best-effort cleanup into a death receipt.
func (e *Executor) CleanupStaleExceptPending(ctx context.Context, pending []PendingRuntimeHandoff) error {
	protected := make(map[uuid.UUID]bool)
	for _, p := range pending {
		if !validPending(p) {
			return ErrRuntimeStopUnconfirmed
		}
		for _, id := range p.RuntimeIDs {
			protected[id] = true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(e.o.WorkRoot, "agents", "*", "pi.pid"))
	for _, f := range matches {
		if id, err := uuid.Parse(filepath.Base(filepath.Dir(f))); err == nil && protected[id] {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var rec pidRecord
		if json.Unmarshal(b, &rec) == nil && rec.PID > 0 && rec.Hostname == e.o.Hostname && rec.InstanceID != e.o.InstanceID.String() {
			if isOurPi(rec.PID, rec.SessionDir) {
				e.log.Warn("结束上一次运行遗留的 Pi 进程", "pid", rec.PID, "pidFile", f)
				_ = syscall.Kill(-rec.PID, syscall.SIGTERM)
				for i := 0; i < 20 && processAlive(rec.PID); i++ {
					time.Sleep(100 * time.Millisecond)
				}
				if processAlive(rec.PID) {
					_ = syscall.Kill(-rec.PID, syscall.SIGKILL)
				}
			}
		}
		_ = os.Remove(f)
	}
	ids, err := e.src.DeletedAgentIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if protected[id] {
			continue
		}
		_ = os.RemoveAll(e.dirs(id).home)
	}
	return nil
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// isOurPi 用进程命令行确认它确实是由本平台启动的该 Agent 的 Pi，避免误杀复用了 PID 的其他进程。
func isOurPi(pid int, sessionDir string) bool {
	if !processAlive(pid) {
		return false
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	cmd := string(out)
	return strings.Contains(cmd, "--mode rpc") && sessionDir != "" && strings.Contains(cmd, sessionDir)
}

func (e *Executor) snapshotLog(snap agentconfig.Snapshot) *slog.Logger {
	if snap.TaskID != uuid.Nil {
		return e.log.With("agentId", snap.ConfigurationAgentID, "runtimeId", snap.AgentID, "taskId", snap.TaskID, "instanceId", e.o.InstanceID)
	}
	return e.log.With("agentId", snap.AgentID, "instanceId", e.o.InstanceID)
}

func closeGoogleBroker(b *cliauth.Broker) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.Close(ctx)
}
