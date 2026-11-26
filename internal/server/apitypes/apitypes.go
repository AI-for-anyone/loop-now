// Package apitypes defines the JSON structures that match api/openapi.yaml one to one, shared by the HTTP endpoints and
// the SSE events. OpenAPI is authoritative for fields and nullability; contract tests check the actual output.
package apitypes

import (
	"encoding/json"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/messageimage"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
)

// ToolEvent mirrors the ToolEvent schema.
type ToolEvent struct {
	ToolCallID string    `json:"toolCallId"`
	ToolName   string    `json:"toolName"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
}

// Run mirrors the Run schema.
type Run struct {
	TriggerInboxMessageID *uuid.UUID  `json:"triggerInboxMessageId,omitempty"`
	WakeSleepID           *uuid.UUID  `json:"wakeSleepId,omitempty"`
	PiCommand             *RunCommand `json:"piCommand,omitempty"`
	// Compaction is set only on manual compaction (/compact) runs: the instructions and the result.
	Compaction       *RunCompaction `json:"compaction,omitempty"`
	ID               uuid.UUID      `json:"id"`
	Status           string         `json:"status"`
	Phase            *string        `json:"phase"`
	EndReason        *string        `json:"endReason"`
	ErrorCode        *string        `json:"errorCode"`
	ErrorMessage     *string        `json:"errorMessage"`
	ConfigVersion    int64          `json:"configVersion"`
	WhenBusy         *string        `json:"whenBusy"`
	QueuedAt         time.Time      `json:"queuedAt"`
	StartedAt        *time.Time     `json:"startedAt"`
	RunningAt        *time.Time     `json:"runningAt"`
	AbortRequestedAt *time.Time     `json:"abortRequestedAt"`
	EndedAt          *time.Time     `json:"endedAt"`
	Tools            []ToolEvent    `json:"tools"`
	Usage            UsageTotals    `json:"usage"`
	// UsageReported says whether the runtime reported usage for this run (it has usage records); false means the zeros
	// in Usage are unknown, not zero.
	UsageReported bool      `json:"usageReported"`
	Model         *ModelRef `json:"model"`
	// Selection is who ran the turn (runtime, provider, model, thinking level), recorded when the reply began; null
	// before that and for older records.
	Selection *RunSelection `json:"selection"`
	// ContextHandoff is set only on a turn that carried the saved conversation over to a new runtime.
	ContextHandoff *ContextHandoff `json:"contextHandoff,omitempty"`
	// BackgroundTasks are the background tasks the run started; TriggerBackgroundTasks, only on a reply the CLI gave by
	// itself (origin background), the tasks whose end it followed.
	BackgroundTasks        []BackgroundTask `json:"backgroundTasks"`
	TriggerBackgroundTasks []BackgroundTask `json:"triggerBackgroundTasks,omitempty"`
}

// BackgroundTask mirrors the BackgroundTask schema.
type BackgroundTask struct {
	ID              string     `json:"id"`
	Runtime         string     `json:"runtime"`
	Kind            string     `json:"kind"`
	Label           string     `json:"label"`
	RunID           uuid.UUID  `json:"runId"`
	ToolCallID      *string    `json:"toolCallId"`
	StartedAt       time.Time  `json:"startedAt"`
	Status          string     `json:"status"`
	EndedAt         *time.Time `json:"endedAt"`
	ExitCode        *int64     `json:"exitCode"`
	StopReason      *string    `json:"stopReason"`
	StopRequestedAt *time.Time `json:"stopRequestedAt"`
	Output          *string    `json:"output"`
}

// FromBackgroundTask converts a stored background task; runId is the run or task turn that started it.
func FromBackgroundTask(t store.BackgroundTask) BackgroundTask {
	out := BackgroundTask{ID: t.NativeID, Runtime: t.Runtime, Kind: t.Kind, Label: t.Label, ToolCallID: t.ToolCallID, StartedAt: t.StartedAt,
		Status: t.Status, EndedAt: t.EndedAt, ExitCode: t.ExitCode, StopReason: t.StopReason, StopRequestedAt: t.StopRequestedAt, Output: t.Output}
	switch {
	case t.RunID != nil:
		out.RunID = *t.RunID
	case t.TaskTurnID != nil:
		out.RunID = *t.TaskTurnID
	}
	return out
}

// SseBackgroundTasksUpdated mirrors the SseBackgroundTasksUpdated schema.
type SseBackgroundTasksUpdated struct {
	Tasks []BackgroundTask `json:"tasks"`
}

// TaskNodeBackgroundTasks mirrors the TaskNodeBackgroundTasks schema.
type TaskNodeBackgroundTasks struct {
	TaskID    uuid.UUID        `json:"taskId"`
	NodeRunID uuid.UUID        `json:"nodeRunId"`
	RuntimeID uuid.UUID        `json:"runtimeId"`
	Tasks     []BackgroundTask `json:"tasks"`
}

// BackgroundTaskEndedDetails mirrors the BackgroundTaskEndedDetails schema.
type BackgroundTaskEndedDetails struct {
	Task BackgroundTask `json:"task"`
}

// Operations of stopping background tasks (agent_actions.operation).
const (
	BackgroundStopOne = "stop_background_task"
	BackgroundStopAll = "stop_all_background_tasks"
)

// BackgroundTaskList mirrors the BackgroundTaskList schema.
type BackgroundTaskList struct {
	Tasks []BackgroundTask `json:"tasks"`
}

// FromBackgroundTasks converts a list; never nil.
func FromBackgroundTasks(ts []store.BackgroundTask) []BackgroundTask {
	out := make([]BackgroundTask, 0, len(ts))
	for _, t := range ts {
		out = append(out, FromBackgroundTask(t))
	}
	return out
}

// RunSelection mirrors the RunSelection schema.
type RunSelection struct {
	Runtime       string  `json:"runtime"`
	Provider      string  `json:"provider"`
	ModelID       string  `json:"modelId"`
	ThinkingLevel *string `json:"thinkingLevel"`
}

// ContextHandoff mirrors the ContextHandoff schema.
type ContextHandoff struct {
	// Reason is runtime_changed, session_lost or task_start.
	Reason           string `json:"reason"`
	FromRuntime      string `json:"fromRuntime"`
	ToRuntime        string `json:"toRuntime"`
	IncludedMessages int    `json:"includedMessages"`
	OmittedMessages  int    `json:"omittedMessages"`
	Images           int    `json:"images"`
	Bytes            int    `json:"bytes"`
	BudgetBytes      int    `json:"budgetBytes"`
}

// UsageTotals mirrors the UsageTotals schema.
type UsageTotals struct {
	Calls      int64 `json:"calls"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Total      int64 `json:"total"`
}

