package apitypes

import (
	"github.com/google/uuid"
	"time"
)

type TaskNodeRunSummary struct {
	ID            uuid.UUID  `json:"id"`
	Origin        string     `json:"origin"`
	ExecutionMode string     `json:"executionMode"`
	Status        string     `json:"status"`
	QueuedAt      time.Time  `json:"queuedAt"`
	StartedAt     *time.Time `json:"startedAt"`
	EndedAt       *time.Time `json:"endedAt"`
	ErrorCode     *string    `json:"errorCode"`
	ErrorMessage  *string    `json:"errorMessage"`
}
type TaskNodeRunTimeline struct {
	TaskID     uuid.UUID          `json:"taskId"`
	NodeRunID  uuid.UUID          `json:"nodeRunId"`
	AgentID    uuid.UUID          `json:"agentId"`
	RuntimeID  uuid.UUID          `json:"runtimeId"`
	Run        TaskNodeRunSummary `json:"run"`
	Entries    []any              `json:"entries"`
	Usage      UsageTotals        `json:"usage"`
	NextCursor *string            `json:"nextCursor"`
}
