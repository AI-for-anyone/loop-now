package apitypes

import (
	"loop-now/internal/server/sleepmodel"
	"time"

	"github.com/google/uuid"
)

// ---- 概览 ----

// ProcessExit 对应 ProcessExit。
type ProcessExit struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
}

// AgentOverview 对应 AgentOverview。
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

// 概览事件流的事件名。
const (
	EventOverviewSnapshot = "snapshot"
	EventAgentStatus      = "agent.status"
	EventAgentRemoved     = "agent.removed"
)

// SseOverviewSnapshot 对应 SseOverviewSnapshot。
type SseOverviewSnapshot struct {
	Agents []AgentOverview `json:"agents"`
}

// SseAgentStatus 对应 SseAgentStatus。
type SseAgentStatus struct {
	Agent AgentOverview `json:"agent"`
}

// SseAgentRemoved 对应 SseAgentRemoved。
type SseAgentRemoved struct {
	AgentID   uuid.UUID  `json:"agentId"`
	Scope     string     `json:"scope"`
	ProjectID *uuid.UUID `json:"projectId"`
}

// ---- 用量 ----

// UsageRow 对应 UsageRow。
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

// UsagePage 对应 UsagePage。
type UsagePage struct {
	From *time.Time `json:"from"`
	To   *time.Time `json:"to"`
	Rows []UsageRow `json:"rows"`
}

// ---- 异常与失败 ----

// 异常类别。
const (
	AnomalyRunFailed      = "run_failed"
	AnomalyRunInterrupted = "run_interrupted"
	AnomalyServerRestart  = "server_restart"
)

// Anomaly 对应 Anomaly。
type Anomaly struct {
	RunID        uuid.UUID  `json:"runId"`
	Kind         string     `json:"kind"`
	At           time.Time  `json:"at"`
	AgentID      uuid.UUID  `json:"agentId"`
	AgentName    string     `json:"agentName"`
	Scope        string     `json:"scope"`
	ProjectID    *uuid.UUID `json:"projectId"`
	ErrorCode    *string    `json:"errorCode"`
	ErrorMessage *string    `json:"errorMessage"`
	EndReason    *string    `json:"endReason"`
}

// AnomalyPage 对应 AnomalyPage。
type AnomalyPage struct {
	Items      []Anomaly `json:"items"`
	Total      int       `json:"total"`
	NextCursor *string   `json:"nextCursor"`
}

// ---- 时间线 ----

// RunTimeline 对应 RunTimeline。Entries 中每项是下面某个 Timeline* 结构，按时间排序。
type RunTimeline struct {
	Run     Run         `json:"run"`
	Entries []any       `json:"entries"`
	Usage   UsageTotals `json:"usage"`
}

// 时间线条目的类型。
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
)

// TimelineBase 是每个条目共有的字段。
type TimelineBase struct {
	Type string    `json:"type"`
	At   time.Time `json:"at"`
}

// TimelineQueuedEntry 对应 TimelineQueued。
type TimelineQueuedEntry struct {
	TimelineBase
	WhenBusy string `json:"whenBusy"`
}

// TimelinePromotedEntry 记录排队中的消息改变了排队方式（目前只有普通排队转为打断）。
type TimelinePromotedEntry struct {
	TimelineBase
	From      string     `json:"from"`
	To        string     `json:"to"`
	MessageID *uuid.UUID `json:"messageId,omitempty"`
}

// TimelineMessageCancelledEntry 对应 TimelineMessageCancelled：这一执行承载的一条 Inbox 消息被人取消。
type TimelineMessageCancelledEntry struct {
	TimelineBase
	MessageID uuid.UUID `json:"messageId"`
}

// TimelinePhaseEntry 对应 TimelinePhase。
type TimelinePhaseEntry struct {
	TimelineBase
	Phase string `json:"phase"`
}

// TimelineCLIInstallEntry 对应 TimelineCliInstall。
type TimelineCLIInstallEntry struct {
	TimelineBase
	CLI        string  `json:"cli"`
	Version    string  `json:"version"`
	Result     string  `json:"result"`
	DurationMs int64   `json:"durationMs"`
	Error      *string `json:"error"`
}

// TimelineSyncSource 对应 TimelineSyncSource。
type TimelineSyncSource struct {
	Path         string  `json:"path"`
	Kind         string  `json:"kind"`
	Outcome      string  `json:"outcome"`
	FilesWritten *int64  `json:"filesWritten"`
	FilesDeleted *int64  `json:"filesDeleted"`
	Error        *string `json:"error"`
}

