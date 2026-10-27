// Package apitypes 定义与 api/openapi.yaml 一一对应的 JSON 结构，供 HTTP 接口与 SSE 事件共用。
// 字段与可空性以 OpenAPI 为准，契约测试会校验实际输出。
package apitypes

import (
	"loop-now/internal/server/messageimage"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

// ToolEvent 对应 ToolEvent。
type ToolEvent struct {
	ToolCallID string    `json:"toolCallId"`
	ToolName   string    `json:"toolName"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
}

// Run 对应 Run。
type Run struct {
	TriggerInboxMessageID *uuid.UUID  `json:"triggerInboxMessageId,omitempty"`
	WakeSleepID           *uuid.UUID  `json:"wakeSleepId,omitempty"`
	ID                    uuid.UUID   `json:"id"`
	Status                string      `json:"status"`
	Phase                 *string     `json:"phase"`
	EndReason             *string     `json:"endReason"`
	ErrorCode             *string     `json:"errorCode"`
	ErrorMessage          *string     `json:"errorMessage"`
	ConfigVersion         int64       `json:"configVersion"`
	WhenBusy              *string     `json:"whenBusy"`
	QueuedAt              time.Time   `json:"queuedAt"`
	StartedAt             *time.Time  `json:"startedAt"`
	RunningAt             *time.Time  `json:"runningAt"`
	AbortRequestedAt      *time.Time  `json:"abortRequestedAt"`
	EndedAt               *time.Time  `json:"endedAt"`
	Tools                 []ToolEvent `json:"tools"`
	Usage                 UsageTotals `json:"usage"`
	Model                 *ModelRef   `json:"model"`
}

// UsageTotals 对应 UsageTotals。
type UsageTotals struct {
	Calls      int64 `json:"calls"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

// FromUsageTotals 转换用量合计。
func FromUsageTotals(t store.UsageTotals) UsageTotals {
	return UsageTotals{Calls: t.Calls, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Total: t.Total}
}

// Message 对应 Message。
type Message struct {
	Images       []messageimage.Ref `json:"images,omitempty"`
	ID           uuid.UUID          `json:"id"`
	Role         string             `json:"role"`
	Origin       string             `json:"origin"`
	Text         string             `json:"text"`
	CreatedAt    time.Time          `json:"createdAt"`
	SessionEpoch int                `json:"sessionEpoch"`
	Run          Run                `json:"run"`
}

// QueueCounts 对应 QueueCounts。
type QueueCounts struct {
	Interrupt int `json:"interrupt"`
	Normal    int `json:"normal"`
}

// SessionInfo 对应 SessionInfo。
type SessionInfo struct {
	State string `json:"state"`
	Epoch int    `json:"epoch"`
}

// AgentStatus 对应 AgentStatus。
type AgentStatus struct {
	Sleep            *sleepmodel.Record `json:"sleep"`
	SleepRevision    int64              `json:"sleepRevision"`
	AgentID          uuid.UUID          `json:"agentId"`
	Busy             bool               `json:"busy"`
	CurrentRun       *Run               `json:"currentRun"`
	Queued           QueueCounts        `json:"queued"`
	ProcessState     string             `json:"processState"`
	ProcessPID       *int               `json:"processPid"`
	ProcessStartedAt *time.Time         `json:"processStartedAt"`
	Session          SessionInfo        `json:"session"`
}

// AgentBusyDetails 对应 AgentBusyDetails。
type AgentBusyDetails struct {
	CurrentRunID uuid.UUID   `json:"currentRunId"`
	Queued       QueueCounts `json:"queued"`
}

// SSE 事件名。
const (
	EventSnapshot       = "snapshot"
	EventMessageCreated = "message.created"
	EventRunUpdated     = "run.updated"
	EventRunText        = "run.text"
	EventRunTool        = "run.tool"
	EventRunEnded       = "run.ended"
	EventSessionUpdated = "session.updated"
	EventQueueUpdated   = "queue.updated"
)

// SseSnapshot 对应 SseSnapshot。
type SseSnapshot struct {
	Status      AgentStatus `json:"status"`
	CurrentText *string     `json:"currentText"`
	QueuedRuns  []Run       `json:"queuedRuns"`
	QueueItems  []QueueItem `json:"queueItems"`
}

// SseQueueUpdated 对应 SseQueueUpdated：全部排队执行与逐条排队列表，按实际处理顺序。
type SseQueueUpdated struct {
	QueuedRuns []Run       `json:"queuedRuns"`
	Items      []QueueItem `json:"items"`
}

// QueueItem 对应 QueueItem：排队列表中的一条待处理消息。
type QueueItem struct {
	Kind      string             `json:"kind"`
	RunID     uuid.UUID          `json:"runId"`
	MessageID *uuid.UUID         `json:"messageId"`
	From      InboxParticipant   `json:"from"`
	Text      string             `json:"text"`
	Images    []messageimage.Ref `json:"images"`
	WhenBusy  string             `json:"whenBusy"`
	QueuedAt  time.Time          `json:"queuedAt"`
}

// FromQueueItem 转换排队项。
func FromQueueItem(it store.QueueItem) QueueItem {
	images := it.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: it.Kind, RunID: it.RunID, MessageID: it.MessageID, From: FromParty(it.From), Text: it.Text, Images: images, WhenBusy: it.WhenBusy, QueuedAt: it.QueuedAt}
}

// FromQueueItems 转换排队列表；空列表为 []。
func FromQueueItems(items []store.QueueItem) []QueueItem {
	out := make([]QueueItem, 0, len(items))
	for _, it := range items {
		out = append(out, FromQueueItem(it))
	}
	return out
}

// AgentQueue 对应 AgentQueue。
type AgentQueue struct {
	AgentID uuid.UUID   `json:"agentId"`
	Items   []QueueItem `json:"items"`
}

// AgentQueueActionInput 对应 AgentQueueActionInput。
type AgentQueueActionInput struct {
	ActionID uuid.UUID `json:"actionId"`
}

// 排队动作（AgentQueueActionReceipt.operation）。
const (
	QueuePromoteRun          = "promote_run"
	QueueCancelRun           = "cancel_run"
	QueueStopRun             = "stop_run"
	QueuePromoteInboxMessage = "promote_inbox_message"
	QueueCancelInboxMessage  = "cancel_inbox_message"
)

// AgentQueueActionReceipt 对应 AgentQueueActionReceipt；同一动作重放时原样返回首次回执。
type AgentQueueActionReceipt struct {
	ActionID  uuid.UUID   `json:"actionId"`
	AgentID   uuid.UUID   `json:"agentId"`
	Operation string      `json:"operation"`
	Status    string      `json:"status"`
	Item      *QueueItem  `json:"item"`
	Run       *Run        `json:"run"`
	Queue     []QueueItem `json:"queue"`
}

// SseMessageCreated 对应 SseMessageCreated。
type SseMessageCreated struct {
	Message Message `json:"message"`
}

// SseRunUpdated 对应 SseRunUpdated。
type SseRunUpdated struct {
	Run Run `json:"run"`
}

// SseRunText 对应 SseRunText。
type SseRunText struct {
	RunID uuid.UUID `json:"runId"`
	Delta string    `json:"delta"`
}

// SseRunTool 对应 SseRunTool。
type SseRunTool struct {
	RunID      uuid.UUID `json:"runId"`
	ToolCallID string    `json:"toolCallId"`
	ToolName   string    `json:"toolName"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
}

// SseRunEnded 对应 SseRunEnded。
type SseRunEnded struct {
	Run     Run     `json:"run"`
	Message Message `json:"message"`
}

// SseSessionUpdated 对应 SseSessionUpdated。
type SseSessionUpdated struct {
	State string `json:"state"`
	Epoch int    `json:"epoch"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// FromRun 把执行记录转为接口结构。
func FromRun(r store.Run) Run {
	out := Run{
		TriggerInboxMessageID: r.TriggerInboxID,
		WakeSleepID:           r.WakeSleepID, ID: r.ID, Status: r.Status, Phase: strPtr(r.Phase), EndReason: strPtr(r.EndReason), ErrorCode: strPtr(r.ErrorCode),
		ErrorMessage: strPtr(r.ErrorMessage), ConfigVersion: r.ConfigVersion, WhenBusy: strPtr(r.WhenBusy),
		QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, RunningAt: r.RunningAt, AbortRequestedAt: r.AbortRequestedAt, EndedAt: r.EndedAt,
		Tools: make([]ToolEvent, 0, len(r.Tools)), Usage: FromUsageTotals(r.Usage),
	}
	if r.Model != nil {
		out.Model = &ModelRef{Provider: r.Model.Provider, ModelID: r.Model.Model}
	}
	if r.Status != "starting" {
		out.Phase = nil
	}
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, ToolEvent{ToolCallID: t.ToolCallID, ToolName: t.ToolName, State: t.State, At: t.At})
	}
	return out
}

// FromMessage 把消息与其所属执行转为接口结构。
func FromMessage(m store.Message, r store.Run) Message {
	return Message{Images: m.Images, ID: m.ID, Role: m.Role, Origin: r.Origin, Text: m.Text, CreatedAt: m.CreatedAt, SessionEpoch: m.SessionEpoch, Run: FromRun(r)}
}