// FromUsageTotals converts usage totals.
func FromUsageTotals(t store.UsageTotals) UsageTotals {
	return UsageTotals{Calls: t.Calls, Input: t.Input, Output: t.Output, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Total: t.Total}
}

// Message mirrors the Message schema.
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

// QueueCounts mirrors the QueueCounts schema.
type QueueCounts struct {
	Interrupt int `json:"interrupt"`
	Normal    int `json:"normal"`
}

// SessionInfo mirrors the SessionInfo schema.
type SessionInfo struct {
	State string `json:"state"`
	Epoch int    `json:"epoch"`
}

// AgentStatus mirrors the AgentStatus schema.
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
	// BackgroundTasks are the background tasks running on the Agent's runtime process, oldest first;
	// RestartOnNextMessage says that, while they run, the next message restarts the process (which stops them).
	BackgroundTasks      []BackgroundTask `json:"backgroundTasks"`
	RestartOnNextMessage bool             `json:"restartOnNextMessage"`
	// PermissionRequests are the Agent's pending permission requests, oldest first.
	PermissionRequests []PermissionRequest `json:"permissionRequests"`
}

// AgentBusyDetails mirrors the AgentBusyDetails schema. CurrentRunID is the run in progress, or the earliest queued run
// when none is in progress. It is nil when there is neither: the busy lock is held by a platform command that creates
// no run (/new, /reload, /resume, export), or a run is just about to start or has just ended.
type AgentBusyDetails struct {
	CurrentRunID *uuid.UUID  `json:"currentRunId"`
	Queued       QueueCounts `json:"queued"`
}

// NotFoundDetails mirrors the NotFoundDetails schema: the details of a not_found that is not about a missing resource.
// Reason is NotFoundUnknownEndpoint when no endpoint matches the method and path (for example a console that is newer
// than the server); clients must use it, not the message, to tell "this server has no such endpoint" from "the
// resource does not exist".
type NotFoundDetails struct {
	Reason string `json:"reason"`
}