// TimelineWorkspaceSyncEntry 对应 TimelineWorkspaceSync。
type TimelineWorkspaceSyncEntry struct {
	TimelineBase
	Result     string               `json:"result"`
	DurationMs int64                `json:"durationMs"`
	Error      *string              `json:"error"`
	Sources    []TimelineSyncSource `json:"sources"`
}

// TimelinePiStartedEntry 对应 TimelinePiStarted。
type TimelinePiStartedEntry struct {
	TimelineBase
	PID           int64  `json:"pid"`
	StartupMs     int64  `json:"startupMs"`
	Reason        string `json:"reason"`
	ConfigVersion int64  `json:"configVersion"`
}

// TimelinePiReusedEntry 对应 TimelinePiReused。
type TimelinePiReusedEntry struct {
	TimelineBase
	PID              int64      `json:"pid"`
	ProcessStartedAt *time.Time `json:"processStartedAt"`
}

// TimelinePiStoppedEntry 对应 TimelinePiStopped。
type TimelinePiStoppedEntry struct {
	TimelineBase
	PID      int64  `json:"pid"`
	Reason   string `json:"reason"`
	UptimeMs int64  `json:"uptimeMs"`
}

// TimelinePiExitedEntry 对应 TimelinePiExited。
type TimelinePiExitedEntry struct {
	TimelineBase
	PID        int64  `json:"pid"`
	UptimeMs   int64  `json:"uptimeMs"`
	Exit       string `json:"exit"`
	StderrTail string `json:"stderrTail"`
}

// TimelineToolEntry 对应 TimelineTool：At 为开始时间。
type TimelineToolEntry struct {
	TimelineBase
	ToolCallID string     `json:"toolCallId"`
	ToolName   string     `json:"toolName"`
	Command    *string    `json:"command,omitempty"`
	State      string     `json:"state"`
	EndedAt    *time.Time `json:"endedAt"`
	DurationMs *int64     `json:"durationMs"`
}

// TimelineModelCallEntry 对应 TimelineModelCall。
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

// TimelineRetryStartedEntry 对应 TimelineModelRetryStarted。
type TimelineRetryStartedEntry struct {
	TimelineBase
	Attempt      int64  `json:"attempt"`
	MaxAttempts  int64  `json:"maxAttempts"`
	DelayMs      int64  `json:"delayMs"`
	ErrorMessage string `json:"errorMessage"`
}

// TimelineRetryEndedEntry 对应 TimelineModelRetryEnded。
type TimelineRetryEndedEntry struct {
	TimelineBase
	Success    bool    `json:"success"`
	Attempt    int64   `json:"attempt"`
	FinalError *string `json:"finalError"`
}

// TimelineExtensionUIEntry 对应 TimelineExtensionUI。
type TimelineExtensionUIEntry struct {
	TimelineBase
	Method     string     `json:"method"`
	Message    *string    `json:"message"`
	Level      *string    `json:"level"`
	StatusKey  *string    `json:"statusKey"`
	Answered   *string    `json:"answered"`
	QuestionID *uuid.UUID `json:"questionId"`
}

// TimelineCompactionEntry 对应 TimelineCompaction。
type TimelineCompactionEntry struct {
	TimelineBase
	Reason               string  `json:"reason"`
	Result               string  `json:"result"`
	TokensBefore         *int64  `json:"tokensBefore"`
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
	ErrorMessage         *string `json:"errorMessage"`
	DurationMs           *int64  `json:"durationMs"`
}

// TimelineAbortRequestedEntry 对应 TimelineAbortRequested。
type TimelineAbortRequestedEntry struct {
	TimelineBase
	ByRunID *uuid.UUID `json:"byRunId"`
	Stop    bool       `json:"stop,omitempty"`
}

// TimelineEndedEntry 对应 TimelineEnded。
type TimelineEndedEntry struct {
	TimelineBase
	Status    string  `json:"status"`
	EndReason *string `json:"endReason"`
	ErrorCode *string `json:"errorCode"`
}

// TimelineSessionMissingEntry 对应 TimelineSessionMissing。
type TimelineSessionMissingEntry struct {
	TimelineBase
	SessionFile *string `json:"sessionFile"`
}

// TimelineSleepEntry exposes the persistent sleep transition, never its internal scheduling fields.
type TimelineSleepEntry struct {
	TimelineBase
	Sleep sleepmodel.Record `json:"sleep"`
}
