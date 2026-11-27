package apitypes

import (
	"loop-now/internal/server/sleepmodel"
	"time"

	"github.com/google/uuid"
)

// ---- Overview ----

// ProcessExit mirrors the ProcessExit schema.
type ProcessExit struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// AgentOverview mirrors the AgentOverview schema.
type AgentOverview struct {
	AgentID         uuid.UUID    `json:"agentId"`
	Scope           string       `json:"scope"`
	ProjectID       *uuid.UUID   `json:"projectId"`
	Name            string       `json:"name"`
	Model           ModelRef     `json:"model"`
	Status          AgentStatus  `json:"status"`
	LastRun         *Run         `json:"lastRun"`
	LastProcessExit *ProcessExit `json:"lastProcessExit"`
}

// Event names of the overview event stream.
const (
	EventOverviewSnapshot = "snapshot"
	EventAgentStatus      = "agent.status"
	EventAgentRemoved     = "agent.removed"
)

// SseOverviewSnapshot mirrors the SseOverviewSnapshot schema.
type SseOverviewSnapshot struct {
	Agents []AgentOverview `json:"agents"`
}

// SseAgentStatus mirrors the SseAgentStatus schema.
type SseAgentStatus struct {
	Agent AgentOverview `json:"agent"`
}

// SseAgentRemoved mirrors the SseAgentRemoved schema.
type SseAgentRemoved struct {
	AgentID   uuid.UUID  `json:"agentId"`
	Scope     string     `json:"scope"`
	ProjectID *uuid.UUID `json:"projectId"`
}

// ---- Usage ----

// UsageRow mirrors the UsageRow schema.
type UsageRow struct {
	AgentID      uuid.UUID  `json:"agentId"`
	AgentName    string     `json:"agentName"`
	AgentDeleted bool       `json:"agentDeleted"`
	Scope        string     `json:"scope"`
	ProjectID    *uuid.UUID `json:"projectId"`
	Provider     *string    `json:"provider"`
	Model        *string    `json:"model"`
	Calls        int64      `json:"calls"`
	Input        int64      `json:"input"`
	Output       int64      `json:"output"`
	CacheRead    int64      `json:"cacheRead"`
	CacheWrite   int64      `json:"cacheWrite"`
	Total        int64      `json:"total"`
}

// UsagePage mirrors the UsagePage schema.
type UsagePage struct {
	From *time.Time `json:"from"`
	To   *time.Time `json:"to"`
	Rows []UsageRow `json:"rows"`
}

// ---- Anomalies and failures ----

// Anomaly kinds.
const (
	AnomalyRunFailed      = "run_failed"
	AnomalyRunInterrupted = "run_interrupted"
	AnomalyServerRestart  = "server_restart"
)

// Anomaly mirrors the Anomaly schema.
type Anomaly struct {
	RunID     uuid.UUID  `json:"runId"`
	Kind      string     `json:"kind"`
	At        time.Time  `json:"at"`
	AgentID   uuid.UUID  `json:"agentId"`
	AgentName string     `json:"agentName"`
	Scope     string     `json:"scope"`
	ProjectID *uuid.UUID `json:"projectId"`
	// Runtime is the runtime the run was dispatched to; null for records from before it was kept.
	Runtime      *string `json:"runtime"`
	ErrorCode    *string `json:"errorCode"`
	ErrorMessage *string `json:"errorMessage"`
	EndReason    *string `json:"endReason"`
	// Compaction is set only on manual compaction (/compact) runs; null otherwise.
	Compaction *RunCompaction `json:"compaction"`
}

// AnomalyPage mirrors the AnomalyPage schema.
type AnomalyPage struct {
	Items      []Anomaly `json:"items"`
	Total      int       `json:"total"`
	NextCursor *string   `json:"nextCursor"`
}

// ---- Timeline ----

// RunTimeline mirrors the RunTimeline schema. Each item in Entries is one of the Timeline* structures below, in time order.
type RunTimeline struct {
	Run     Run         `json:"run"`
	Entries []any       `json:"entries"`
	Usage   UsageTotals `json:"usage"`
}

