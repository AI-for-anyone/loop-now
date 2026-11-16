// Package executor defines the executor interface. Conversation coordination depends only on this package, not on the
// details of the local executor; an E2B executor added later only has to implement the same interface.
package executor

import (
	"context"
	"errors"
	"loop-now/internal/server/messageimage"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Run outcomes.
const (
	OutcomeCompleted   = "completed"
	OutcomeFailed      = "failed"
	OutcomeInterrupted = "interrupted"
)

// Run phases (valid while the run state is starting), matching RunPhase in the OpenAPI spec.
const (
	PhaseWaitingSlot = "waiting_slot"
	PhasePreparing   = "preparing"
	PhaseLaunching   = "launching"
)

// Failure reasons, matching RunErrorCode in the OpenAPI spec.
const (
	ErrCredentialUnavailable = "credential_unavailable"
	ErrCLIInstallFailed      = "cli_install_failed"
	ErrWorkspaceSyncFailed   = "workspace_sync_failed"
	ErrSessionMissing        = "session_missing"
	ErrPiStartFailed         = "pi_start_failed"
	ErrPiExited              = "pi_exited"
	ErrPiError               = "pi_error"
	ErrInternal              = "internal_error"
	ErrImageUnsupported      = "image_unsupported"
	ErrPluginBlocked         = "plugin_blocked"
	// The following are failures of runtimes other than Pi (Claude Code); Pi still uses pi_*.
	ErrRuntimeUnavailable   = "runtime_unavailable"
	ErrRuntimeLoginRequired = "runtime_login_required"
	ErrRuntimeStartFailed   = "runtime_start_failed"
	ErrRuntimeExited        = "runtime_exited"
	ErrRuntimeError         = "runtime_error"
)

// Process states, matching ProcessState in the OpenAPI spec.
const (
	ProcessStopped  = "stopped"
	ProcessStarting = "starting"
	ProcessIdle     = "idle"
	ProcessBusy     = "busy"
)

// Tool states.
const (
	ToolStarted   = "started"
	ToolSucceeded = "succeeded"
	ToolFailed    = "failed"
)

// OriginConsoleCompact is a manual compaction from the console; it uses a real run but produces no conversation
// message.
const OriginConsoleCompact = "console_compact"

// CompactionResult is the result of one manual compaction. A non-empty Skipped means Pi did not compact (the session is
// too short, was already compacted, or an extension cancelled it); this is still a normal completion and all token
// counts are 0. Otherwise the fields hold the token counts before and after compaction.
type CompactionResult struct {
	TokensBefore         int64
	EstimatedTokensAfter int64
	Skipped              string
}

// SessionExport is session download content that has been read and whose origin has been verified.
type SessionExport struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Expected failures of platform commands (maintenance operations). The executor wraps them in the error it returns, and
// the conversation service turns them into a 409.
var (
	// ErrMaintenanceBusy: the runtime still has a run in progress, or is doing another maintenance operation (such as a
	// task fork or a runtime handover).
	ErrMaintenanceBusy = errors.New("the runtime is busy")
	// ErrNoSession: there is no session to operate on.
	ErrNoSession = errors.New("there is no session to operate on")
	// ErrUnsupportedRuntime: this Agent's runtime does not support the command (for example, Claude Code has no Pi
	// session).
	ErrUnsupportedRuntime = errors.New("the current runtime does not support this command")
)

// IdleProcessStopper is implemented by local executors that support maintenance operations.
type IdleProcessStopper interface {
	StopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error)
}

// SessionExporter is implemented by executors that can export from a managed Pi session. sessionFile is the current
// session file recorded in the database; the caller has already confirmed that this is a Pi Agent and that the session
// is available.
type SessionExporter interface {
	ExportSession(ctx context.Context, agentID uuid.UUID, sessionFile, format string) (SessionExport, error)
}

// ManagedSession is a valid Pi session in the managed sessions directory; Path is used only inside the server.
type ManagedSession struct {
	ID        string
	Path      string
	Title     string
	Preview   string
	UpdatedAt time.Time
	Current   bool
}

// SessionLister enumerates resumable Pi sessions and must not start a process to do so. ResolveSession finds a session
// by its full ID (reading only the file header) and returns nil when there is none or the ID is ambiguous.
type SessionLister interface {
	ListSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]ManagedSession, error)
	ResolveSession(ctx context.Context, agentID uuid.UUID, sessionID, currentFile string) (*ManagedSession, error)
}

// Request is the input of one run.
type Request struct {
	Images  []messageimage.Content
	AgentID uuid.UUID
	RunID   uuid.UUID
	Message string
	// Origin and MessageID are passed to plugins' run.before_start: the source and the message that triggered this turn
	// (for a platform reminder, that Inbox message).
	Origin    string
	MessageID uuid.UUID
}

// RunIDLine is the line the platform puts at the very start of every message handed to Pi: the run ID of this turn
// (the Agent must pass it when it calls loopctl). In old sessions this line is in Chinese,
// “本轮运行ID：<ID>。” ("Run ID for this turn: <ID>."); that is not migrated, and the platform's production code does
// not parse this line.
func RunIDLine(id uuid.UUID) string { return "Run ID for this turn: " + id.String() + ".\n" }

