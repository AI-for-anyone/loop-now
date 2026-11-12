package apitypes

import "time"

// ConsoleResumeSession 是 /resume 列出的受管会话，不暴露本机路径。
type ConsoleResumeSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	UpdatedAt time.Time `json:"updatedAt"`
	Current   bool      `json:"current"`
}

// ConsoleSessionStatus 是 /session 的只读快照，不会为查询启动 Pi。
type ConsoleSessionStatus struct {
	Status        AgentStatus `json:"status"`
	Model         ModelRef    `json:"model"`
	ThinkingLevel string      `json:"thinkingLevel"`
	// LastCompaction 是最近一次手动压缩的执行（任何状态），没有时为 null；发起压缩的请求断开后从这里读结果。
	LastCompaction *Run `json:"lastCompaction"`
}

// RunCompaction 是手动压缩（/compact）的指令与结果。Outcome 在执行结束前、失败或被取消时为 null。
type RunCompaction struct {
	Instructions         string  `json:"instructions"`
	Outcome              *string `json:"outcome"`
	SkipReason           *string `json:"skipReason"`
	TokensBefore         *int64  `json:"tokensBefore"`
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
}

// ConsoleCommandResult 是平台或插件认领的控制台命令结果。
type ConsoleCommandResult struct {
	Session        *ConsoleSessionStatus  `json:"session"`
	CopyText       *string                `json:"copyText"`
	ExportFormat   *string                `json:"exportFormat"`
	ProcessStopped *bool                  `json:"processStopped"`
	ResetSession   *SessionInfo           `json:"resetSession"`
	ResumeSessions []ConsoleResumeSession `json:"resumeSessions"`
	ResumedSession *SessionInfo           `json:"resumedSession"`
	CompactRun     *Run                   `json:"compactRun,omitempty"`
	// Deprecated: 与 CompactRun.Compaction.TokensBefore 相同，契约里已标为弃用，保留给旧客户端。
	TokensBefore *int64 `json:"tokensBefore"`
	// Deprecated: 与 CompactRun.Compaction.EstimatedTokensAfter 相同，契约里已标为弃用，保留给旧客户端。
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
	Reply                *string `json:"reply"`
	Handled              bool    `json:"handled"`
	Source               string  `json:"source"`
	Plugin               string  `json:"plugin"`
	Command              string  `json:"command"`
}