// Timeline entry types.
const (
	TimelineQueued            = "queued"
	TimelineDispatched        = "dispatched"
	TimelinePhase             = "phase"
	TimelineCLIInstall        = "cli_install"
	TimelineWorkspaceSync     = "workspace_sync"
	TimelinePiStarted         = "pi_started"
	TimelinePiReused          = "pi_reused"
	TimelinePiStopped         = "pi_stopped"
	TimelinePiExited          = "pi_exited"
	TimelineRunning           = "running"
	TimelineTool              = "tool"
	TimelineModelCall         = "model_call"
	TimelineModelRetryStarted = "model_retry_started"
	TimelineModelRetryEnded   = "model_retry_ended"
	TimelineCompaction        = "compaction"
	TimelineAbortRequested    = "abort_requested"
	TimelineEnded             = "ended"
	TimelineSessionMissing    = "session_missing"
	TimelinePromoted          = "promoted"
	TimelineMessageCancelled  = "message_cancelled"
	TimelineExtensionUI       = "extension_ui"
	TimelineModelRateLimit    = "model_rate_limit"
	TimelineContextHandoff    = "context_handoff"
	TimelineBackgroundWait    = "background_wait_timeout"
	TimelineBackgroundStarted = "background_task_started"
	TimelineBackgroundEnded   = "background_task_ended"
)

// TimelineBase holds the fields every entry has.
type TimelineBase struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
}

// TimelineQueuedEntry mirrors the TimelineQueued schema.
type TimelineQueuedEntry struct {
	TimelineBase
	WhenBusy string `json:"whenBusy"`
}

// TimelinePromotedEntry records that a queued message changed how it is queued (currently only a regular queue turning
// into an interrupt).
type TimelinePromotedEntry struct {
	TimelineBase
	From      string     `json:"from"`
	To        string     `json:"to"`
	MessageID *uuid.UUID `json:"messageId,omitempty"`
}

// TimelineMessageCancelledEntry mirrors the TimelineMessageCancelled schema: someone cancelled an Inbox message carried
// by this run.
type TimelineMessageCancelledEntry struct {
	TimelineBase
	MessageID uuid.UUID `json:"messageId"`
}

// TimelinePhaseEntry mirrors the TimelinePhase schema.
type TimelinePhaseEntry struct {
	TimelineBase
	Phase   string `json:"phase"`
	Runtime string `json:"runtime"`
}

// TimelineCLIInstallEntry mirrors the TimelineCliInstall schema.
type TimelineCLIInstallEntry struct {
	TimelineBase
	CLI        string  `json:"cli"`
	Version    string  `json:"version"`
	Result     string  `json:"result"`
	DurationMs int64   `json:"durationMs"`
	Error      *string `json:"error"`
}

// TimelineSyncSource mirrors the TimelineSyncSource schema.
type TimelineSyncSource struct {
	Path         string  `json:"path"`
	Kind         string  `json:"kind"`
	Outcome      string  `json:"outcome"`
	FilesWritten *int64  `json:"filesWritten"`
	FilesDeleted *int64  `json:"filesDeleted"`
	Error        *string `json:"error"`
}

// TimelineWorkspaceSyncEntry mirrors the TimelineWorkspaceSync schema.
type TimelineWorkspaceSyncEntry struct {
	TimelineBase
	Result     string               `json:"result"`
	DurationMs int64                `json:"durationMs"`
	Error      *string              `json:"error"`
	Sources    []TimelineSyncSource `json:"sources"`
}

// TimelinePiStartedEntry mirrors the TimelinePiStarted schema.
type TimelinePiStartedEntry struct {
	TimelineBase
	PID           int64  `json:"pid"`
	StartupMs     int64  `json:"startupMs"`
	Reason        string `json:"reason"`
	ConfigVersion int64  `json:"configVersion"`
	Runtime       string `json:"runtime"`
}

// TimelinePiReusedEntry mirrors the TimelinePiReused schema.
type TimelinePiReusedEntry struct {
	TimelineBase
	PID              int64      `json:"pid"`
	ProcessStartedAt *time.Time `json:"processStartedAt"`
	Runtime          string     `json:"runtime"`
}

// TimelinePiStoppedEntry mirrors the TimelinePiStopped schema.
type TimelinePiStoppedEntry struct {
	TimelineBase
	PID      int64  `json:"pid"`
	Reason   string `json:"reason"`
	UptimeMs int64  `json:"uptimeMs"`
	Runtime  string `json:"runtime"`
}

// TimelinePiExitedEntry mirrors the TimelinePiExited schema.
type TimelinePiExitedEntry struct {
	TimelineBase
	PID        int64  `json:"pid"`
	UptimeMs   int64  `json:"uptimeMs"`
	Exit       string `json:"exit"`
	StderrTail string `json:"stderrTail"`
	Runtime    string `json:"runtime"`
}

// TimelineModelRateLimitEntry mirrors the TimelineModelRateLimit schema: the subscription quota status reported by
// Claude Code, verbatim.
type TimelineModelRateLimitEntry struct {
	TimelineBase
	Info map[string]any `json:"info"`
}