// Result is the result of one run.
type Result struct {
	Outcome      string
	Text         string // final reply text (on failure or interrupt, the part produced so far)
	ErrorCode    string
	ErrorMessage string
	Compaction   *CompactionResult // set only when console_compact succeeds
	// ConfigVersion is the config version this run actually used.
	ConfigVersion int64
	// CommandHandled means Pi handled this message itself (an extension command): it was not passed to the model and
	// there is no reply.
	CommandHandled bool
	// CommandNotices are the messages (the command's output) that extensions gave with ctx.ui.notify while the command
	// ran, in order, when CommandHandled is set; CommandNoticesTruncated means they were cut off after exceeding the
	// limit (50 notices, 16 KiB in total).
	CommandNotices          []CommandNotice
	CommandNoticesTruncated bool
}

// CommandNotice is one notification an extension gave while a Pi command ran (the extension's original text, plain
// text, with newlines and spaces preserved).
type CommandNotice struct {
	Level   string `json:"level"` // info / warning / error
	Message string `json:"message"`
}

// Sink receives events during a run. Callbacks may come from goroutines inside the executor; implementations must
// return quickly.
type Sink interface {
	// Phase reports a change of startup phase.
	Phase(phase string)
	// Running means the message has been handed to Pi and the reply has started.
	Running(configVersion int64)
	TextDelta(delta string)
	Tool(toolCallID, toolName, state string)
}

// Executor runs Agent messages.
type Executor interface {
	// Execute handles one message until the turn ends. It installs the CLI, syncs the config and starts or restarts the
	// process as needed, and first waits for a slot when the process limit applies. It returns an error only for
	// infrastructure failures; expected failures (credential unavailable, clone failed, etc.) are returned with
	// Result.Outcome = failed.
	Execute(ctx context.Context, req Request, sink Sink) (Result, error)
	// Interrupt interrupts the Agent's run whose ID is runID; if that run has not started yet, it simply gives up. If
	// the Agent has no current run, or the current run is not runID, it does nothing: a late interrupt must not land on
	// the next run.
	Interrupt(ctx context.Context, agentID, runID uuid.UUID) error
	// Release stops the process and deletes the separate HOME (called when an Agent is deleted).
	Release(ctx context.Context, agentID uuid.UUID) error
	// Process returns the Agent's process state, with the PID and start time while the process is alive.
	Process(agentID uuid.UUID) ProcessInfo
	// Shutdown ends all processes.
	Shutdown(ctx context.Context) error
}

// ProcessInfo is an Agent's process state. PID and StartedAt are set only while the process is alive (idle, busy).
type ProcessInfo struct {
	State     string
	PID       int
	StartedAt *time.Time
}

// Event is a run record event reported by the executor; for the values of Type see the Event constants in the store
// package.
type Event struct {
	Type string
	At   time.Time // zero means the time of recording
	Data map[string]any
}

// Usage is one usage report from Pi. Provider and Model both empty means an unknown model.
type Usage struct {
	Kind          string // assistant / tool / compaction / cache_warm / other
	Provider      string
	Model         string
	ResponseModel string
	StopReason    string
	Input         int64
	Output        int64
	CacheRead     int64
	CacheWrite    int64
	Reasoning     *int64
	DurationMs    *int64
}

// Observer receives the executor's run records: events, usage and process state changes. A nil runID means the record
// is not tied to a reply. Callbacks may come from goroutines inside the executor, so implementations must return
// quickly; a recording failure is only logged and does not affect the run.
type Observer interface {
	Event(agentID uuid.UUID, runID *uuid.UUID, e Event)
	Usage(agentID uuid.UUID, runID *uuid.UUID, u Usage)
	ProcessChanged(agentID uuid.UUID)
}

// NopObserver discards all run records.
type NopObserver struct{}

func (NopObserver) Event(uuid.UUID, *uuid.UUID, Event) {}
func (NopObserver) Usage(uuid.UUID, *uuid.UUID, Usage) {}
func (NopObserver) ProcessChanged(uuid.UUID)           {}

// reservedEnv lists the environment variables the executor controls, which credentials cannot override.
var reservedEnv = []string{"HOME", "PATH", "TMPDIR", "USER", "LOGNAME", "PI_CODING_AGENT_DIR", "PI_OFFLINE", "GIT_TERMINAL_PROMPT"}

// reservedEnvPrefix is the prefix of the environment variables loopctl reads. The executor injects LOOPCTL_AGENT_URL
// and LOOPCTL_AGENT_TOKEN; nothing with this prefix may be set by a credential or by pass-through, so that an Agent's
// loopctl cannot be pointed at another server.
const reservedEnvPrefix = "LOOPCTL_"

// IsReservedEnv reports whether an environment variable name is reserved by the executor.
func IsReservedEnv(name string) bool {
	return slices.Contains(reservedEnv, name) || strings.HasPrefix(name, reservedEnvPrefix)
}

// PiCommand is one command Pi can run (Pi RPC get_commands): an extension command, a prompt template or a Skill command
// (name is skill:<name>).
type PiCommand struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Source      string  `json:"source"` // extension / prompt / skill
	// Path is the file the command comes from (extension file, template or Skill); it is used to tell which plugin the
	// command belongs to.
	Path string `json:"path"`
}

// PiCommands is the command list read the last time the Agent started Pi.
type PiCommands struct {
	CapturedAt time.Time   `json:"capturedAt"`
	Commands   []PiCommand `json:"commands"`
}
