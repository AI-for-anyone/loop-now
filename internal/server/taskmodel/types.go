// Package taskmodel holds the data types of the server's task contract; it contains no execution or storage behavior.
package taskmodel

import (
	"encoding/json"
	"slices"
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
	// CurrentNodes are every current node, in the order they became current: one outside a fork, one per branch
	// with a node inside a fork. The Current* fields above name the current node when there is exactly one.
	CurrentNodes []CurrentNode `json:"currentNodes"`
	Reason       *string       `json:"reason"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	EndedAt      *time.Time    `json:"endedAt"`
	Link         string        `json:"link"`
	Version      int64         `json:"version"`
	// Branch is the branch of the task's workspace; nil for a task without one.
	Branch *string `json:"branch"`
	// WorkspaceMode is the mode of the task's workspace (Workspace.Mode); nil for a task without one.
	WorkspaceMode *string `json:"workspaceMode"`
}

// MarshalJSON writes currentNodes as a list, empty when there is no current node.
func (t Summary) MarshalJSON() ([]byte, error) {
	type plain Summary
	if t.CurrentNodes == nil {
		t.CurrentNodes = []CurrentNode{}
	}
	return json.Marshal(plain(t))
}

// CurrentNode is a current node of a task (TaskCurrentNode).
type CurrentNode struct {
	NodeRunID uuid.UUID `json:"nodeRunId"`
	StepID    string    `json:"stepId"`
	StepTitle string    `json:"stepTitle"`
	AgentID   uuid.UUID `json:"agentId"`
	Branch    *string   `json:"branch"`
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
	// Branch is the branch the node's step is in (named by its first step); nil outside every branch.
	Branch        *string                 `json:"branch"`
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
	if d.Task.CurrentNodes == nil {
		// Stored before forks: the one current node, if any.
		d.Task.CurrentNodes = []CurrentNode{}
		if d.Task.CurrentNodeRunID != nil && d.Task.CurrentStepID != nil && d.Task.CurrentAgentID != nil {
			d.Task.CurrentNodes = append(d.Task.CurrentNodes, CurrentNode{NodeRunID: *d.Task.CurrentNodeRunID, StepID: *d.Task.CurrentStepID, AgentID: *d.Task.CurrentAgentID})
		}
	}
	for i := range d.Task.CurrentNodes {
		c := &d.Task.CurrentNodes[i]
		c.StepTitle = c.StepID
		if t := d.stepTitle(&c.StepID); t != nil {
			c.StepTitle = *t
		}
	}
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
	// Branches are the workspaces of the task's parallel branches, one per branch in the order they first started.
	Branches []WorkspaceBranch `json:"branches"`
}

// MarshalJSON writes branches as a list, empty for a workspace made before forks.
func (w Workspace) MarshalJSON() ([]byte, error) {
	type plain Workspace
	if w.Branches == nil {
		w.Branches = []WorkspaceBranch{}
	}
	return json.Marshal(plain(w))
}

// WorkspaceBranch is the workspace of one parallel branch (TaskWorkspaceBranch): its own worktree in git mode, its own
// copy of the task's copy in copy mode.
type WorkspaceBranch struct {
	Step  string              `json:"step"`
	Path  string              `json:"path"`
	State string              `json:"state"`
	Merge *string             `json:"merge"`
	Git   *WorkspaceBranchGit `json:"git"`
}

// WorkspaceBranchGit is the git side of a branch's workspace (TaskWorkspaceBranchGit).
type WorkspaceBranchGit struct {
	Branch     string `json:"branch"`
	BaseCommit string `json:"baseCommit"`
}

// Merge results of a branch at its join (TaskWorkspaceBranch.merge).
const (
	BranchMerged   = "merged"
	BranchConflict = "conflict"
	BranchSkipped  = "skipped"
)

// Branch returns the workspace of the branch with this first step, nil when there is none.
func (w *Workspace) Branch(step string) *WorkspaceBranch {
	if w == nil {
		return nil
	}
	for i := range w.Branches {
		if w.Branches[i].Step == step {
			return &w.Branches[i]
		}
	}
	return nil
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
	if w.Branches == nil {
		w.Branches = []WorkspaceBranch{}
	}
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
	// Fork is internal: the fork whose branches are running; nil outside a fork.
	Fork *ForkRun `json:"fork,omitempty"`
}

// ForkRun is a fork taken and not yet joined: its branches in the order of the fork's edges.
type ForkRun struct {
	ID string `json:"id"`
	// Join is the step where the branches meet.
	Join string `json:"join"`
	// SourceNodeRunID is the node that took the fork.
	SourceNodeRunID uuid.UUID   `json:"sourceNodeRunId"`
	Branches        []BranchRun `json:"branches"`
}

// Branch states of a running fork.
const (
	// BranchWaiting: not started yet (its workspace is made, then its first node, when the limit on branches at once
	// allows it).
	BranchWaiting = "waiting"
	// BranchStarting: its node is current, but its workspace is not made yet and its node message not queued.
	BranchStarting = "starting"
	// BranchRunning: its node is current.
	BranchRunning = "running"
	// BranchArrived: it submitted an edge into the join.
	BranchArrived = "arrived"
)

// BranchRun is one branch of a running fork.
type BranchRun struct {
	// Step is the branch's first step, which names it.
	Step string `json:"step"`
	// EdgeID is the fork edge that starts it.
	EdgeID string `json:"edgeId"`
	State  string `json:"state"`
	// ArrivedNodeRunID is the node that submitted the edge into the join.
	ArrivedNodeRunID *uuid.UUID `json:"arrivedNodeRunId,omitempty"`
}

// Joined reports whether every branch of the fork has arrived at its join.
func (f *ForkRun) Joined() bool {
	for _, b := range f.Branches {
		if b.State != BranchArrived {
			return false
		}
	}
	return true
}

// BranchRun returns the branch with this first step, nil when the fork has none.
func (f *ForkRun) BranchRun(step string) *BranchRun {
	for i := range f.Branches {
		if f.Branches[i].Step == step {
			return &f.Branches[i]
		}
	}
	return nil
}

// Current is the task's current node when it has exactly one; nil when it has none or several (inside a fork).
func (r *Record) Current() *NodeRun {
	if r.View.Task.CurrentNodeRunID == nil {
		return nil
	}
	return r.Node(*r.View.Task.CurrentNodeRunID)
}

// Node is the node run with this id, nil when the task has none.
func (r *Record) Node(id uuid.UUID) *NodeRun {
	for i := range r.View.Nodes {
		if r.View.Nodes[i].ID == id {
			return &r.View.Nodes[i]
		}
	}
	return nil
}

// Active are the task's current nodes: the one current node outside a fork, one per branch with a node inside one.
func (r *Record) Active() []*NodeRun {
	out := []*NodeRun{}
	for _, c := range r.View.Task.CurrentNodes {
		if n := r.Node(c.NodeRunID); n != nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 && len(r.View.Task.CurrentNodes) == 0 {
		if n := r.Current(); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// ActiveNode is the node run with this id when it is a current node; nil otherwise.
func (r *Record) ActiveNode(id uuid.UUID) *NodeRun {
	for _, n := range r.Active() {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// ActiveFor is the current node of the task instance runtime; nil when the instance has none. An instance has at most
// one: an Agent working in a branch works only in that branch.
func (r *Record) ActiveFor(runtime uuid.UUID) *NodeRun {
	for _, n := range r.Active() {
		if n.RuntimeID == runtime {
			return n
		}
	}
	return nil
}

// SetCurrent makes n the task's only current node: its node run, step (id and title) and Agent.
func (r *Record) SetCurrent(n NodeRun) {
	r.View.Task.CurrentNodes = []CurrentNode{r.currentNode(n)}
	r.syncCurrent()
}

// ReplaceCurrent puts next in place of the current node old (a branch moving on, a retry); next becomes current at the
// end when old is not current.
func (r *Record) ReplaceCurrent(old uuid.UUID, next NodeRun) {
	list := r.currentList()
	for i, c := range list {
		if c.NodeRunID == old {
			list[i] = r.currentNode(next)
			r.View.Task.CurrentNodes = list
			r.syncCurrent()
			return
		}
	}
	r.View.Task.CurrentNodes = append(list, r.currentNode(next))
	r.syncCurrent()
}

// AddCurrent makes n one more current node (a branch starting).
func (r *Record) AddCurrent(n NodeRun) {
	r.View.Task.CurrentNodes = append(r.currentList(), r.currentNode(n))
	r.syncCurrent()
}

// RemoveCurrent takes the node out of the current nodes (a branch arriving at its join).
func (r *Record) RemoveCurrent(id uuid.UUID) {
	list := []CurrentNode{}
	for _, c := range r.currentList() {
		if c.NodeRunID != id {
			list = append(list, c)
		}
	}
	r.View.Task.CurrentNodes = list
	r.syncCurrent()
}

// currentList is the list of current nodes, filled from the single current node of a record stored before forks.
func (r *Record) currentList() []CurrentNode {
	if len(r.View.Task.CurrentNodes) == 0 {
		if n := r.Current(); n != nil {
			return []CurrentNode{r.currentNode(*n)}
		}
		return []CurrentNode{}
	}
	return slices.Clone(r.View.Task.CurrentNodes)
}

func (r *Record) currentNode(n NodeRun) CurrentNode {
	c := CurrentNode{NodeRunID: n.ID, StepID: n.StepID, StepTitle: n.StepID, AgentID: n.AgentID, Branch: n.Branch}
	if t := r.View.stepTitle(&n.StepID); t != nil {
		c.StepTitle = *t
	}
	return c
}

// syncCurrent sets the single current fields from the list: they name the current node when there is exactly one, and
// are nil otherwise.
func (r *Record) syncCurrent() {
	t := &r.View.Task
	if len(t.CurrentNodes) != 1 {
		t.CurrentNodeRunID, t.CurrentStepID, t.CurrentStepTitle, t.CurrentAgentID = nil, nil, nil, nil
		return
	}
	c := t.CurrentNodes[0]
	id, step, agent := c.NodeRunID, c.StepID, c.AgentID
	t.CurrentNodeRunID, t.CurrentStepID, t.CurrentAgentID = &id, &step, &agent
	t.CurrentStepTitle = r.View.stepTitle(&step)
}

// SettleForkStatus sets the status of a task inside a fork from its current nodes, each a branch: running while any
// branch has work to do (or none has a node yet), else waiting while any waits, else blocked with the reason of the
// first blocked node. Outside a fork, and for a task that is preparing or has ended, it changes nothing.
func (r *Record) SettleForkStatus() {
	if r.Fork == nil || r.Ended() || r.View.Task.Status == "preparing" {
		return
	}
	run, wait := len(r.Active()) == 0, false
	var reason *string
	for _, n := range r.Active() {
		switch n.Status {
		case "waiting":
			wait = true
		case "blocked":
			if reason == nil {
				reason = n.Reason
			}
		default:
			run = true
		}
	}
	switch {
	case run:
		r.View.Task.Status, r.View.Task.Reason = "running", nil
	case wait:
		r.View.Task.Status, r.View.Task.Reason = "waiting", nil
	default:
		r.View.Task.Status, r.View.Task.Reason = "blocked", reason
	}
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
