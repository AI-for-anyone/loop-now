package local

import (
	"context"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor/local/pirpc"
	"loop-now/internal/server/executor/local/runturn"
	"loop-now/internal/server/messageimage"
	"loop-now/internal/server/modelauth"
)

// runtimeProcess is one runtime process managed by the executor. The process table, idle timeout, stop, interrupt, pid
// records and running a turn go only through this interface and do not care which runtime it is.
type runtimeProcess interface {
	Pid() int
	Done() <-chan struct{}
	ExitErr() error
	Stderr() string
	CloseStdin() error
	// Abort interrupts the turn in progress.
	Abort(ctx context.Context) error
	// Prompt sends one message and waits for the turn to end; when the process has exited it returns runturn.ErrExited.
	Prompt(ctx context.Context, text string, sink runturn.Sink, images ...messageimage.Content) (runturn.Result, error)
}

// driver is the part of the local executor that differs per Agent runtime: how to start the process, what to refresh
// before reuse, what to do before each turn, and what to call failures. The process table, slots and eviction, idle
// timeout, Agent tokens, pid files, interrupts, task freezing and handover, and prepare (CLIs, Skills, credential
// files, workspace sources, local mode, appended prompt) are all handled by the executor in one place.
type driver interface {
	// name is the runtime name in logs; runtime is its AgentRuntime value (recorded in process events).
	name() string
	runtime() string
	// identity is the runtime's own process fingerprint: when it differs from the live process's, restart with the new
	// config.
	identity(snap agentconfig.Snapshot) string
	// refresh rewrites the runtime's own cache files before a live process is reused; expected failures are *failure.
	refresh(snap agentconfig.Snapshot, d agentDirs) error
	// start starts the process. spec holds the common environment, directories and session to continue that the
	// executor prepared; on failure start releases what it created itself; expected failures are *failure.
	start(ctx context.Context, spec startSpec) (started, error)
	// attach is called after the process is registered with the executor and before the pid file is written: it hooks
	// up run record, dialog and other callbacks.
	attach(p *proc)
	// ready waits for the process to be ready (for example, reading the session file); on failure the executor stops
	// the process; expected failures are *failure.
	ready(ctx context.Context, p *proc, spec startSpec) error
	// beforeTurn is called before this turn's message is sent; expected failures are *failure.
	beforeTurn(ctx context.Context, p *proc, snap agentconfig.Snapshot) error
	// afterTurn is called after this turn ends and before the process becomes idle: it records the session file and
	// refines the failure reason.
	afterTurn(p *proc, tr runturn.Result) runturn.Result
	// exited is the failure code and message when the process exits during this turn; crashMessage is the final message
	// with the stderr tail added.
	exited() (code, msg string)
	crashMessage(p *proc, stderrTail string) string
}

// startSpec is what the executor prepared for starting a runtime process.
type startSpec struct {
	snap agentconfig.Snapshot
	d    agentDirs
	prep prepared
	// env is the platform's common environment: directories, run PATH, credentials, Lark, Agent token and the Google
	// refresh channel.
	env []string
	// sessionFile is the session file to continue; empty means a new session (on a task instance's first start it forks
	// from snap.ForkSessionFile).
	sessionFile string
}

// started is the process a driver started, plus the resources that come with it.
type started struct {
	rt runtimeProcess
	// client is Pi's RPC client, used only for Pi-specific operations; nil for other runtimes.
	client     *pirpc.Client
	authBridge *modelauth.RuntimeBridge
	// binary and version are the real path and version of the runtime executable (Claude Code); sessionID is the
	// session ID the platform gave it.
	binary, version, sessionID string
}

// driverFor returns the runtime that runs this config: the local Claude Code when the model provider is claude-code,
// otherwise the bundled Pi.
func (e *Executor) driverFor(snap agentconfig.Snapshot) driver {
	if isClaude(snap) {
		return claudeDriver{e: e}
	}
	return piDriver{e: e}
}
