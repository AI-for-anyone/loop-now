package local

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/procgroup"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/cliauth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/toolcommand"
)

// Source is the data source the executor reads config from and writes state back to; agentconfig.Service implements it.
type Source interface {
	ExecutionSnapshot(ctx context.Context, agentID uuid.UUID) (agentconfig.Snapshot, error)
	ReadBlob(ctx context.Context, sha string) ([]byte, error)
	SourceFilesForSync(ctx context.Context, sourceID uuid.UUID) ([]store.SyncFile, error)
	MarkSourceSynced(ctx context.Context, sourceID uuid.UUID, processed []store.SyncFile) error
	RecordSessionFile(ctx context.Context, agentID uuid.UUID, path string) error
	MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error
	DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error)
}

// Options are the local executor's parameters, all taken from the config and the startup checks.
type Options struct {
	Models modelcatalog.Reader
	// Hooks calls the plugins' run.before_start before each turn starts; nil means it is not called.
	Hooks pluginhost.Hooker
	// Dialogs answers the confirm, select and input requests an extension sends during a turn (presented as console
	// questions); when nil, every request is answered as cancelled. agentID is the Agent that runs (for a task turn,
	// the task instance); ctx ends when this turn is no longer the process's current run or when the process exits.
	Dialogs                   func(ctx context.Context, agentID, runID uuid.UUID, req pirpc.DialogRequest) pirpc.DialogResponse
	LarkFactory               func(context.Context, LarkRuntimeRequest) (LarkRuntime, error)
	GoogleResolver            cliauth.GrantResolver
	GoogleRuntimeEntry        string
	AuthResolver              modelauth.RuntimeResolver
	AuthExtension             string
	CodexServiceTierExtension string
	LegacyAuthExtension       string
	PiPackageRoot             string
	PiAIDir                   string // pi-ai directory resolved by the preflight check (inside pi-coding-agent or next to it)
	AuthIPCTimeout            time.Duration
	WorkRoot                  string
	PiBin                     string
	NodeBin                   string // used to run PiBin when it is a .js file (packaged layout); empty means find node on PATH
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
	// StderrTailBytes is how much of the end of Pi's stderr goes into error messages and run records; 0 means 1000.
	StderrTailBytes int
	// Observer receives run records; nil means they are discarded.
	Observer executor.Observer
	// Tokens issues an Agent token for every Pi process started; nil means no token is injected (for tests).
	Tokens TokenIssuer
	// AgentURL is the LOOPCTL_AGENT_URL injected into Pi: the full base URL of the Agent-only API on the loopback
	// listener.
	AgentURL string
	// LoopctlBinDir is the directory of the installed loopctl of the same version, put at the front of the Agent's run
	// PATH; empty means it is not added (for tests).
	LoopctlBinDir string
	// ContextDir is the root of the project and global context folders; empty means no context entry and no path notes
	// are injected (for tests).
	ContextDir string
	// UserPath returns the directories of the user's PATH (appended after the run PATH when
	// “用本机的 CLI” ("Use local CLIs") is on); nil means nothing is appended.
	UserPath func(context.Context) []string
	// RealHome returns the real home directory of the user running the server process (used as Pi's HOME when
	// “用本机的登录状态” ("Use local logins") is on; the Claude Code runtime always uses it); nil means os.UserHomeDir.
	RealHome func() (string, error)
	// ClaudeBin is the claude executable; empty means look it up on the PATH of the user's login shell (UserPath).
	ClaudeBin string
}

// TokenIssuer issues and revokes Agent tokens. A token is issued before Pi starts and revoked when the process stops,
// exits or is discarded.
type TokenIssuer interface {
	IssueAgentToken(ctx context.Context, agentID uuid.UUID) (id uuid.UUID, token string, err error)
	RevokeAgentToken(ctx context.Context, id uuid.UUID) error
}

// Executor is the local executor.
type Executor struct {
	sessionCache sync.Map // session file path -> sessionMeta (for the /resume list)
	o            Options
	src          Source
	tmpl         *clitemplate.Catalog
	inst         *clitemplate.Installer
	log          *slog.Logger
	obs          executor.Observer

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
	claude   *claudecli.Probe // cache of claude's version and session record directory
}

