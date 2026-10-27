// Package taskmodel 保存服务端任务契约的数据类型，不包含执行或存储行为。
package taskmodel

import (
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/workflow"
)

type Summary struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"projectId"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	CurrentNodeRunID *uuid.UUID `json:"currentNodeRunId"`
	CurrentStepID    *string    `json:"currentStepId"`
	CurrentAgentID   *uuid.UUID `json:"currentAgentId"`
	Reason           *string    `json:"reason"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	EndedAt          *time.Time `json:"endedAt"`
	Link             string     `json:"link"`
	Version          int64      `json:"version"`
}

type Question struct {
	ID        uuid.UUID `json:"id"`
	ThreadID  uuid.UUID `json:"threadId"`
	MessageID uuid.UUID `json:"messageId"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	CanAnswer bool      `json:"canAnswer"`
}

type NodeRun struct {
	Sleep         *sleepmodel.Record      `json:"sleep"`
	SleepRevision int64                   `json:"sleepRevision"`
	ID            uuid.UUID               `json:"id"`
	TaskID        uuid.UUID               `json:"taskId"`
	StepID        string                  `json:"stepId"`
	Visit         int                     `json:"visit"`
	Attempt       int                     `json:"attempt"`
	AgentID       uuid.UUID               `json:"agentId"`
	AgentName     string                  `json:"agentName"`
	RuntimeID     uuid.UUID               `json:"runtimeId"`
	Status        string                  `json:"status"`
	Inputs        map[string]any          `json:"inputs"`
	InputVersions []workflow.InputVersion `json:"inputVersions"`
	Output        map[string]any          `json:"output"`
	EdgeID        *string                 `json:"edgeId"`
	Reason        *string                 `json:"reason"`
	Question      *Question               `json:"question"`
	RunIDs        []uuid.UUID             `json:"runIds"`
	StartedAt     *time.Time              `json:"startedAt"`
	EndedAt       *time.Time              `json:"endedAt"`
}

type Artifact struct {
	ID           uuid.UUID `json:"id"`
	TaskID       uuid.UUID `json:"taskId"`
	NodeRunID    uuid.UUID `json:"nodeRunId"`
	Name         string    `json:"name"`
	ContentType  string    `json:"contentType"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"createdAt"`
	DownloadPath string    `json:"downloadPath"`
}

type Detail struct {
	SleepingInstances []sleepmodel.Record `json:"sleepingInstances"`
	Task              Summary             `json:"task"`
	Request           map[string]any      `json:"request"`
	Workflow          workflow.Definition `json:"workflow"`
	Nodes             []NodeRun           `json:"nodes"`
	Artifacts         []Artifact          `json:"artifacts"`
	Cleanup           string              `json:"cleanup"`
}

// Record 是数据库中原子保存的聚合，计数等内部字段不进入API。
type Record struct {
	CreationRequestID    *uuid.UUID     `json:"creationRequestId,omitempty"`
	SourceInboxMessageID *uuid.UUID     `json:"sourceInboxMessageId,omitempty"`
	View                 Detail         `json:"view"`
	EdgeCounts           map[string]int `json:"edgeCounts"`
	// NodeQueueDigests is internal: the last emitted node queue per node, so a
	// task.node.queue.updated event is written only when the queue changes.
	NodeQueueDigests map[uuid.UUID]string `json:"nodeQueueDigests,omitempty"`
}

func (r *Record) Current() *NodeRun {
	if r.View.Task.CurrentNodeRunID == nil {
		return nil
	}
	for i := range r.View.Nodes {
		if r.View.Nodes[i].ID == *r.View.Task.CurrentNodeRunID {
			return &r.View.Nodes[i]
		}
	}
	return nil
}
func (r *Record) Ended() bool {
	return r.View.Task.Status == "completed" || r.View.Task.Status == "cancelled"
}

type Message struct {
	WakeSleepID *uuid.UUID `json:"wakeSleepId,omitempty"`
	// VoidReason marks a queued turn voided without running; the console hides
	// merged_into_interrupt (its messages moved to an interrupt turn).
	VoidReason   *string   `json:"voidReason,omitempty"`
	ErrorCode    *string   `json:"errorCode,omitempty"`
	ErrorMessage *string   `json:"errorMessage,omitempty"`
	ID           uuid.UUID `json:"id"`
	RunID        uuid.UUID `json:"runId"`
	NodeRunID    uuid.UUID `json:"nodeRunId"`
	Role         string    `json:"role"`
	Text         string    `json:"text"`
	Origin       string    `json:"origin"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"createdAt"`
}
type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor *string   `json:"nextCursor"`
}
type Page struct {
	Tasks      []Summary `json:"tasks"`
	NextCursor *string   `json:"nextCursor"`
}

type CreateInput struct {
	ActionID             uuid.UUID           `json:"actionId"`
	Title                string              `json:"title"`
	Request              map[string]any      `json:"request"`
	Workflow             workflow.Definition `json:"workflow"`
	SourceInboxMessageID *uuid.UUID          `json:"sourceInboxMessageId,omitempty"`
}
type SubmitInput struct {
	ActionID uuid.UUID      `json:"actionId"`
	RunID    *uuid.UUID     `json:"runId,omitempty"`
	Output   map[string]any `json:"output"`
	EdgeID   *string        `json:"edgeId"`
}
type FailInput struct {
	ActionID uuid.UUID  `json:"actionId"`
	RunID    *uuid.UUID `json:"runId,omitempty"`
	Reason   string     `json:"reason"`
}
type RetryInput struct {
	ActionID          uuid.UUID `json:"actionId"`
	ExpectedNodeRunID uuid.UUID `json:"expectedNodeRunId"`
	Note              string    `json:"note,omitempty"`
}
type CancelInput struct {
	ActionID uuid.UUID `json:"actionId"`
}

type AvailableAgent struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Address       string    `json:"address"`
	Prompt        string    `json:"prompt"`
	CLIs          []string  `json:"clis"`
	ConfigVersion int64     `json:"configVersion"`
}
type AgentList struct {
	Agents []AvailableAgent `json:"agents"`
}
