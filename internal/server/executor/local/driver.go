package local

import (
	"context"
	"os"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
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

// retiringProcess is a runtime process that cannot take another message after a turn was interrupted (Claude Code
// exits after SIGINT). Once Exiting reports true, the executor removes it from the process table and the next message
// continues the session in a new process.
type retiringProcess interface {
	Exiting() bool
}

// driver is the part of the local executor that differs per Agent runtime: where its Skills and MCP servers go, what it
// needs to reach its model, how to start the process, what to refresh before reuse, what to do before and after each
// turn, and what to call failures. The process table, slots and eviction, idle timeout, Agent tokens, pid files,
// interrupts, task freezing and handover, and the rest of prepare (CLIs, Prompt, CLI credential files, workspace
// sources, local mode) are handled by the executor in one place.
//
// Besides these methods a driver may implement the optional interfaces below (taskForker, compactor, sessionExporter,
// sessionLister, nativeCommander, purger). The registry's capabilities must say yes exactly when the driver implements
// the matching one; a test checks this.
type driver interface {
	// runtime is the runtime's id in the registry (recorded in process events); name is its product name in logs.
	runtime() string
	name() string
	// identity is the runtime's own process fingerprint: when it differs from the live process's, restart with the new
	// config.
	identity(snap agentconfig.Snapshot) string
	// syncAssets puts the Agent's Skills (its own copies and the built-in loopctl), the granted plugins' Skills and their
	// MCP servers where this runtime reads them, and returns the Skill directories to pass when starting (none when the
	// runtime finds them by itself).
	syncAssets(ctx context.Context, snap agentconfig.Snapshot, d agentDirs) ([]string, error)
	// clearAssets removes what syncAssets wrote. The executor calls it for every runtime that is not running the Agent,
	// so a switch of runtime leaves nothing of the old one behind.
	clearAssets(d agentDirs) error
	// modelCredentials writes what this runtime needs to reach its model; loginHome is the real HOME when the Agent
	// runs with local logins, otherwise empty. Expected failures are *failure.
	modelCredentials(snap agentconfig.Snapshot, d agentDirs, loginHome string) error
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
	// sessionSaved reports whether the session a reference names (the runtime's own: a file path, a thread id) is saved
	// where the runtime keeps it. A driver that can only tell by continuing the session returns true here and reports a
	// missing session when it continues it (see startSpec.runID).
	sessionSaved(ref string) bool
}

// fileSessions is sessionSaved for runtimes whose session reference is the path of a file the runtime writes.
type fileSessions struct{}

func (fileSessions) sessionSaved(ref string) bool {
	_, err := os.Stat(ref)
	return err == nil
}

// taskForker is a driver whose task instances can start from a copy of the regular session (capability taskFork).
type taskForker interface {
	// forkBaseline reads and checks the regular session sessionFile of the Agent agentID and returns what the task
	// instance's baseline file holds; it reads at most maxBytes. The instance forks from that file on its first start.
	forkBaseline(ctx context.Context, agentID uuid.UUID, sessionFile string, maxBytes int64) ([]byte, error)
}

// compactor is a driver that compacts the current session on request (session command compact). Errors other than
// runturn.ErrExited are *failure.
type compactor interface {
	compact(ctx context.Context, p *proc, instructions string) (executor.CompactionResult, error)
}

// sessionExporter is a driver that exports the Agent's current session (session command export). The executor has
// already made sure the Agent is idle.
type sessionExporter interface {
	exportSession(ctx context.Context, agentID uuid.UUID, sessionFile, format string) (executor.SessionExport, error)
}

// sessionLister is a driver that lists and finds the Agent's sessions without starting a process (session command
// resume).
type sessionLister interface {
	listSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]executor.ManagedSession, error)
	resolveSession(ctx context.Context, agentID uuid.UUID, sessionID, currentFile string) (*executor.ManagedSession, error)
}

// nativeCommander is a driver that lists the runtime's own slash commands (capability nativeCommands). The executor
// asks once after each start and keeps the list in the Agent directory.
type nativeCommander interface {
	commands(ctx context.Context, p *proc) ([]executor.PiCommand, error)
}

// purger is a driver that keeps data outside the Agent's directories and removes it when an Agent is deleted or a task
// instance is retired. Release calls every purger, whatever runtime the Agent used last; failures are only logged.
type purger interface {
	purge(ctx context.Context, agentID uuid.UUID)
}

// startSpec is what the executor prepared for starting a runtime process.
type startSpec struct {
	snap agentconfig.Snapshot
	d    agentDirs
	prep prepared
	// env is the platform's common environment: directories, run PATH, credentials, Lark, Agent token and the Google
	// refresh channel.
	env []string
	// sessionFile is the session reference to continue (the runtime's own: a file path, a thread id); empty means a new
	// session (on a task instance's first start it forks from snap.ForkSessionFile).
	sessionFile string
	// runID is the run this start is for, for a driver that finds a missing session only when it continues it.
	runID uuid.UUID
}

// started is the process a driver started, plus the resources that come with it.
type started struct {
	rt         runtimeProcess
	authBridge *modelauth.RuntimeBridge
}

// drivers builds each runtime's driver, by runtime id. A test checks that every runtime in the registry has one.
var drivers = map[string]func(*Executor) driver{
	agentruntime.Pi:         func(e *Executor) driver { return piDriver{e: e} },
	agentruntime.ClaudeCode: func(e *Executor) driver { return claudeDriver{e: e} },
	agentruntime.Codex:      func(e *Executor) driver { return codexDriver{e: e} },
}

// driverFor returns the driver of the runtime that runs this config (decided by its model provider).
func (e *Executor) driverFor(snap agentconfig.Snapshot) driver {
	return e.driverByID(agentruntime.ForProvider(snap.Provider).ID)
}

// driverByID returns the driver of the runtime whose id is id; nil when there is none.
func (e *Executor) driverByID(id string) driver {
	if build := drivers[id]; build != nil {
		return build(e)
	}
	return nil
}

// allDrivers returns the driver of every runtime, in registry order.
func (e *Executor) allDrivers() []driver {
	var out []driver
	for _, rt := range agentruntime.All() {
		if drv := e.driverByID(rt.ID); drv != nil {
			out = append(out, drv)
		}
	}
	return out
}

// runtimeName is the product name of a runtime in the registry.
func runtimeName(id string) string {
	rt, _ := agentruntime.ByID(id)
	return rt.Name
}

// capabilities returns the registry's capabilities of the runtime that runs this config.
func capabilities(snap agentconfig.Snapshot) agentruntime.Capabilities {
	return agentruntime.ForProvider(snap.Provider).Capabilities
}