type proc struct {
	recoveryIdentity    *RuntimeProcessIdentity // immutable kernel identity captured at launch
	larkRuntime         *larkResource
	larkBinding         *clicredential.LarkBinding
	googleBridge        *cliauth.Broker
	authBridge          *modelauth.RuntimeBridge
	log                 *slog.Logger
	agentID             uuid.UUID
	rt                  runtimeProcess // runtime process; the process table, stop, interrupt and running a turn use only this
	client              *pirpc.Client  // Pi's RPC client, only for Pi-specific operations; nil for other runtimes
	drv                 driver
	runtimeIdentity     string      // runtime name plus its own fingerprint; a change means restart
	runtimeBinary       string      // real path of the runtime executable (Claude Code)
	runtimeVersion      string      // runtime version (Claude Code)
	sessionID           string      // session ID the platform gave the runtime (Claude Code)
	sessionStale        atomic.Bool // runtime switched (between Pi and Claude Code): do not record its session file back any more
	initChecked         atomic.Bool // initial info reported by the process has been checked (Claude Code's Skill list)
	sessionUntracked    atomic.Bool // runtime's session record not found (Claude Code): not a save failure; next start opens a new session
	version             int64
	promptIdentity      string
	localIdentity       string // fingerprint of local mode (directory and PATH switches); a change means restart with the new config
	stateIdentity       string // fingerprint of the plugin provider's subscription state (version, dir, paths); a change means restart
	stateVersion        uuid.UUID
	credentialIdentity  string
	modelConfigIdentity string
	provider            string // provider and model at startup, used for compaction usage
	model               string
	startedAt           time.Time
	sessionFile         string // session file path reported by Pi
	sessMu              sync.Mutex
	recorded            string // session file path already written to the database (guarded by sessMu)
	busy                bool
	lastUsed            time.Time
	idleTimer           *time.Timer
	dirs                agentDirs
	stopOnce            sync.Once
	exitOnce            sync.Once
	stopping            atomic.Bool               // stopped on purpose by the platform; the exit is not unexpected
	curRun              atomic.Pointer[uuid.UUID] // the run currently replying on this process
	exitRun             atomic.Pointer[uuid.UUID] // the run during which the process exited (set before curRun is cleared)
	tokenID             uuid.UUID                 // Agent token issued for this process; zero value when none was issued
	revokeOnce          sync.Once
	revokeErr           error
	stopDone            chan struct{}
	stopErr             error
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

// stopCause says why a process is stopped; an empty reason means no stop event is recorded (for example when startup
// failed or the process has already exited).
type stopCause struct {
	reason   string
	runID    *uuid.UUID
	forAgent *uuid.UUID // the Agent a slot is freed for (when reason is evicted)
}

// Stop reasons (the reason of pi.stopped).
const (
	stopIdleTimeout   = "idle_timeout"
	stopConfigChanged = "config_changed"
	stopEvicted       = "evicted"
	stopReleased      = "released"
	stopShutdown      = "shutdown"
	stopAbortTimeout  = "abort_timeout"
	stopForkSnapshot  = "fork_snapshot"
	// Platform commands that stop an idle process: /new, /reload, /resume. console_export was the process that /export
	// html used to start temporarily for an export; exports now use pi --export and no longer start a process, and the
	// value is kept for old records.
	stopSessionReset   = "session_reset"
	stopConsoleReload  = "console_reload"
	stopSessionResumed = "session_resumed"
	stopConsoleExport  = "console_export"
)

// StopReasons are all the reasons pi.stopped can carry; ProcessExit.reason and TimelinePiStopped.reason in openapi must
// list them all (checked by the api contract test).
var StopReasons = []string{stopIdleTimeout, stopConfigChanged, stopEvicted, stopReleased, stopShutdown, stopAbortTimeout, stopForkSnapshot,
	stopSessionReset, stopConsoleReload, stopSessionResumed, stopConsoleExport}

// idleStopReasons are the reasons StopIdle accepts.
var idleStopReasons = []string{stopSessionReset, stopConsoleReload, stopSessionResumed}

var _ executor.Executor = (*Executor)(nil)

// New creates the local executor.
func New(o Options, src Source, tmpl *clitemplate.Catalog, inst *clitemplate.Installer, log *slog.Logger) *Executor {
	if o.RealHome == nil {
		o.RealHome = os.UserHomeDir
	}
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
		live: map[*proc]struct{}{}, pending: map[uuid.UUID]int{}, closedCh: make(chan struct{}), claude: &claudecli.Probe{}}
}

