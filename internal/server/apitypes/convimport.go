package apitypes

import (
	"time"

	"github.com/google/uuid"
)

// ConversationScanInput mirrors the ConversationScanInput schema.
type ConversationScanInput struct {
	Since *time.Time `json:"since,omitempty"`
	Path  *string    `json:"path,omitempty"`
}

// ConversationScan mirrors the ConversationScan schema.
type ConversationScan struct {
	OperationID string         `json:"operationId"`
	State       string         `json:"state"`
	Error       *string        `json:"error"`
	FilesTotal  int            `json:"filesTotal"`
	FilesDone   int            `json:"filesDone"`
	Folders     []ImportFolder `json:"folders"`
}

// ImportFolder mirrors the ImportFolder schema.
type ImportFolder struct {
	Path          string               `json:"path"`
	Git           bool                 `json:"git"`
	Repository    *ImportRepository    `json:"repository"`
	LastActiveAt  time.Time            `json:"lastActiveAt"`
	PreChecked    bool                 `json:"preChecked"`
	ProjectID     *uuid.UUID           `json:"projectId"`
	Conversations []ImportConversation `json:"conversations"`
	Truncated     bool                 `json:"truncated"`
}

// ImportRepository is ImportFolder.repository.
type ImportRepository struct {
	Remote       *string `json:"remote"`
	MainCheckout string  `json:"mainCheckout"`
}

// ImportConversation mirrors the ImportConversation schema.
type ImportConversation struct {
	CLI          string     `json:"cli"`
	SessionID    string     `json:"sessionId"`
	Cwd          string     `json:"cwd"`
	Title        string     `json:"title"`
	FirstPrompt  string     `json:"firstPrompt"`
	LastActiveAt time.Time  `json:"lastActiveAt"`
	MessageCount int        `json:"messageCount"`
	SizeBytes    int64      `json:"sizeBytes"`
	Model        *string    `json:"model"`
	AgentID      *uuid.UUID `json:"agentId"`
	SkipReason   *string    `json:"skipReason"`
}

// ConversationImportInput mirrors the ConversationImportInput schema.
type ConversationImportInput struct {
	ScanID  string                     `json:"scanId"`
	Folders []ConversationImportFolder `json:"folders"`
}

// ConversationImportFolder is one item of ConversationImportInput.folders.
type ConversationImportFolder struct {
	Path       string     `json:"path"`
	ProjectID  *uuid.UUID `json:"projectId"`
	SessionIDs []string   `json:"sessionIds"`
}

// ConversationImport mirrors the ConversationImport schema.
type ConversationImport struct {
	OperationID string          `json:"operationId"`
	State       string          `json:"state"`
	Error       *string         `json:"error"`
	Done        int             `json:"done"`
	Total       int             `json:"total"`
	Projects    []ImportProject `json:"projects"`
	Agents      []ImportedAgent `json:"agents"`
	Errors      []ImportError   `json:"errors"`
}

// ImportProject is one item of ConversationImport.projects.
type ImportProject struct {
	Path      string    `json:"path"`
	ProjectID uuid.UUID `json:"projectId"`
}

// ImportedAgent is one item of ConversationImport.agents.
type ImportedAgent struct {
	SessionID string    `json:"sessionId"`
	AgentID   uuid.UUID `json:"agentId"`
}

// ImportError is one item of ConversationImport.errors.
type ImportError struct {
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

// ImportedFrom mirrors the ImportedFrom schema.
type ImportedFrom struct {
	CLI        string    `json:"cli"`
	SessionID  string    `json:"sessionId"`
	Path       string    `json:"path"`
	ImportedAt time.Time `json:"importedAt"`
}