// NotFoundUnknownEndpoint is NotFoundDetails.Reason when no endpoint matches the method and path.
const NotFoundUnknownEndpoint = "unknown_endpoint"

// SSE event names.
const (
	EventSnapshot       = "snapshot"
	EventMessageCreated = "message.created"
	EventRunUpdated     = "run.updated"
	EventRunText        = "run.text"
	EventRunTool        = "run.tool"
	EventRunEnded       = "run.ended"
	EventSessionUpdated = "session.updated"
	EventQueueUpdated   = "queue.updated"
	// EventBackgroundTasksUpdated carries the Agent's running background tasks (SseBackgroundTasksUpdated).
	EventBackgroundTasksUpdated = "background_tasks.updated"
)

// SseSnapshot mirrors the SseSnapshot schema.
type SseSnapshot struct {
	Status      AgentStatus `json:"status"`
	CurrentText *string     `json:"currentText"`
	QueuedRuns  []Run       `json:"queuedRuns"`
	QueueItems  []QueueItem `json:"queueItems"`
}

// SseQueueUpdated mirrors the SseQueueUpdated schema: all queued runs plus the per-item queue list, in actual
// processing order.
type SseQueueUpdated struct {
	QueuedRuns []Run       `json:"queuedRuns"`
	Items      []QueueItem `json:"items"`
}

// QueueItem mirrors the QueueItem schema: one pending message in the queue list.
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

// FromQueueItem converts a queue item.
func FromQueueItem(it store.QueueItem) QueueItem {
	images := it.Images
	if images == nil {
		images = []messageimage.Ref{}
	}
	return QueueItem{Kind: it.Kind, RunID: it.RunID, MessageID: it.MessageID, From: FromParty(it.From), Text: it.Text, Images: images, WhenBusy: it.WhenBusy, QueuedAt: it.QueuedAt}
}

// FromQueueItems converts a queue list; an empty list becomes [].
func FromQueueItems(items []store.QueueItem) []QueueItem {
	out := make([]QueueItem, 0, len(items))
	for _, it := range items {
		out = append(out, FromQueueItem(it))
	}
	return out
}

// AgentQueue mirrors the AgentQueue schema.
type AgentQueue struct {
	AgentID uuid.UUID   `json:"agentId"`
	Items   []QueueItem `json:"items"`
}

// AgentQueueActionInput mirrors the AgentQueueActionInput schema.
type AgentQueueActionInput struct {
	ActionID uuid.UUID `json:"actionId"`
}

// Queue actions (AgentQueueActionReceipt.operation).
const (
	QueuePromoteRun          = "promote_run"
	QueueCancelRun           = "cancel_run"
	QueueStopRun             = "stop_run"
	QueuePromoteInboxMessage = "promote_inbox_message"
	QueueCancelInboxMessage  = "cancel_inbox_message"
)

// AgentQueueActionReceipt mirrors the AgentQueueActionReceipt schema; replaying the same action returns the first
// receipt unchanged.
type AgentQueueActionReceipt struct {
	ActionID  uuid.UUID   `json:"actionId"`
	AgentID   uuid.UUID   `json:"agentId"`
	Operation string      `json:"operation"`
	Status    string      `json:"status"`
	Item      *QueueItem  `json:"item"`
	Run       *Run        `json:"run"`
	Queue     []QueueItem `json:"queue"`
}

// SseMessageCreated mirrors the SseMessageCreated schema.
type SseMessageCreated struct {
	Message Message `json:"message"`
}

// SseRunUpdated mirrors the SseRunUpdated schema.
type SseRunUpdated struct {
	Run Run `json:"run"`
}

// SseRunText mirrors the SseRunText schema.
type SseRunText struct {
	RunID uuid.UUID `json:"runId"`
	Delta string    `json:"delta"`
}