func (e *Executor) event(agentID uuid.UUID, runID *uuid.UUID, typ string, data map[string]any) {
	if runID != nil && *runID == uuid.Nil {
		runID = nil // maintenance such as export has no conversation run; must not write a dangling run foreign key
	}
	e.obs.Event(agentID, runID, executor.Event{Type: typ, At: time.Now(), Data: data})
}

// ---- Execute ----

// Execute implements executor.Executor.
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
		return failed(executor.ErrCredentialUnavailable, "Some of this Agent's credentials are unavailable or need to be entered again. Enter them again and try again.", 0), nil
	}
	if err != nil {
		return executor.Result{}, err
	}
	if req.Origin == executor.OriginConsoleCompact && isClaude(snap) {
		return failed(executor.ErrRuntimeError, "The current runtime does not support Pi manual compaction.", snap.ConfigVersion), nil
	}
	if blocked, reason, err := e.beforeRun(runCtx, snap, req); err != nil {
		return executor.Result{}, err
	} else if blocked {
		return failed(executor.ErrPluginBlocked, reason, snap.ConfigVersion), nil
	}
	if len(req.Images) > 0 && !e.o.Models.SupportsImages(snap.Provider, snap.ModelID) {
		return failed(executor.ErrImageUnsupported, "The model selected for this turn does not support images. The images were not sent.", snap.ConfigVersion), nil
	}
	// Dynamic Lark credentials must never reach the legacy bot/direct launch
	// path. Keep this closed until the concrete captured-scope runtime factory
	// and private exec manager are wired; no boolean/mock readiness bypass.
	if snap.LarkBinding != nil && e.o.LarkFactory == nil {
		return failed(executor.ErrCredentialUnavailable, "The Lark user runtime channel is not wired up yet.", snap.ConfigVersion), nil
	}
	if snap.Session.State == "missing" {
		return failed(executor.ErrSessionMissing, "The session file is missing. Confirm reopening the session in the console first.", snap.ConfigVersion), nil
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
	if err := p.drv.beforeTurn(runCtx, p, snap); err != nil {
		e.turnFinished(p)
		if r.interrupted.Load() || runCtx.Err() != nil {
			return executor.Result{Outcome: executor.OutcomeInterrupted, ConfigVersion: snap.ConfigVersion}, nil
		}
		var f *failure
		if errors.As(err, &f) {
			return failed(f.code, f.msg, snap.ConfigVersion), nil
		}
		return executor.Result{}, err
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
	if req.Origin == executor.OriginConsoleCompact {
		return e.compact(ctx, p, r, req, snap), nil
	}
	tr, err := p.rt.Prompt(ctx, req.Message, sinkAdapter{s: sink, command: func(id, name, command string) {
		if command = toolcommand.Display(name, command); command != "" {
			e.event(snap.AgentID, &runID, "tool.command", map[string]any{"toolCallId": id, "toolName": name, "command": command})
		}
	}}, req.Images...)
	exitCode, exitMsg := p.drv.exited()
	if errors.Is(err, runturn.ErrExited) {
		tr, err = runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: exitCode, ErrorMessage: exitMsg}, nil
	}
	if err == nil {
		tr = p.drv.afterTurn(p, tr)
	}
	switch {
	case exiting(p) && tr.ErrorCode != exitCode:
		// A process that was sent SIGINT on interrupt (Claude Code) is sure to exit afterwards: remove it from the
		// process table, so the next message continues the session in a new process instead of being written to this
		// one and failing.
		e.retire(p)
	case tr.ErrorCode == exitCode, !alive(p):
		// The process has exited: remove it from the process table right away, so the next message starts a new process
		// (without waiting for the reader goroutine to confirm EOF).
		e.discard(p, &runID)
	}
	sessionErr := e.turnFinished(p)
	if err == nil && sessionErr != nil && tr.Outcome == runturn.OutcomeCompleted {
		return failed(executor.ErrInternal, "Could not save the session record.", snap.ConfigVersion), nil
	}
	if err != nil {
		return executor.Result{}, err
	}
	res := executor.Result{Text: tr.Text, ConfigVersion: snap.ConfigVersion}
	switch {
	case tr.Outcome == runturn.OutcomeAborted, r.interrupted.Load() && tr.Outcome == runturn.OutcomeFailed:
		res.Outcome = executor.OutcomeInterrupted
	case tr.Outcome == runturn.OutcomeCompleted:
		res.Outcome, res.CommandHandled = executor.OutcomeCompleted, tr.Handled
		if tr.Handled && tr.Notices != nil {
			res.CommandNoticesTruncated = tr.Notices.Truncated
			for _, n := range tr.Notices.Items {
				res.CommandNotices = append(res.CommandNotices, executor.CommandNotice{Level: n.Level, Message: n.Message})
			}
		}
	default:
		res.Outcome = executor.OutcomeFailed
		res.ErrorCode, res.ErrorMessage = tr.ErrorCode, tr.ErrorMessage
		if res.ErrorCode == exitCode {
			// When stdout reaches EOF, stderr may still be being copied into the buffer by the os/exec goroutine: wait
			// for the process to end (with a limit, as in recordExit) before taking the tail.
			select {
			case <-p.rt.Done():
			case <-time.After(e.o.StopGrace):
			}
			res.ErrorMessage = strings.TrimSpace(p.drv.crashMessage(p, tail(p.rt.Stderr(), e.o.StderrTailBytes)))
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

// acquire returns a process usable for the Agent: it reuses a live process with the same config version, or else starts
// a new one within the slot limit.
func (e *Executor) acquire(ctx context.Context, r *run, snap agentconfig.Snapshot, sink executor.Sink) (*proc, error) {
	waitingReported := false
	startReason := "no_process"
	for {
		e.mu.Lock()
		if p := e.procs[snap.AgentID]; p != nil {
			if p.runtimeIdentity == runtimeIdentity(e.driverFor(snap), snap) && p.version == snap.ConfigVersion && p.credentialIdentity == snap.CredentialIdentity && p.modelConfigIdentity == snap.ModelConfigIdentity && p.promptIdentity == prompts.Identity(snap.PromptLayers) && p.localIdentity == snap.Local.Identity() && p.stateIdentity == snap.ProviderState.Identity() && sameLarkBinding(p.larkBinding, snap.LarkBinding) && alive(p) {
				p.busy = true
				if p.idleTimer != nil {
					p.idleTimer.Stop()
				}
				e.mu.Unlock()
				if err := e.syncCredentialFiles(snap, p.dirs); err != nil {
					e.turnFinished(p)
					return nil, fail(executor.ErrCredentialUnavailable, "Could not rebuild the CLI credential cache.")
				}
				if err := p.drv.refresh(snap, p.dirs); err != nil {
					e.turnFinished(p)
					return nil, err
				}
				e.event(snap.AgentID, &r.id, store.EventPiReused, map[string]any{"pid": p.rt.Pid(), "startedAt": p.startedAt, "runtime": p.drv.runtime()})
				e.obs.ProcessChanged(snap.AgentID)
				return p, nil
			}
			delete(e.procs, snap.AgentID)
			if p.drv.name() != e.driverFor(snap).name() {
				// The runtime changed: the session has been reset in the database, so the old process must not record
				// its session file back when it stops.
				p.sessionStale.Store(true)
			}
			e.mu.Unlock()
			if !alive(p) {
				// The process has exited but watch has not removed it yet: record the unexpected exit, then clean up.
				e.recordExit(p, nil)
				e.stop(p, stopCause{})
			} else {
				// The config changed: stop the old process, then restart with the new config.
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
			victim.log.Info("process limit reached; closing the least recently used idle " + victim.drv.name() + " process")
			e.stop(victim, stopCause{reason: stopEvicted, forAgent: &snap.AgentID})
			continue
		}
		ch := make(chan struct{}, 1)
		if waitingReported {
			e.waiters = append([]chan struct{}{ch}, e.waiters...) // woken but still got no slot: keep its place in line
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
			return nil, errors.New("the executor is closed")
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
	case <-p.rt.Done():
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
	// Woken up but gave the slot up: pass it on to the next waiter.
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

// launch prepares the config and starts the Pi process; startReason is no_process or config_changed.
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
			result, err = nil, fail(executor.ErrCredentialUnavailable, "Release of the Lark user runtime channel was not confirmed.")
		}
	}()
	prep, err := e.prepare(ctx, snap, d, runID, shim)
	if err != nil {
		return nil, err
	}
	sink.Phase(executor.PhaseLaunching)
	sessionFile := ""
	if snap.Session.State == "active" && snap.Session.SessionFile != "" {
		// Given a session file path that does not exist, Pi silently creates a new session, so we must check first.
		if _, err := os.Stat(snap.Session.SessionFile); err != nil {
			if mErr := e.src.MarkSessionMissing(ctx, snap.AgentID, &runID); mErr != nil {
				return nil, mErr
			}
			return nil, fail(executor.ErrSessionMissing, "The recorded session file %s no longer exists. Confirm reopening the session in the console first.", snap.Session.SessionFile)
		}
		sessionFile = snap.Session.SessionFile
	}
	env := append(slices.Clone(prep.env), larkEnv...)
	var tokenID uuid.UUID
	if e.o.Tokens != nil {
		id, tok, err := e.o.Tokens.IssueAgentToken(ctx, snap.AgentID)
		if err != nil {
			return nil, fail(executor.ErrInternal, "Could not issue the Agent token: %v", err)
		}
		tokenID = id
		env = append(slices.Clone(env), "LOOPCTL_AGENT_URL="+e.o.AgentURL, "LOOPCTL_AGENT_TOKEN="+tok)
	}
	var googleBridge *cliauth.Broker
	if snap.GoogleBinding != nil {
		if !filepath.IsAbs(e.o.GoogleRuntimeEntry) {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "The Google refresh channel is not configured.")
		}
		googleBridge, err = cliauth.NewBroker(*snap.GoogleBinding, e.o.GoogleResolver)
		if err != nil {
			e.revokeToken(tokenID, e.snapshotLog(snap))
			return nil, fail(executor.ErrCredentialUnavailable, "The Google refresh channel is not available.")
		}
		for key, value := range googleBridge.Environment() {
			env = append(env, key+"="+value)
		}
		env = append(env, "LOOP_NOW_GCLOUD_RUNTIME_ENTRY="+e.o.GoogleRuntimeEntry)
	}
	drv := e.driverFor(snap)
	spec := startSpec{snap: snap, d: d, prep: prep, env: env, sessionFile: sessionFile}
	begin := time.Now()
	st, err := drv.start(ctx, spec)
	if err != nil {
		if googleBridge != nil {
			closeGoogleBroker(googleBridge)
		}
		e.revokeToken(tokenID, e.snapshotLog(snap))
		return nil, err
	}
	client := st.rt
	p := &proc{larkRuntime: lark, googleBridge: googleBridge, authBridge: st.authBridge, log: e.snapshotLog(snap), agentID: snap.AgentID, rt: client, client: st.client, drv: drv, runtimeIdentity: runtimeIdentity(drv, snap),
		runtimeBinary: st.binary, runtimeVersion: st.version, sessionID: st.sessionID, version: snap.ConfigVersion, promptIdentity: prompts.Identity(snap.PromptLayers), localIdentity: snap.Local.Identity(), stateIdentity: snap.ProviderState.Identity(), credentialIdentity: snap.CredentialIdentity, modelConfigIdentity: snap.ModelConfigIdentity, provider: snap.Provider, model: snap.ModelID,
		startedAt: begin, dirs: d, lastUsed: time.Now(), recorded: snap.Session.SessionFile, tokenID: tokenID, stopDone: make(chan struct{})}
	if identity, identityErr := readRecoveryProcess(client.Pid()); identityErr == nil && validRecoveryProcess(&identity) {
		p.recoveryIdentity = &identity
	}
	if snap.LarkBinding != nil {
		binding := *snap.LarkBinding
		p.larkBinding = &binding
	}
	if snap.ProviderState != nil {
		p.stateVersion = snap.ProviderState.VersionID
	}
	attached = true
	e.mu.Lock()
	e.live[p] = struct{}{}
	e.mu.Unlock()
	drv.attach(p)
	e.writePidFile(p)
	if err := drv.ready(ctx, p, spec); err != nil {
		e.stop(p, stopCause{})
		return nil, err
	}
	startup := time.Since(begin)
	e.event(snap.AgentID, &runID, store.EventPiStarted, map[string]any{
		"pid": client.Pid(), "configVersion": snap.ConfigVersion, "startupMs": startup.Milliseconds(), "reason": startReason, "runtime": drv.runtime()})
	e.snapshotLog(snap).Info(drv.name()+" process started", "runId", runID, "pid", client.Pid(), "startup", startup, "reason", startReason)
	return p, nil
}

// runtimeIdentity is the runtime fingerprint compared when reusing a process: the runtime name plus its own
// fingerprint.
func runtimeIdentity(drv driver, snap agentconfig.Snapshot) string {
	return drv.name() + "\x00" + drv.identity(snap)
}

// exiting reports whether the process is already exiting (Claude Code, sent SIGINT on interrupt): such a process must
// not be given new messages.
func exiting(p *proc) bool {
	x, ok := p.rt.(interface{ Exiting() bool })
	return ok && x.Exiting()
}

// retire removes a process that is exiting at the platform's request from the process table and finishes it off; its
// exit is not unexpected, and no exit event is recorded.
func (e *Executor) retire(p *proc) {
	p.stopping.Store(true)
	e.mu.Lock()
	if e.procs[p.agentID] == p {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	go e.stop(p, stopCause{})
}

// discard removes an exited process from the process table, records the unexpected exit, and makes sure its process
// group ends.
func (e *Executor) discard(p *proc, runID *uuid.UUID) {
	p.exitRun.Store(runID)
	e.mu.Lock()
	if e.procs[p.agentID] == p {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	go func() {
		if !p.stopping.Load() { // ended on purpose by the platform (e.g. interrupt timeout): not an unexpected exit
			e.recordExit(p, runID)
		}
		e.stop(p, stopCause{})
	}()
}

// recordExit waits for the process to end, then records one unexpected exit (at most once per process).
func (e *Executor) recordExit(p *proc, runID *uuid.UUID) {
	p.exitOnce.Do(func() {
		select {
		case <-p.rt.Done():
		case <-time.After(e.o.StopGrace):
		}
		exit := "the process has not exited yet"
		if !alive(p) {
			exit = "exit status 0"
			if err := p.rt.ExitErr(); err != nil {
				exit = err.Error()
			}
		}
		stderr := tail(p.rt.Stderr(), e.o.StderrTailBytes)
		e.event(p.agentID, runID, store.EventPiExited, map[string]any{
			"pid": p.rt.Pid(), "uptimeMs": time.Since(p.startedAt).Milliseconds(), "exit": exit, "stderrTail": stderr, "runtime": p.drv.runtime()})
		p.log.Warn(p.drv.name()+" process exited unexpectedly", "runId", runID, "pid", p.rt.Pid(), "exit", exit)
		e.obs.ProcessChanged(p.agentID)
	})
}

// turnFinished records the session file after a turn ends, marks the process idle and starts the idle timer.
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

// recordSession records the path only once the session file has been written to disk.
func (e *Executor) recordSession(p *proc) error {
	if p.sessionStale.Load() {
		return nil
	}
	p.sessMu.Lock()
	defer p.sessMu.Unlock()
	if p.sessionFile == "" && p.sessionUntracked.Load() {
		return nil
	}
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
		p.log.Error("failed to record the session file", "runId", p.curRun.Load(), "code", "session_record_failed")
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

// watch removes the process from the table when it exits unexpectedly, records the exit, and gives up the slot.
func (e *Executor) watch(p *proc) {
	<-p.rt.Done()
	e.revokeProcToken(p)
	e.mu.Lock()
	removed := e.procs[p.agentID] == p
	if removed {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	if !p.stopping.Load() {
		// Read curRun first: discard sets exitRun before clearing curRun, so at least one of them matches.
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

// stop ends the process group by closing stdin, then SIGTERM, then SIGKILL, moving to the next step only if the
// previous one did not work; when the reason is not empty, it records one stop event after the process has ended.
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
		begin := time.Now() // the stop event is recorded at the time stopping began, not after the process ended
		p.stopping.Store(true)
		e.revokeProcToken(p) // revoke the token first: once the process starts stopping it must not call the Agent API any more
		if p.revokeErr != nil {
			p.stopErr = ErrRuntimeStopUnconfirmed
		}
		if p.idleTimer != nil {
			p.idleTimer.Stop()
		}
		e.recordSession(p)
		_ = p.rt.CloseStdin()
		if !waitDone(p, e.o.StopGrace) {
			_ = procgroup.Terminate(p.rt.Pid())
			if !waitDone(p, e.o.StopGrace) {
				_ = procgroup.Kill(p.rt.Pid())
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

// revokeProcToken revokes the process's Agent token, at most once per process.
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
		log.Error("failed to revoke the Agent token", "tokenId", tokenID, "err", err)
		return ErrRuntimeStopUnconfirmed
	}
	return nil
}

// recordStop records one stop of a process by the platform, timed at the moment stopping began.
func (e *Executor) recordStop(p *proc, why stopCause, at time.Time) {
	data := map[string]any{"pid": p.rt.Pid(), "reason": why.reason, "uptimeMs": at.Sub(p.startedAt).Milliseconds(), "runtime": p.drv.runtime()}
	if why.forAgent != nil {
		data["forAgentId"] = why.forAgent.String()
	}
	e.obs.Event(p.agentID, why.runID, executor.Event{Type: store.EventPiStopped, At: at, Data: data})
	p.log.Info(p.drv.name()+" process stopped", "runId", why.runID, "pid", p.rt.Pid(), "reason", why.reason)
}

func waitDone(p *proc, d time.Duration) bool {
	select {
	case <-p.rt.Done():
		return true
	case <-time.After(d):
		return false
	}
}

// procObserver passes the usage and events Pi reports to the executor's observer: those during a turn are tied to the
// current run, the rest only to the Agent.
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
		// Pi's automatic compaction uses the session's current model; the platform sets the model at startup and does
		// not switch it midway.
		eu.Provider, eu.Model = o.p.provider, o.p.model
	}
	if eu.Provider == "" || eu.Model == "" {
		eu.Provider, eu.Model = "", ""
	}
	o.e.obs.Usage(o.p.agentID, o.runID(inTurn), eu)
}

// dialog hands a dialog to Options.Dialogs, limited to the turn that sent it: once this turn ends (the process moves on
// to another run) it no longer waits for the answer.
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

// ---- Interrupt / Release / state / shutdown ----

// Interrupt implements executor.Executor: it works asynchronously and returns at once. It only interrupts the run whose
// ID is runID.
func (e *Executor) Interrupt(_ context.Context, agentID, runID uuid.UUID) error {
	e.mu.Lock()
	r := e.runs[agentID]
	e.mu.Unlock()
	if r == nil || r.id != runID {
		return nil // no run, or the current run is a different one: a late interrupt does not apply to it
	}
	r.mu.Lock()
	if r.interrupted.Swap(true) {
		r.mu.Unlock()
		return nil // already being interrupted
	}
	p, prompting := r.proc, r.prompting
	r.mu.Unlock()
	if !prompting {
		r.cancel() // still waiting for a slot or preparing: just give up
		return nil
	}
	go e.abort(r, p)
	return nil
}

// abort first clears Pi's internal queue, then aborts the current run; it keeps trying within the interrupt timeout and
// force-kills the process after the timeout (the session file is kept).
func (e *Executor) abort(r *run, p *proc) {
	deadline := time.Now().Add(e.o.AbortTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		err := p.rt.Abort(ctx)
		cancel()
		if err == nil {
			select {
			case <-r.done:
				return
			case <-time.After(300 * time.Millisecond):
				// The Prompt may just not have been sent yet; try again.
				continue
			}
		}
		select {
		case <-r.done:
			return
		default:
		}
		if errors.Is(err, runturn.ErrExited) {
			return
		}
	}
	select {
	case <-r.done:
		return
	default:
	}
	p.log.Warn("interrupt timed out; force-ending the "+p.drv.name()+" process", "runId", r.id)
	e.mu.Lock()
	if e.procs[p.agentID] == p {
		delete(e.procs, p.agentID)
	}
	e.mu.Unlock()
	// Record the stop before force-killing: after the kill the run may end first with a process exit and clean up the
	// process, so the stop event cannot rely on a later stop call.
	e.recordStop(p, stopCause{reason: stopAbortTimeout, runID: &r.id}, time.Now())
	p.stopping.Store(true)
	_ = procgroup.Kill(p.rt.Pid())
	e.stop(p, stopCause{})
}

// Release implements executor.Executor: it stops the process and deletes the separate HOME (the workspace and the
// session files are kept).
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
	// Remove the subscription account links first, so deleting the directory afterwards cannot reach the account's
	// state directory through a link.
	if err := clearProviderLinks(e.dirs(agentID)); err != nil {
		return err
	}
	// The session records Claude Code keeps under ~/.claude for this workspace: first archive them into the Agent's own
	// sessions directory, then delete them with claude purge. A failure is only logged and does not block deletion and
	// cleanup.
	e.purgeClaude(ctx, agentID)
	return releaseHome(e.o.WorkRoot, agentID)
}

// ProviderStatesInUse returns the plugin provider state versions used by running Pi processes (the sweeper does not
// delete their directories).
func (e *Executor) ProviderStatesInUse() map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	e.mu.Lock()
	defer e.mu.Unlock()
	for p := range e.live {
		if p.stateVersion != uuid.Nil {
			out[p.stateVersion] = true
		}
	}
	return out
}

// beforeRun calls the intercepting Hook run.before_start (plugins/SPEC.md §4): when it blocks, this turn fails with
// plugin_blocked and Pi is not started.
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
		reason = "No reason given."
	}
	return true, "Plugin " + o.Plugin + " blocked this turn: " + reason, nil
}

// FreezeForFork is called by the task preparer that holds the regular dispatch gate: it stops the idle Pi once the
// current turn ends, so the source session is stable. It is the same thing as the platform commands' StopIdle, only
// with the stop reason fork_snapshot. While another maintenance operation is running, the error wraps ErrRuntimeBusy;
// when the current turn has not ended yet, it is “常态轮次尚未完全结束” ("the regular turn has not fully finished").
func (e *Executor) FreezeForFork(ctx context.Context, agentID uuid.UUID) error {
	_, err := e.stopIdle(ctx, agentID, stopForkSnapshot)
	return err
}

// Process implements executor.Executor.
func (e *Executor) Process(agentID uuid.UUID) executor.ProcessInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p := e.procs[agentID]; p != nil {
		started := p.startedAt
		info := executor.ProcessInfo{State: executor.ProcessIdle, PID: p.rt.Pid(), StartedAt: &started}
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

// Shutdown implements executor.Executor: it ends all processes.
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

// ---- Cleanup of leftover processes and deleted Agents ----

type pidRecord struct {
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"startedAt"`
	InstanceID string    `json:"instanceId"`
	Hostname   string    `json:"hostname"`
	SessionDir string    `json:"sessionDir"`
	// ProcessStart is the process start time recorded by the kernel (see processStart); empty when the platform does
	// not support it.
	ProcessStart string `json:"processStart,omitempty"`
}

func (e *Executor) writePidFile(p *proc) {
	start, _ := processStart(p.rt.Pid())
	e.mu.Lock()
	defer e.mu.Unlock()
	b, _ := json.Marshal(pidRecord{PID: p.rt.Pid(), StartedAt: p.startedAt, InstanceID: e.o.InstanceID.String(), Hostname: e.o.Hostname, SessionDir: p.dirs.sessions, ProcessStart: start})
	if err := os.WriteFile(p.dirs.pidFile, b, 0o600); err != nil {
		p.log.Warn("failed to write pi.pid", "err", err)
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
	if json.Unmarshal(raw, &record) != nil || record.PID != p.rt.Pid() || !record.StartedAt.Equal(p.startedAt) || record.InstanceID != e.o.InstanceID.String() {
		return
	}
	_ = os.Remove(p.dirs.pidFile)
}

// CleanupStale runs at server startup: it ends Pi processes left over from the previous run that are still alive, and
// deletes the separate HOME of deleted Agents.
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
			if isOurPi(rec) {
				e.log.Warn("ending a Pi process left over from the previous server run", "pid", rec.PID, "pidFile", f)
				_ = procgroup.Terminate(rec.PID)
				for i := 0; i < 20 && processAlive(rec.PID); i++ {
					time.Sleep(100 * time.Millisecond)
				}
				if processAlive(rec.PID) {
					_ = procgroup.Kill(rec.PID)
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
		_ = clearProviderLinks(e.dirs(id))
		_ = os.RemoveAll(e.dirs(id).home)
	}
	return nil
}

func processAlive(pid int) bool { return procgroup.ProcessAlive(pid) }

// isOurPi confirms that the PID in the record is still the Pi process originally started, so that another process that
// reused the PID is not killed by mistake. When a start time was recorded it compares that (Pi changes its own process
// title, so the command line no longer contains --mode rpc and the session directory); when none was recorded (written
// by an old version, or the platform does not support it) it falls back to comparing the command line.
func isOurPi(rec pidRecord) bool {
	if !processAlive(rec.PID) {
		return false
	}
	if rec.ProcessStart != "" {
		start, err := processStart(rec.PID)
		return err == nil && start == rec.ProcessStart
	}
	cmd, err := processCommandLine(rec.PID)
	if err != nil {
		return false
	}
	return strings.Contains(cmd, "--mode rpc") && rec.SessionDir != "" && strings.Contains(cmd, rec.SessionDir)
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
