// Package sleepmodel defines scope-local persistent waiting, without execution behavior.
package sleepmodel

import (
	"github.com/google/uuid"
	"time"
)

type Input struct {
	ActionID uuid.UUID `json:"actionId"`
	RunID    uuid.UUID `json:"runId"`
	Seconds  int64     `json:"seconds"`
	Reason   *string   `json:"reason,omitempty"`
}
type Record struct {
	ID          uuid.UUID  `json:"id"`
	ScopeKind   string     `json:"scopeKind"`
	AgentID     uuid.UUID  `json:"agentId"`
	TaskID      *uuid.UUID `json:"taskId"`
	RuntimeID   *uuid.UUID `json:"runtimeId"`
	NodeRunID   *uuid.UUID `json:"nodeRunId"`
	SourceRunID uuid.UUID  `json:"sourceRunId"`
	State       string     `json:"state"`
	Seconds     int64      `json:"seconds"`
	RequestedAt time.Time  `json:"requestedAt"`
	EnteredAt   *time.Time `json:"enteredAt"`
	WakeAt      *time.Time `json:"wakeAt"`
	EndedAt     *time.Time `json:"endedAt"`
	EndReason   *string    `json:"endReason"`
	WakeRunID   *uuid.UUID `json:"wakeRunId"`
	Reason      *string    `json:"reason"`
	Revision    int64      `json:"revision"`
}
type Receipt struct {
	Sleep   Record `json:"sleep"`
	EndTurn bool   `json:"endTurn"`
}
type State struct {
	CurrentRunID *uuid.UUID `json:"currentRunId"`
	Sleep        *Record    `json:"sleep"`
}
type Updated struct {
	TaskVersion   *int64     `json:"taskVersion"`
	ScopeKind     string     `json:"scopeKind"`
	AgentID       uuid.UUID  `json:"agentId"`
	TaskID        *uuid.UUID `json:"taskId"`
	RuntimeID     *uuid.UUID `json:"runtimeId"`
	NodeRunID     *uuid.UUID `json:"nodeRunId"`
	EndedSleepID  *uuid.UUID `json:"endedSleepId"`
	SleepRevision int64      `json:"sleepRevision"`
	Sleep         *Record    `json:"sleep"`
}
