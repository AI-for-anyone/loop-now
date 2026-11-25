package apitypes

import (
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// Permission event names.
const (
	EventPermissionRequestsUpdated = "permission_requests.updated"
	TimelinePermissionRequest      = "permission_request"
)

// PermissionRequest mirrors the PermissionRequest schema.
type PermissionRequest struct {
	ID         uuid.UUID  `json:"id"`
	AgentID    uuid.UUID  `json:"agentId"`
	RunID      uuid.UUID  `json:"runId"`
	TaskID     *uuid.UUID `json:"taskId"`
	NodeRunID  *uuid.UUID `json:"nodeRunId"`
	Runtime    string     `json:"runtime"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Detail     *string    `json:"detail"`
	CreatedAt  time.Time  `json:"createdAt"`
	State      string     `json:"state"`
	AnsweredAt *time.Time `json:"answeredAt"`
	Reason     *string    `json:"reason"`
}

// FromPermissionRequest converts a request.
func FromPermissionRequest(r store.PermissionRequest) PermissionRequest {
	return PermissionRequest{ID: r.ID, AgentID: r.AgentID, RunID: r.OwnerRun(), TaskID: r.TaskID, NodeRunID: r.NodeRunID, Runtime: r.Runtime,
		Kind: r.Kind, Title: r.Title, Detail: r.Detail, CreatedAt: r.CreatedAt, State: r.State, AnsweredAt: r.AnsweredAt, Reason: r.Reason}
}

// FromPermissionRequests converts a list (never nil).
func FromPermissionRequests(rs []store.PermissionRequest) []PermissionRequest {
	out := make([]PermissionRequest, 0, len(rs))
	for _, r := range rs {
		out = append(out, FromPermissionRequest(r))
	}
	return out
}

// PermissionRequestAnsweredDetails mirrors the PermissionRequestAnsweredDetails schema.
type PermissionRequestAnsweredDetails struct {
	Request PermissionRequest `json:"request"`
}

// PermissionRequestList mirrors the PermissionRequestList schema.
type PermissionRequestList struct {
	Requests []PermissionRequest `json:"requests"`
}

// SsePermissionRequestsUpdated mirrors the SsePermissionRequestsUpdated schema.
type SsePermissionRequestsUpdated struct {
	Requests []PermissionRequest `json:"requests"`
}

// TaskNodePermissionRequests mirrors the TaskNodePermissionRequests schema.
type TaskNodePermissionRequests struct {
	TaskID    uuid.UUID           `json:"taskId"`
	NodeRunID uuid.UUID           `json:"nodeRunId"`
	RuntimeID uuid.UUID           `json:"runtimeId"`
	Requests  []PermissionRequest `json:"requests"`
}

// PermissionSettings mirrors the PermissionSettings schema.
type PermissionSettings struct {
	DefaultMode string `json:"defaultMode"`
}

// PermissionAnswer mirrors the PermissionAnswer schema.
type PermissionAnswer struct {
	Decision string  `json:"decision"`
	Reason   *string `json:"reason"`
}

// TimelinePermissionRequestEntry mirrors the TimelinePermissionRequest schema.
type TimelinePermissionRequestEntry struct {
	TimelineBase
	RequestID  uuid.UUID  `json:"requestId"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	State      string     `json:"state"`
	AnsweredAt *time.Time `json:"answeredAt"`
	Reason     *string    `json:"reason"`
}
