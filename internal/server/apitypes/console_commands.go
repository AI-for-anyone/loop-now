package apitypes

import "time"

// ConsoleResumeSession is a managed session listed by /resume; it does not expose local paths.
type ConsoleResumeSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	UpdatedAt time.Time `json:"updatedAt"`
	Current   bool      `json:"current"`
}

// ConsoleSessionStatus is the read-only snapshot for /session; it never starts Pi just to answer the query.
type ConsoleSessionStatus struct {
	Status        AgentStatus `json:"status"`
	Model         ModelRef    `json:"model"`
	ThinkingLevel string      `json:"thinkingLevel"`
	// LastCompaction is the most recent manual compaction run (in any status), null when there is none; once the
	// request that started the compaction has disconnected, the result is read from here.
	LastCompaction *Run `json:"lastCompaction"`
}

// RunCompaction holds the instructions and result of a manual compaction (/compact). Outcome is null before the run
// ends, and when it failed or was cancelled.
type RunCompaction struct {
	Instructions         string  `json:"instructions"`
	Outcome              *string `json:"outcome"`
	SkipReason           *string `json:"skipReason"`
	TokensBefore         *int64  `json:"tokensBefore"`
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
}

// ConsoleCommandResult is the result of a console command claimed by the platform or a plugin.
type ConsoleCommandResult struct {
	Session        *ConsoleSessionStatus  `json:"session"`
	CopyText       *string                `json:"copyText"`
	ExportFormat   *string                `json:"exportFormat"`
	ProcessStopped *bool                  `json:"processStopped"`
	ResetSession   *SessionInfo           `json:"resetSession"`
	ResumeSessions []ConsoleResumeSession `json:"resumeSessions"`
	ResumedSession *SessionInfo           `json:"resumedSession"`
	CompactRun     *Run                   `json:"compactRun,omitempty"`
	// Deprecated: same as CompactRun.Compaction.TokensBefore; marked deprecated in the contract, kept for old clients.
	TokensBefore *int64 `json:"tokensBefore"`
	// Deprecated: same as CompactRun.Compaction.EstimatedTokensAfter; deprecated in the contract, kept for old clients.
	EstimatedTokensAfter *int64  `json:"estimatedTokensAfter"`
	Reply                *string `json:"reply"`
	Handled              bool    `json:"handled"`
	Source               string  `json:"source"`
	Plugin               string  `json:"plugin"`
	Command              string  `json:"command"`
}
