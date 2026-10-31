// Package executor 定义执行器接口。对话协调只依赖这里，不依赖本机执行器的细节，
// 以后加入 E2B 执行器时实现同一接口即可。
package executor

import (
	"context"
	"loop-now/internal/server/messageimage"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// 执行结果。
const (
	OutcomeCompleted   = "completed"
	OutcomeFailed      = "failed"
	OutcomeInterrupted = "interrupted"
)

// 执行阶段（执行状态为 starting 时有效），与 OpenAPI 的 RunPhase 一致。
const (
	PhaseWaitingSlot = "waiting_slot"
	PhasePreparing   = "preparing"
	PhaseLaunching   = "launching"
)

// 失败原因，与 OpenAPI 的 RunErrorCode 一致。
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
)

// 进程状态，与 OpenAPI 的 ProcessState 一致。
const (
	ProcessStopped  = "stopped"
	ProcessStarting = "starting"
	ProcessIdle     = "idle"
	ProcessBusy     = "busy"
)

// 工具状态。
const (
	ToolStarted   = "started"
	ToolSucceeded = "succeeded"
	ToolFailed    = "failed"
)

// OriginConsoleCompact 是控制台手动压缩；它使用真实运行，但不产生对话消息。
const OriginConsoleCompact = "console_compact"

// CompactionResult 是一次手动压缩的 Token 变化。
type CompactionResult struct {
	TokensBefore         int64
	EstimatedTokensAfter int64
}

// SessionExport 是已读取并验证来源的会话下载内容。
type SessionExport struct {
	Filename    string
	ContentType string
	Data        []byte
}

// IdleProcessStopper 由支持维护操作的本机执行器实现。
type IdleProcessStopper interface {
	StopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error)
}

// SessionExporter 由能从受管 Pi 会话导出的执行器实现。
type SessionExporter interface {
	ExportSession(ctx context.Context, agentID uuid.UUID, format string) (SessionExport, error)
}

// ManagedSession 是受管 sessions 目录里的一个有效 Pi 会话；Path 只在服务端内部使用。
type ManagedSession struct {
	ID        string
	Path      string
	Title     string
	Preview   string
	UpdatedAt time.Time
	Current   bool
}

// SessionLister 枚举可恢复的 Pi 会话，不得因此启动进程。
type SessionLister interface {
	ListSessions(ctx context.Context, agentID uuid.UUID, currentFile string) ([]ManagedSession, error)
}

// Request 是一次执行的输入。
type Request struct {
	Images  []messageimage.Content
	AgentID uuid.UUID
	RunID   uuid.UUID
	Message string
	// Origin 与 MessageID 交给插件的 run.before_start：触发这一轮的来源与消息（平台提醒为那条 Inbox 消息）。
	Origin    string
	MessageID uuid.UUID
}

// Result 是一次执行的结果。
type Result struct {
	Outcome      string
	Text         string // 最终回复文字（失败或打断时为已产生的部分）
	ErrorCode    string
	ErrorMessage string
	Compaction   *CompactionResult // 仅 console_compact 成功时有值
	// ConfigVersion 是这次执行实际使用的配置版本。
	ConfigVersion int64
}

// Sink 接收执行过程中的事件。回调可能来自执行器内部的协程，实现须尽快返回。
type Sink interface {
	// Phase 报告启动阶段的变化。
	Phase(phase string)
	// Running 表示消息已交给 Pi，开始回复。
	Running(configVersion int64)
	TextDelta(delta string)
	Tool(toolCallID, toolName, state string)
}

// Executor 执行 Agent 的消息。
type Executor interface {
	// Execute 处理一条消息直到这一轮结束；按需安装 CLI、同步配置、启动或重启进程，
	// 受进程数上限约束时会先等待名额。只有基础设施故障才返回 error，
	// 可预期的失败（凭证不可用、克隆失败等）以 Result.Outcome = failed 返回。
	Execute(ctx context.Context, req Request, sink Sink) (Result, error)
	// Interrupt 打断该 Agent 中 ID 为 runID 的那一次执行；执行尚未开始时让它直接放弃。
	// 该 Agent 当前没有执行，或当前执行不是 runID 时什么也不做：迟到的打断不能落到下一次执行上。
	Interrupt(ctx context.Context, agentID, runID uuid.UUID) error
	// Release 停止进程并删除独立 HOME（删除 Agent 时调用）。
	Release(ctx context.Context, agentID uuid.UUID) error
	// Process 返回该 Agent 的进程状态，进程存活时带进程号与启动时间。
	Process(agentID uuid.UUID) ProcessInfo
	// Shutdown 结束全部进程。
	Shutdown(ctx context.Context) error
}

// ProcessInfo 是 Agent 的进程状态。PID 与 StartedAt 只在进程存活（idle、busy）时有值。
type ProcessInfo struct {
	State     string
	PID       int
	StartedAt *time.Time
}

// Event 是执行器回报的运行记录事件，类型取值见 store 包的 Event 常量。
type Event struct {
	Type string
	At   time.Time // 为零时取记录时刻
	Data map[string]any
}

// Usage 是 Pi 报告的一次用量。Provider 与 Model 都为空表示未知模型。
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

// Observer 接收执行器的运行记录：事件、用量与进程状态变化。runID 为空表示与回复无关。
// 回调可能来自执行器内部的协程，实现须尽快返回；记录失败只写日志，不影响执行。
type Observer interface {
	Event(agentID uuid.UUID, runID *uuid.UUID, e Event)
	Usage(agentID uuid.UUID, runID *uuid.UUID, u Usage)
	ProcessChanged(agentID uuid.UUID)
}

// NopObserver 丢弃全部运行记录。
type NopObserver struct{}

func (NopObserver) Event(uuid.UUID, *uuid.UUID, Event) {}
func (NopObserver) Usage(uuid.UUID, *uuid.UUID, Usage) {}
func (NopObserver) ProcessChanged(uuid.UUID)           {}

// reservedEnv 是由执行器控制、凭证不能覆盖的环境变量。
var reservedEnv = []string{"HOME", "PATH", "TMPDIR", "USER", "LOGNAME", "PI_CODING_AGENT_DIR", "PI_OFFLINE", "GIT_TERMINAL_PROMPT"}

// reservedEnvPrefix 是 loopctl 读取的环境变量前缀。执行器注入 LOOPCTL_AGENT_URL 与 LOOPCTL_AGENT_TOKEN；
// 整个前缀都不允许由凭证或透传设置，防止把 Agent 的 loopctl 指向其他服务端。
const reservedEnvPrefix = "LOOPCTL_"

// IsReservedEnv 判断环境变量名是否由执行器保留。
func IsReservedEnv(name string) bool {
	return slices.Contains(reservedEnv, name) || strings.HasPrefix(name, reservedEnvPrefix)
}
