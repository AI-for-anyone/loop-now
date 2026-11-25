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
