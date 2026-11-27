// Package taskmodel holds the data types of the server's task contract; it contains no execution or storage behavior.
package taskmodel

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/workflow"
)

// Summary is the task object (TaskSummary in api/openapi.yaml). Number is the task's number within the project (shown
// as #12 in the console): it starts at 1, is assigned by the storage layer on creation, and never changes or gets
// reused.
type Summary struct {
	ID               uuid.UUID  `json:"id"`
	ProjectID        uuid.UUID  `json:"projectId"`
	Number           int64      `json:"number"`
	Title            string     `json:"title"`
	Status           string     `json:"status"`
	CurrentNodeRunID *uuid.UUID `json:"currentNodeRunId"`
	CurrentStepID    *string    `json:"currentStepId"`
	// CurrentStepTitle is the title of the current step in the frozen workflow; nil when there is no current step.
	CurrentStepTitle *string    `json:"currentStepTitle"`
	CurrentAgentID   *uuid.UUID `json:"currentAgentId"`
	Reason           *string    `json:"reason"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	EndedAt          *time.Time `json:"endedAt"`
	Link             string     `json:"link"`
	Version          int64      `json:"version"`
	// Branch is the branch of the task's workspace; nil for a task without one.
	Branch *string `json:"branch"`
	// WorkspaceMode is the mode of the task's workspace (Workspace.Mode); nil for a task without one.
	WorkspaceMode *string `json:"workspaceMode"`
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
	// Workspace is the task's workspace; nil for a task without one (and for records written before workspaces).
	Workspace *Workspace `json:"workspace"`
}

// Workspace states (TaskWorkspace.state in api/openapi.yaml).
const (
	WorkspaceStatePending = "pending"
	WorkspaceStateActive  = "active"
	WorkspaceStateRemoved = "removed"
	WorkspaceStateKept    = "kept"
)

// UnmarshalJSON also fills task.workspaceMode for views stored before it existed (stage 1, git mode only), and
// task.currentStepTitle from the workflow for views stored before that existed.
func (d *Detail) UnmarshalJSON(b []byte) error {
	type plain Detail
	if err := json.Unmarshal(b, (*plain)(d)); err != nil {
		return err
	}
	if d.Task.WorkspaceMode == nil && d.Workspace != nil {
		mode := d.Workspace.Mode
		d.Task.WorkspaceMode = &mode
	}
	d.Task.CurrentStepTitle = d.stepTitle(d.Task.CurrentStepID)
	return nil
}

// stepTitle is the title of the workflow step with this id; nil for a nil id or a step the workflow does not have.
func (d *Detail) stepTitle(id *string) *string {
	if id == nil {
		return nil
	}
	step, ok := d.Workflow.Step(*id)
	if !ok {
		return nil
	}
	return &step.Title
}

// Workspace modes (TaskWorkspace.mode) and the requested mode of TaskWorkspaceInput.
const (
	WorkspaceModeGit  = "git"
	WorkspaceModeCopy = "copy"
	WorkspaceModeAuto = "auto"
)

// Workspace is the task's workspace (TaskWorkspace): a git worktree (Mode git, Git set) or a copy of a folder (Mode
// copy, Copy set once the copy exists). Kept is non-nil exactly when State is WorkspaceStateKept.
type Workspace struct {
	Mode     string         `json:"mode"`
	Folder   string         `json:"folder"`
	Path     string         `json:"path"`
	State    string         `json:"state"`
	Kept     *WorkspaceKept `json:"kept"`
	ModeNote *string        `json:"modeNote"`
	Git      *WorkspaceGit  `json:"git"`
	Copy     *WorkspaceCopy `json:"copy"`
}

// WorkspaceGit is the git side of a git-mode workspace (TaskWorkspaceGit).
type WorkspaceGit struct {
	Repository string  `json:"repository"`
	Branch     string  `json:"branch"`
	BaseBranch string  `json:"baseBranch"`
	BaseCommit *string `json:"baseCommit"`
}

// WorkspaceCopy describes the copy of a copy-mode workspace as made (TaskWorkspaceCopy).
type WorkspaceCopy struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// UnmarshalJSON also reads the stage 1 shape stored before modes existed (repository, branch, baseBranch and
// baseCommit at the top level, no mode) as a git-mode workspace with the same values.
func (w *Workspace) UnmarshalJSON(b []byte) error {
	type plain Workspace
	var v struct {
		plain
		Repository *string `json:"repository"`
		Branch     string  `json:"branch"`
		BaseBranch string  `json:"baseBranch"`
		BaseCommit *string `json:"baseCommit"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*w = Workspace(v.plain)
	if w.Mode == "" && v.Repository != nil {
		w.Mode, w.Folder = WorkspaceModeGit, *v.Repository
		w.Git = &WorkspaceGit{Repository: *v.Repository, Branch: v.Branch, BaseBranch: v.BaseBranch, BaseCommit: v.BaseCommit}
	}
	return nil
}