// SseRunTool mirrors the SseRunTool schema.
type SseRunTool struct {
	RunID      uuid.UUID `json:"runId"`
	ToolCallID string    `json:"toolCallId"`
	ToolName   string    `json:"toolName"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
}

// SseRunEnded mirrors the SseRunEnded schema.
type SseRunEnded struct {
	Run     Run     `json:"run"`
	Message Message `json:"message"`
}

// SseSessionUpdated mirrors the SseSessionUpdated schema.
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

// RunCommand mirrors the RunPiCommand schema: the command handed to Pi in this turn (a /pi:<name> message on the
// conversation page).
type RunCommand struct {
	Name    string `json:"name"`
	Handled bool   `json:"handled"`
	// Notices are, when handled, the messages an extension emitted with ctx.ui.notify while the command ran (the
	// command's output), in order; Truncated means they were cut off after exceeding the limit.
	Notices   []executor.CommandNotice `json:"notices"`
	Truncated bool                     `json:"truncated"`
}

// FromRun converts a run record into the API structure.
func FromRun(r store.Run) Run {
	out := Run{
		TriggerInboxMessageID: r.TriggerInboxID,
		WakeSleepID:           r.WakeSleepID, ID: r.ID, Status: r.Status, Phase: strPtr(r.Phase), EndReason: strPtr(r.EndReason), ErrorCode: strPtr(r.ErrorCode),
		ErrorMessage: strPtr(r.ErrorMessage), ConfigVersion: r.ConfigVersion, WhenBusy: strPtr(r.WhenBusy),
		QueuedAt: r.QueuedAt, StartedAt: r.StartedAt, RunningAt: r.RunningAt, AbortRequestedAt: r.AbortRequestedAt, EndedAt: r.EndedAt,
		Tools: make([]ToolEvent, 0, len(r.Tools)), Usage: FromUsageTotals(r.Usage), UsageReported: r.Usage.Calls > 0,
	}
	if r.Model != nil {
		out.Model = &ModelRef{Provider: r.Model.Provider, ModelID: r.Model.Model}
	}
	if r.PiCommand != nil {
		out.PiCommand = &RunCommand{Name: *r.PiCommand, Handled: r.PiCommandHandled, Notices: []executor.CommandNotice{}}
		var saved struct {
			Notices   []executor.CommandNotice `json:"notices"`
			Truncated bool                     `json:"truncated"`
		}
		if len(r.PiCommandNotices) > 0 && json.Unmarshal(r.PiCommandNotices, &saved) == nil && saved.Notices != nil {
			out.PiCommand.Notices, out.PiCommand.Truncated = saved.Notices, saved.Truncated
		}
	}
	out.Compaction = CompactionOf(r)
	if sel := r.SelectionInfo(); sel != nil {
		out.Selection = &RunSelection{Runtime: sel.Runtime, Provider: sel.Provider, ModelID: sel.ModelID, ThinkingLevel: sel.ThinkingLevel}
	}
	if h := r.HandoffInfo(); h != nil {
		out.ContextHandoff = &ContextHandoff{Reason: h.ReasonOrDefault(), FromRuntime: h.FromRuntime, ToRuntime: h.ToRuntime, IncludedMessages: h.IncludedMessages,
			OmittedMessages: h.OmittedMessages, Images: h.Images, Bytes: h.Bytes, BudgetBytes: h.BudgetBytes}
	}
	if r.Status != "starting" {
		out.Phase = nil
	}
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, ToolEvent{ToolCallID: t.ToolCallID, ToolName: t.ToolName, State: t.State, At: t.At})
	}
	out.BackgroundTasks = FromBackgroundTasks(r.BackgroundTasks)
	if r.Origin == executor.OriginBackground {
		out.TriggerBackgroundTasks = FromBackgroundTasks(r.TriggerBackgroundTasks)
	}
	return out
}

// CompactionOf returns the instructions and result of a manual compaction (/compact) run; nil when the run is not a
// manual compaction.
func CompactionOf(r store.Run) *RunCompaction {
	if r.Origin != executor.OriginConsoleCompact {
		return nil
	}
	c := store.ParseCompaction(r.Compaction)
	return &RunCompaction{Instructions: c.Instructions, Outcome: strPtr(c.Outcome), SkipReason: strPtr(c.SkipReason),
		TokensBefore: c.TokensBefore, EstimatedTokensAfter: c.EstimatedTokensAfter}
}

// FromMessage converts a message and the run it belongs to into the API structure.
func FromMessage(m store.Message, r store.Run) Message {
	return Message{Images: m.Images, ID: m.ID, Role: m.Role, Origin: r.Origin, Text: m.Text, CreatedAt: m.CreatedAt, SessionEpoch: m.SessionEpoch, Run: FromRun(r)}
}
