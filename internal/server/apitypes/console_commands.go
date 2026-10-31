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
}

// ConsoleCommandResult 是平台或插件认领的控制台命令结果。
type ConsoleCommandResult struct {
	Session              *ConsoleSessionStatus  `json:"session"`
	CopyText             *string                `json:"copyText"`
	ExportFormat         *string                `json:"exportFormat"`
	ProcessStopped       *bool                  `json:"processStopped"`
	ResetSession         *SessionInfo           `json:"resetSession"`
	ResumeSessions       []ConsoleResumeSession `json:"resumeSessions"`
	ResumedSession       *SessionInfo           `json:"resumedSession"`
	TokensBefore         *int64                 `json:"tokensBefore"`
	EstimatedTokensAfter *int64                 `json:"estimatedTokensAfter"`
	Reply                *string                `json:"reply"`
	Handled              bool                   `json:"handled"`
	Source               string                 `json:"source"`
	Plugin               string                 `json:"plugin"`
	Command              string                 `json:"command"`
}