// WorkspaceKept says what removing a kept workspace would lose (TaskWorkspaceKept); counts that do not apply to the
// mode are 0.
type WorkspaceKept struct {
	UncommittedFiles int `json:"uncommittedFiles"`
	UnpushedCommits  int `json:"unpushedCommits"`
	ChangedFiles     int `json:"changedFiles"`
}

// WorkspaceInput asks for a workspace when a task is created (TaskWorkspaceInput).
type WorkspaceInput struct {
	Folder     string  `json:"folder"`
	Mode       string  `json:"mode,omitempty"`
	BaseBranch *string `json:"baseBranch,omitempty"`
	Branch     *string `json:"branch,omitempty"`
}

// Record is the aggregate saved atomically in the database; internal fields such as counts do not go into the API.
type Record struct {
	CreationRequestID    *uuid.UUID     `json:"creationRequestId,omitempty"`
	SourceInboxMessageID *uuid.UUID     `json:"sourceInboxMessageId,omitempty"`
	View                 Detail         `json:"view"`
	EdgeCounts           map[string]int `json:"edgeCounts"`
	// WorkspaceBranchGiven is internal: the Manager named the workspace branch, so it may exist already.
	WorkspaceBranchGiven bool `json:"workspaceBranchGiven,omitempty"`
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

// SetCurrent makes n the task's current node: its node run, step (id and title) and Agent.
func (r *Record) SetCurrent(n NodeRun) {
	r.View.Task.CurrentNodeRunID = &n.ID
	r.View.Task.CurrentStepID = &n.StepID
	r.View.Task.CurrentStepTitle = r.View.stepTitle(&n.StepID)
	r.View.Task.CurrentAgentID = &n.AgentID
}

func (r *Record) Ended() bool {
	return r.View.Task.Status == "completed" || r.View.Task.Status == "cancelled"
}

type Message struct {
	WakeSleepID *uuid.UUID `json:"wakeSleepId,omitempty"`
	// VoidReason marks a queued turn voided without running; the console hides
	// merged_into_interrupt (its messages moved to an interrupt turn).
	VoidReason   *string `json:"voidReason,omitempty"`
	ErrorCode    *string `json:"errorCode,omitempty"`
	ErrorMessage *string `json:"errorMessage,omitempty"`
	// Runtime is the runtime the task instance runs the turn on; omitted when it cannot be told.
	Runtime   string    `json:"runtime,omitempty"`
	ID        uuid.UUID `json:"id"`
	RunID     uuid.UUID `json:"runId"`
	NodeRunID uuid.UUID `json:"nodeRunId"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	Origin    string    `json:"origin"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}
type MessagePage struct {
	Messages   []Message `json:"messages"`
	NextCursor *string   `json:"nextCursor"`
}
type Page struct {
	Tasks      []Summary `json:"tasks"`
	NextCursor *string   `json:"nextCursor"`
}

// Counts are the counts of the project's unfinished tasks by status (TaskCounts in api/openapi.yaml).
type Counts struct {
	Preparing int64 `json:"preparing"`
	Running   int64 `json:"running"`
	Waiting   int64 `json:"waiting"`
	Blocked   int64 `json:"blocked"`
}

type CreateInput struct {
	ActionID             uuid.UUID           `json:"actionId"`
	Title                string              `json:"title"`
	Request              map[string]any      `json:"request"`
	Workflow             workflow.Definition `json:"workflow"`
	SourceInboxMessageID *uuid.UUID          `json:"sourceInboxMessageId,omitempty"`
	Workspace            *WorkspaceInput     `json:"workspace,omitempty"`
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

// RemoveWorkspaceInput is the body of removeTaskWorkspace.
type RemoveWorkspaceInput struct {
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