// TimelineToolEntry mirrors the TimelineTool schema: At is the start time.
type TimelineToolEntry struct {
	TimelineBase
	ToolCallID string     `json:"toolCallId"`
	ToolName   string     `json:"toolName"`
	Command    *string    `json:"command,omitempty"`
	Target     *string    `json:"target,omitempty"`
	State      string     `json:"state"`
	EndedAt    *time.Time `json:"endedAt"`
	DurationMs *int64     `json:"durationMs"`
}

// TimelineModelCallEntry mirrors the TimelineModelCall schema.
type TimelineModelCallEntry struct {
	TimelineBase
	Kind          string  `json:"kind"`
	Provider      *string `json:"provider"`
	Model         *string `json:"model"`
	ResponseModel *string `json:"responseModel"`
	StopReason    *string `json:"stopReason"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheRead     int64   `json:"cacheRead"`
	CacheWrite    int64   `json:"cacheWrite"`
	Reasoning     *int64  `json:"reasoning"`
	Total         int64   `json:"total"`
	DurationMs    *int64  `json:"durationMs"`
}

// TimelineRetryStartedEntry mirrors the TimelineModelRetryStarted schema.
type TimelineRetryStartedEntry struct {
	TimelineBase
	Attempt      int64  `json:"attempt"`
	MaxAttempts  int64  `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
}

// TimelineRetryEndedEntry mirrors the TimelineModelRetryEnded schema.
type TimelineRetryEndedEntry struct {
	TimelineBase
	Success    bool    `json:"success"`
	Attempt    int64   `json:"attempt"`
	FinalError *string `json:"finalError"`
}

// TimelineExtensionUIEntry mirrors the TimelineExtensionUI schema.
type TimelineExtensionUIEntry struct {
	TimelineBase
	Method     string     `json:"method"`
	Message    *string    `json:"message"`
	Level      *string    `json:"level"`
	StatusKey  *string    `json:"statusKey"`
	Answered   *string    `json:"answered"`
	QuestionID *uuid.UUID `json:"questionId"`
}

// TimelineCompactionEntry mirrors the TimelineCompaction schema.
type TimelineCompactionEntry struct {
	TimelineBase
	Reason               string  `json:"reason"`
	Result               string  `json:"result"`
	TokensBefore         *int64  `json:"tokensBefore"`
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
	ErrorMessage         *string `json:"errorMessage"`
	DurationMs           *int64  `json:"durationMs"`
}

// TimelineAbortRequestedEntry mirrors the TimelineAbortRequested schema.
type TimelineAbortRequestedEntry struct {
	TimelineBase
	ByRunID *uuid.UUID `json:"byRunId"`
	Stop    bool       `json:"stop,omitempty"`
}

// TimelineEndedEntry mirrors the TimelineEnded schema.
type TimelineEndedEntry struct {
	TimelineBase
	Status    string  `json:"status"`
	EndReason *string `json:"endReason"`
	ErrorCode *string `json:"errorCode"`
}

// TimelineSessionMissingEntry mirrors the TimelineSessionMissing schema.
type TimelineSessionMissingEntry struct {
	TimelineBase
	SessionFile *string `json:"sessionFile"`
}

// TimelineContextHandoffEntry mirrors the TimelineContextHandoff schema.
type TimelineContextHandoffEntry struct {
	TimelineBase
	ContextHandoff
}

// TimelineBackgroundWaitTimeoutEntry mirrors the TimelineBackgroundWaitTimeout schema.
type TimelineBackgroundWaitTimeoutEntry struct {
	TimelineBase
	LimitMs int64 `json:"limitMs"`
}

// TimelineBackgroundTaskStartedEntry mirrors the TimelineBackgroundTaskStarted schema.
type TimelineBackgroundTaskStartedEntry struct {
	TimelineBase
	TaskID     string  `json:"taskId"`
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	ToolCallID *string `json:"toolCallId"`
}

// TimelineBackgroundTaskEndedEntry mirrors the TimelineBackgroundTaskEnded schema.
type TimelineBackgroundTaskEndedEntry struct {
	TimelineBase
	TaskID     string  `json:"taskId"`
	Status     string  `json:"status"`
	ExitCode   *int64  `json:"exitCode"`
	StopReason *string `json:"stopReason"`
	Output     *string `json:"output"`
}

// TimelineSleepEntry exposes the persistent sleep transition, never its internal scheduling fields.
type TimelineSleepEntry struct {
	TimelineBase
	Sleep sleepmodel.Record `json:"sleep"`
}
