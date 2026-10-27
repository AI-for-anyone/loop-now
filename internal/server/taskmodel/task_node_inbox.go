package taskmodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/messageimage"
)

type TaskNodeInboxReceipt struct {
	ActionID  uuid.UUID `json:"actionId"`
	TaskID    uuid.UUID `json:"taskId"`
	NodeRunID uuid.UUID `json:"nodeRunId"`
	AgentID   uuid.UUID `json:"agentId"`
	RuntimeID uuid.UUID `json:"runtimeId"`
	// Message is the existing InboxMessage wire schema, not a TaskMessage.
	Message json.RawMessage `json:"message"`
	Status  string          `json:"status"`
}
type TaskNodeInboxRunLink struct {
	RunID uuid.UUID `json:"runId"`
	Role  string    `json:"role"`
}
type TaskNodeInboxNotExecuted struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}
type TaskNodeInboxRecord struct {
	TaskID      uuid.UUID                 `json:"taskId"`
	NodeRunID   uuid.UUID                 `json:"nodeRunId"`
	AgentID     uuid.UUID                 `json:"agentId"`
	RuntimeID   uuid.UUID                 `json:"runtimeId"`
	Message     json.RawMessage           `json:"message"`
	RunLinks    []TaskNodeInboxRunLink    `json:"runLinks"`
	NotExecuted *TaskNodeInboxNotExecuted `json:"notExecuted"`
}
type TaskNodeInboxPage struct {
	Items      []TaskNodeInboxRecord `json:"items"`
	NextCursor *string               `json:"nextCursor"`
}

// TaskNodeQueue lists a node's pending messages one by one, in the order the
// server claims their turns (priority, then queue sequence; sending order
// inside a turn). Turns without messages (sleep wake, switch continuation)
// contribute no items.
type TaskNodeQueue struct {
	TaskID    uuid.UUID   `json:"taskId"`
	NodeRunID uuid.UUID   `json:"nodeRunId"`
	RuntimeID uuid.UUID   `json:"runtimeId"`
	Items     []QueueItem `json:"items"`
}

// QueueItem is one pending message (QueueItem schema). From is the
// InboxParticipant wire form.
type QueueItem struct {
	Kind      string             `json:"kind"`
	RunID     uuid.UUID          `json:"runId"`
	MessageID *uuid.UUID         `json:"messageId"`
	From      json.RawMessage    `json:"from"`
	Text      string             `json:"text"`
	Images    []messageimage.Ref `json:"images"`
	WhenBusy  string             `json:"whenBusy"`
	QueuedAt  time.Time          `json:"queuedAt"`
}

// TaskNodeQueueActionInput is the body of node queue actions (promote, cancel, stop).
type TaskNodeQueueActionInput struct {
	ActionID          uuid.UUID `json:"actionId"`
	ExpectedAgentID   uuid.UUID `json:"expectedAgentId"`
	ExpectedRuntimeID uuid.UUID `json:"expectedRuntimeId"`
}

// TaskNodeQueueActionReceipt is the stored result of a node queue action.
type TaskNodeQueueActionReceipt struct {
	ActionID  uuid.UUID  `json:"actionId"`
	TaskID    uuid.UUID  `json:"taskId"`
	NodeRunID uuid.UUID  `json:"nodeRunId"`
	AgentID   uuid.UUID  `json:"agentId"`
	RuntimeID uuid.UUID  `json:"runtimeId"`
	Operation string     `json:"operation"`
	Status    string     `json:"status"`
	MessageID *uuid.UUID `json:"messageId"`
	// RunID: promote — the interrupt turn now carrying the message; cancel —
	// the turn that carried it; stop — the stopped turn.
	RunID uuid.UUID  `json:"runId"`
	Item  *QueueItem `json:"item"`
}
