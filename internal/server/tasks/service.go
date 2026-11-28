// Package tasks coordinates generic tasks; business fields are defined by the workflow, and no business process is
// built in.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/dbcli"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/pluginhost"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"

	"loop-now/internal/server/db"
)

type Options struct {
	InstanceID uuid.UUID
	// AbortPollInterval is how often the database is checked for an interrupt flag during a turn (a fallback for when
	// the interrupt notification is lost); 0 means 10 seconds.
	AbortPollInterval             time.Duration
	RecordTimeout                 time.Duration
	MaxSteps, MaxEdges, MaxVisits int
	MaxRequest, MaxArtifact       int64
	ReconcileInterval             time.Duration
}

func (o Options) abortPollInterval() time.Duration {
	if o.AbortPollInterval > 0 {
		return o.AbortPollInterval
	}
	return 10 * time.Second
}

type Service struct {
	// Hooks calls plugins' node.before_start before a task node first starts; when nil, nothing is called.
	Hooks pluginhost.Hooker
	// Native gate is supplied only by the actual runtime manager at composition.
	LarkSwitchNative TaskLarkNativeManager
	larkMu           sync.Mutex
	larkHeld         map[larkSwitchKey]*taskLarkHeld
	inboxDrivers     map[uuid.UUID]bool
	inboxCancels     map[uuid.UUID]context.CancelFunc
	queueMu          sync.Mutex
	eventsMu         sync.Mutex
	eventPosition    int64
	Coord            *coord.Coord
	Store            *store.Store
	Config           *agentconfig.Service
	Executor         executor.Executor
	Options          Options
	Log              *slog.Logger
	Source           *Source
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
	mu               sync.Mutex
	drivers          map[uuid.UUID]bool
	cancels          map[uuid.UUID]context.CancelFunc
	// pokes wake a running driver to look at its task again (Kick while it runs).
	pokes               map[uuid.UUID]chan struct{}
	started             bool
	QuestionMaintenance func(context.Context) error
	SystemNotice        func(context.Context, uuid.UUID, uuid.UUID, string) error
	// InboxCancelled is called after a queued node message has been cancelled by a person and the transaction has
	// committed (it appends an Inbox event); when nil, nothing is pushed.
	InboxCancelled func(context.Context, store.InboxMessage)
}

func New(st *store.Store, cfg *agentconfig.Service, ex executor.Executor, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{Store: st, Config: cfg, Executor: ex, Options: o, Log: log, ctx: ctx, cancel: cancel, inboxDrivers: map[uuid.UUID]bool{}, inboxCancels: map[uuid.UUID]context.CancelFunc{}, drivers: map[uuid.UUID]bool{}, cancels: map[uuid.UUID]context.CancelFunc{}, pokes: map[uuid.UUID]chan struct{}{}}
}

func conflict(message string) error { return apperr.Conflict(apperr.CodeTaskConflict, message) }
func fieldError(err error) error {
	var e *workflow.Error
	if errors.As(err, &e) {
		return apperr.Invalid(e.Field, e.Message)
	}
	return err
}
func bodyAction(p auth.Principal, id uuid.UUID, op string, value any) *store.TaskAction {
	b, _ := json.Marshal(value)
	key := string(p.Kind) + ":" + p.ID.String() + ":" + p.RuntimeID.String()
	return &store.TaskAction{ActorKey: key, ID: id, Operation: op, Body: b}
}

// ActionRecorded reports whether this principal's action ID has already been performed: a replay returns the original
// result directly, and synchronous hooks do not ask plugins again.
func (s *Service) ActionRecorded(ctx context.Context, p auth.Principal, id uuid.UUID) (bool, error) {
	return s.Store.TaskActionRecorded(ctx, bodyAction(p, id, "", nil).ActorKey, id)
}

func manager(p auth.Principal, m *store.TaskMutation) error {
	if p.Kind == auth.KindConsole {
		return nil
	}
	if p.Kind == auth.KindAgent && m.ManagerID != nil && p.ID == *m.ManagerID {
		return nil
	}
	return apperr.Forbidden("Only a person or the project's current Manager can act on this task.")
}

// historyActor lets the same action replay its cached response after the node has advanced; new actions must still
// target the current node.
func historyActor(p auth.Principal, m *store.TaskMutation, nodeID uuid.UUID) error {
	if p.Kind != auth.KindAgent || p.TaskID != m.Record.View.Task.ID {
		return apperr.Forbidden("Only an Agent instance of this task can do this.")
	}
	for _, n := range m.Record.View.Nodes {
		if n.ID == nodeID && n.RuntimeID == p.RuntimeID && n.AgentID == p.ID {
			return nil
		}
	}
	return apperr.Forbidden("The node does not belong to the current task instance.")
}

// nodeActor checks that the caller is the task instance whose current node is nodeID, and returns that node.
func nodeActor(p auth.Principal, m *store.TaskMutation, nodeID uuid.UUID) (*taskmodel.NodeRun, error) {
	var n *taskmodel.NodeRun
	if p.Kind == auth.KindAgent && p.TaskID == m.Record.View.Task.ID {
		n = m.Record.ActiveFor(p.RuntimeID)
	}
	if n == nil || p.ID != n.AgentID {
		return nil, apperr.Forbidden("Only the task instance running the current node can do this.")
	}
	if n.ID != nodeID {
		return nil, conflict("The node run has changed. Read the task again.")
	}
	return n, nil
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in taskmodel.CreateInput) (json.RawMessage, error) {
	if p.Kind != auth.KindAgent {
		return nil, apperr.Forbidden("Only the project's Manager can create tasks, through the Agent API.")
	}
	if strings.TrimSpace(in.Title) == "" || len([]rune(in.Title)) > 200 || in.ActionID == uuid.Nil || in.Request == nil {
		return nil, apperr.Invalid("task", "title must not be blank and must be at most 200 characters; request and a valid actionId are required.")
	}
	if err := in.Workflow.Validate(workflow.Limits{MaxSteps: s.Options.MaxSteps, MaxEdges: s.Options.MaxEdges}); err != nil {
		return nil, fieldError(err)
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, apperr.Invalid("task", "The task cannot be encoded as JSON.")
	}
	if int64(len(encoded)) > s.Options.MaxRequest {
		return nil, apperr.TooLarge("The task definition and input exceed the size limit.")
	}
	a, err := s.Store.GetAgent(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if a.ProjectID == nil || !a.IsManager {
		return nil, apperr.Forbidden("Only the project's current Manager can create tasks.")
	}
	var workspace workspacePlan
	if in.Workspace != nil {
		if workspace, err = s.checkWorkspace(ctx, *a.ProjectID, *in.Workspace, in.Workflow); err != nil {
			return nil, err
		}
	}
	action := bodyAction(p, in.ActionID, "createTask", in)
	out, err := s.Store.CreateTask(ctx, *a.ProjectID, p.ID, *action, func(tx *db.Tx) (taskmodel.Record, []store.TaskInstance, error) {

		id := uuid.Must(uuid.NewV7())
		if in.SourceInboxMessageID != nil {
			var runtime *uuid.UUID
			if p.RuntimeID != uuid.Nil {
				runtime = &p.RuntimeID
			}
			var received bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_messages WHERE id=$1 AND to_kind='agent' AND to_agent_id=$2 AND to_runtime_id IS $3)`, *in.SourceInboxMessageID, p.ID, runtime).Scan(&received); e != nil {
				return taskmodel.Record{}, nil, e
			}
			if !received {
				return taskmodel.Record{}, nil, apperr.Invalid("sourceInboxMessageId", "Must be a message in the requesting Manager's Inbox.")
			}
		}
		now := time.Now().UTC()
		project := prompts.ProjectRules{ID: *a.ProjectID}
		if err := tx.QueryRow(ctx, `SELECT prompt,prompt_version FROM projects WHERE id=$1`, project.ID).Scan(&project.Prompt, &project.Version); err != nil {
			return taskmodel.Record{}, nil, err
		}
		credentialRevision, e := s.Store.ProjectCredentialRevisionTx(ctx, tx, project.ID)
		if e != nil {
			return taskmodel.Record{}, nil, e
		}
		instances := []store.TaskInstance{}
		agents := []uuid.UUID{}
		for _, step := range in.Workflow.Steps {
			if !slices.Contains(agents, step.AgentID) {
				agents = append(agents, step.AgentID)
			}
		}
		slices.SortFunc(agents, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		for _, aid := range agents {
			runtimeID := uuid.Must(uuid.NewV7())
			capture, e := s.Store.CaptureTaskAgent(ctx, tx, aid, runtimeID, project, credentialRevision)
			if e != nil {
				return taskmodel.Record{}, nil, e
			}
			models := s.Config.Models.Snapshot()
			level, e := models.ResolveThinking(capture.Agent.ModelProvider, capture.Agent.ModelID, capture.Agent.ThinkingLevel)
			if e != nil {
				return taskmodel.Record{}, nil, apperr.Invalid("thinkingLevel", e.Error())
			}
			capture.Configuration.ModelsJSON, _ = models.ModelsJSONFor(capture.Agent.ModelProvider)
			capture.Configuration.ThinkingLevel = &level
			capture.Configuration.ServiceTier, e = agentconfig.ResolveServiceTier(capture.Agent.ModelProvider, capture.Agent.ServiceTier)
			if e != nil {
				return taskmodel.Record{}, nil, e
			}
			if capture.Agent.ProjectID == nil || *capture.Agent.ProjectID != *a.ProjectID {
				return taskmodel.Record{}, nil, apperr.Invalid("workflow.steps.agentId", "Must be an Agent in this project.")
			}
			creds := taskruntime.Credentials{ModelAuth: capture.ModelAuth, Model: map[string]string{}, CLI: map[string]taskruntime.CredentialPayload{}}
			dbEntries := map[string][]clitemplate.DatabaseEntry{}
			for _, row := range capture.CredentialRows {
				if capture.ModelAuthManaged && row.Purpose == store.PurposeModel {
					continue
				}
				if row.LarkBinding != nil {
					if !row.LarkBinding.Valid() || row.LarkBinding.AgentID != aid || row.LarkBinding.ProjectID != project.ID {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The Lark authorization binding is invalid.")
					}
					c, e := clicredential.DecodeLark(row.Secret)
					if e != nil || !c.Matches(*row.LarkBinding) {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The Lark authorization is not available.")
					}
					binding := *row.LarkBinding
					creds.LarkBinding = &binding
					continue
				}
				if row.GoogleBinding != nil {
					if !row.GoogleBinding.Valid() || row.GoogleBinding.AgentID != aid || row.GoogleBinding.ProjectID != project.ID {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The Google authorization binding is invalid.")
					}
					if _, e := clicredential.DecodeGoogle(row.Secret); e != nil {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The Google authorization is not available.")
					}
					binding := *row.GoogleBinding
					creds.GoogleBinding = &binding
					continue
				}
				if row.Purpose == store.PurposeCLI {
					template, ok := s.Config.Templates.Get(row.CLIName)
					if !ok || row.TemplateSource != "builtin" || template.CredentialFormat() != row.TemplateVersion {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The CLI credential template is not available. Enter the credential again.")
					}
				}
				var payload clitemplate.Payload
				if e := json.Unmarshal(row.Secret, &payload); e != nil {
					return taskmodel.Record{}, nil, apperr.Invalid("credentials", "A credential is not available.")
				}
				if row.Purpose == store.PurposeModel {
					creds.Model = payload.Env
				} else {
					files := []taskruntime.File{}
					for _, f := range payload.Files {
						files = append(files, taskruntime.File{Path: f.Path, Content: f.Content})
					}
					if row.CLIName == "psql" || row.CLIName == "mysql" {
						if row.DBConnection == nil || row.DBConnection.VersionID == nil {
							return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The captured database identity is missing.")
						}
						item := dbcli.Connection{ProjectID: capture.Agent.ProjectID, AccountID: row.DBConnection.AccountID, Name: row.DBConnection.Name, VersionID: *row.DBConnection.VersionID}
						dbEntries[row.CLIName] = append(dbEntries[row.CLIName], clitemplate.DatabaseEntry{Connection: item, Payload: payload})
					} else {
						creds.CLI[row.CLIName] = taskruntime.CredentialPayload{Env: payload.Env, Files: files}
					}

				}
			}
			for _, cli := range capture.Configuration.CLIs {
				if cli == "psql" || cli == "mysql" {
					payload, err := clitemplate.AggregateDatabase(cli, dbEntries[cli])
					if err != nil {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "The captured database configuration is invalid.")
					}
					files := []taskruntime.File{}
					for _, f := range payload.Files {
						files = append(files, taskruntime.File{Path: f.Path, Content: f.Content})
					}
					creds.CLI[cli] = taskruntime.CredentialPayload{Env: payload.Env, Files: files}
				}
			}
			scope, _ := taskruntime.TaskScope(aid, id)
			snapshot, e := taskruntime.Capture(scope, uuid.Must(uuid.NewV7()), taskruntime.CaptureInput{Configuration: capture.Configuration, Credentials: creds})
			if e != nil {
				return taskmodel.Record{}, nil, apperr.Invalid("snapshot", "The task configuration snapshot is not available.")
			}
			data, e := snapshot.Encode()
			if e != nil {
				return taskmodel.Record{}, nil, e
			}
			instances = append(instances, store.TaskInstance{ID: runtimeID, TaskID: id, AgentID: aid, AgentName: capture.Agent.Name, Snapshot: data, Sources: capture.Sources})
		}
		view := taskmodel.Detail{SleepingInstances: []sleepmodel.Record{}, Task: taskmodel.Summary{ID: id, ProjectID: *a.ProjectID, Title: in.Title, Status: "preparing", CreatedAt: now, UpdatedAt: now, Version: 1}, Request: in.Request, Workflow: in.Workflow, Nodes: []taskmodel.NodeRun{}, Artifacts: []taskmodel.Artifact{}, Cleanup: "none"}
		var requestID *uuid.UUID
		if id, e := uuid.Parse(reqid.From(ctx)); e == nil {
			requestID = &id
		}
		r := taskmodel.Record{CreationRequestID: requestID, SourceInboxMessageID: in.SourceInboxMessageID, View: view, EdgeCounts: map[string]int{}}
		if in.Workspace != nil {
			// The generated branch carries the task's number. Write transactions are serialized, so the next number
			// read here is the one the store assigns; the store checks that it is.
			if e := tx.QueryRow(ctx, `SELECT last_task_number+1 FROM projects WHERE id=$1`, *a.ProjectID).Scan(&r.View.Task.Number); e != nil {
				return taskmodel.Record{}, nil, e
			}
			r.View.Workspace = s.newWorkspace(workspace, id, r.View.Task.Number, in.Title)
			mode := r.View.Workspace.Mode
			r.View.Task.WorkspaceMode = &mode
			if g := r.View.Workspace.Git; g != nil {
				r.View.Task.Branch = &g.Branch
			}
			r.WorkspaceBranchGiven = workspace.branch != ""
		}
		start, _ := in.Workflow.Step(in.Workflow.StartStepID)
		var inst store.TaskInstance
		for _, i := range instances {
			if i.AgentID == start.AgentID {
				inst = i
			}
		}
		n := freshNode(in.Workflow, id, start, inst, 1, 1)
		r.View.Nodes = append(r.View.Nodes, n)
		r.SetCurrent(r.View.Nodes[0])
		return r, instances, nil
	})
	if err == nil {
		s.KickTaskResponse(out)
	}
	return out, err
}

func freshNode(def workflow.Definition, taskID uuid.UUID, step workflow.Step, inst store.TaskInstance, visit, attempt int) taskmodel.NodeRun {
	n := taskmodel.NodeRun{ID: uuid.Must(uuid.NewV7()), TaskID: taskID, StepID: step.ID, Visit: visit, Attempt: attempt, AgentID: step.AgentID, AgentName: inst.AgentName, RuntimeID: inst.ID, Status: "queued", Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}}
	if b := def.Branch(step.ID); b != "" {
		n.Branch = &b
	}
	return n
}
func successful(r *taskmodel.Record) []workflow.SuccessfulResult {
	out := []workflow.SuccessfulResult{}
	for _, n := range r.View.Nodes {
		if n.Status == "completed" {
			out = append(out, workflow.SuccessfulResult{NodeRunID: n.ID, StepID: n.StepID, Output: n.Output})
		}
	}
	return out
}

// block blocks node n and the task. Inside a fork only the branch of n stops: saving the task settles its status from
// its branches (taskmodel.Record.SettleForkStatus).
func block(r *taskmodel.Record, n *taskmodel.NodeRun, reason string) {
	r.View.Task.Status = "blocked"
	r.View.Task.Reason = &reason
	if n != nil {
		n.Status = "blocked"
		n.Reason = &reason
	}
}

func (s *Service) Get(ctx context.Context, p auth.Principal, id uuid.UUID) (taskmodel.Detail, error) {
	r, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return taskmodel.Detail{}, err
	}
	if p.Kind == auth.KindConsole {
		return s.QuestionViews(ctx, p, r.View)
	}
	if p.Kind != auth.KindAgent {
		return taskmodel.Detail{}, apperr.Forbidden("The task API is not open to external identities.")
	}
	project, err := s.Store.GetProject(ctx, r.View.Task.ProjectID)
	if err != nil {
		return taskmodel.Detail{}, err
	}
	if project.ManagerAgentID != nil && *project.ManagerAgentID == p.ID {
		return s.QuestionViews(ctx, p, r.View)
	}
	instances, err := s.Store.TaskInstances(ctx, id)
	if err != nil {
		return taskmodel.Detail{}, err
	}
	for _, inst := range instances {
		if inst.AgentID == p.ID && (p.TaskID == uuid.Nil || p.TaskID == id) {
			return s.QuestionViews(ctx, p, r.View)
		}
	}
	return taskmodel.Detail{}, apperr.Forbidden("The caller is not a participant in this task.")
}

func (s *Service) Submit(ctx context.Context, p auth.Principal, id, nodeID uuid.UUID, in taskmodel.SubmitInput) (json.RawMessage, error) {
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return nil, apperr.Invalid("runId", "Must not be the zero UUID.")
	}
	if in.Output == nil {
		return nil, apperr.Invalid("output", "A result object is required.")
	}
	action := bodyAction(p, in.ActionID, "submitTaskNode", struct {
		NodeID uuid.UUID
		Input  taskmodel.SubmitInput
	}{nodeID, in})
	action.Authorize = func(m *store.TaskMutation) error { return historyActor(p, m, nodeID) }
	return s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		n, e := nodeActor(p, m, nodeID)
		if e != nil {
			return nil, e
		}
		if m.Record.Ended() || n.Status == "blocked" || n.Status == "completed" || n.Output != nil {
			return nil, conflict("The current node cannot submit again: the task has ended, or the node is blocked or has already submitted.")
		}
		var runID uuid.UUID
		if in.RunID != nil {
			runID = *in.RunID
		}
		if err := s.Store.CheckTaskLarkRunTx(ctx, m, nodeID, n.RuntimeID, runID); err != nil {
			return nil, err
		}
		var sleeping bool
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_sleeps WHERE runtime_id=$1 AND mode='task_node' AND state IN ('requested','sleeping'))`, n.RuntimeID).Scan(&sleeping); e != nil {
			return nil, e
		}
		if sleeping {
			return nil, apperr.Conflict(apperr.CodeSleepConflict, "The node has a registered sleep and cannot submit at the same time.")
		}
		step, _ := m.Record.View.Workflow.Step(n.StepID)
		if err := workflow.ValidateValues(step.OutputFields, in.Output); err != nil {
			return nil, fieldError(err)
		}
		if _, err := m.Record.View.Workflow.SelectExit(n.StepID, in.EdgeID, nil); err != nil {
			return nil, fieldError(err)
		}
		for _, field := range step.OutputFields {
			if field.Type == "artifact" {
				v, ok := in.Output[field.Name].(map[string]any)
				if !ok {
					continue
				}
				ref, _ := v["artifactId"].(string)
				var exists bool
				if err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_artifacts WHERE id=$1 AND task_id=$2)`, ref, id).Scan(&exists); err != nil {
					return nil, err
				}
				if !exists {
					return nil, apperr.Invalid(field.Name, "The artifact does not belong to this task.")
				}
			}
		}
		value, err := workflow.CloneValue(in.Output)
		if err != nil {
			return nil, err
		}
		n.Output = value.(map[string]any)
		n.EdgeID = in.EdgeID
		n.Status = "submitted"
		if runID != uuid.Nil {
			if _, err = m.Tx.Exec(ctx, `UPDATE task_turns SET lark_submitted=true WHERE id=$1 AND task_id=$2 AND node_run_id=$3 AND lark_switch_action_id IS NOT NULL`, runID, id, nodeID); err != nil {
				return nil, err
			}
		}
		if err = m.Emit("node.updated", map[string]any{"node": n}); err != nil {
			return nil, err
		}
		return n, nil
	})
}
func (s *Service) Fail(ctx context.Context, p auth.Principal, id, nodeID uuid.UUID, in taskmodel.FailInput) (json.RawMessage, error) {
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return nil, apperr.Invalid("runId", "Must not be the zero UUID.")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, apperr.Invalid("reason", "A failure reason is required.")
	}
	action := bodyAction(p, in.ActionID, "failTaskNode", struct {
		NodeID uuid.UUID
		Input  taskmodel.FailInput
	}{nodeID, in})
	action.Authorize = func(m *store.TaskMutation) error { return historyActor(p, m, nodeID) }
	return s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		n, e := nodeActor(p, m, nodeID)
		if e != nil {
			return nil, e
		}
		if m.Record.Ended() || n.Status == "blocked" {
			return nil, conflict("The task has ended.")
		}
		var runID uuid.UUID
		if in.RunID != nil {
			runID = *in.RunID
		}
		if err := s.Store.CheckTaskLarkRunTx(ctx, m, nodeID, n.RuntimeID, runID); err != nil {
			return nil, err
		}
		block(m.Record, n, in.Reason)
		if err := s.queueNotice(ctx, m, n, "blocked"); err != nil {
			return nil, err
		}
		if err := m.Emit("node.updated", map[string]any{"node": n}); err != nil {
			return nil, err
		}
		return n, nil
	})
}

func (s *Service) Retry(ctx context.Context, p auth.Principal, id uuid.UUID, in taskmodel.RetryInput) (json.RawMessage, error) {
	action := bodyAction(p, in.ActionID, "retryTask", in)
	action.Authorize = func(m *store.TaskMutation) error { return manager(p, m) }
	out, err := s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		// Inside a fork, a blocked branch is retried while the others go on.
		old := m.Record.ActiveNode(in.ExpectedNodeRunID)
		blocked := m.Record.View.Task.Status == "blocked"
		if m.Record.Fork != nil {
			blocked = old != nil && old.Status == "blocked"
		}
		if !blocked || old == nil {
			return nil, conflict("The task or its current blocked node has changed.")
		}
		if fenced, err := s.Store.TaskLarkRuntimeFencedTx(ctx, m, old.RuntimeID); err != nil || fenced {
			if err != nil {
				return nil, err
			}
			return nil, conflict("The original instance is in a credential handoff. The node cannot be retried at the same time.")
		}
		if len(m.Record.View.Nodes) >= s.Options.MaxVisits {
			return nil, conflict("The task reached its limit of node visits and retries. Cancel the task.")
		}
		if err := s.closeQuestion(ctx, m, old, "task_retry"); err != nil {
			return nil, err
		}
		if err := s.Store.InterruptTaskTurns(ctx, m, old.ID); err != nil {
			return nil, err
		}
		if err := s.Store.InterruptTaskInboxRounds(ctx, m, old.RuntimeID); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		old.EndedAt = &now
		if err := m.Emit("node.updated", map[string]any{"node": old}); err != nil {
			return nil, err
		}
		step, _ := m.Record.View.Workflow.Step(old.StepID)
		inst := store.TaskInstance{ID: old.RuntimeID, AgentID: old.AgentID, AgentName: old.AgentName}
		node := freshNode(m.Record.View.Workflow, id, step, inst, old.Visit, old.Attempt+1)
		m.Record.View.Nodes = append(m.Record.View.Nodes, node)
		m.Record.ReplaceCurrent(old.ID, node)
		m.Record.View.Task.Reason = nil
		m.Record.View.Task.Status = "running"
		if f := m.Record.Fork; f != nil && node.Branch != nil {
			if b := f.BranchRun(*node.Branch); b != nil && b.State == taskmodel.BranchStarting {
				// Its workspace is not made yet: the driver makes it, then queues the node message.
				return m.Record.View, nil
			}
		}
		var prepared bool
		if e := m.Tx.QueryRow(ctx, `SELECT prepared FROM task_instances WHERE id=$1`, node.RuntimeID).Scan(&prepared); e != nil {
			return nil, e
		}
		if !prepared || m.Record.View.Workspace != nil && m.Record.View.Workspace.State == taskmodel.WorkspaceStatePending {
			m.Record.View.Task.Status = "preparing"
			return m.Record.View, nil
		}
		if err := s.prepareNode(ctx, m, m.Record.Node(node.ID), in.Note, ""); err != nil {
			return nil, err
		}
		return m.Record.View, nil
	})
	if err == nil {
		s.interruptOldTurns(ctx, id)
		s.KickTaskResponse(out)
	}
	return out, err
}
func (s *Service) Cancel(ctx context.Context, p auth.Principal, id uuid.UUID, in taskmodel.CancelInput) (json.RawMessage, error) {
	action := bodyAction(p, in.ActionID, "cancelTask", in)
	action.Authorize = func(m *store.TaskMutation) error { return manager(p, m) }
	out, err := s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		if m.Record.View.Task.Status == "cancelled" {
			m.Unchanged()
			return m.Record.View, nil
		}
		if m.Record.Ended() {
			return nil, conflict("The task has ended.")
		}
		for i := range m.Record.View.Nodes {
			n := &m.Record.View.Nodes[i]
			if err := s.closeQuestion(ctx, m, n, "task_cancel"); err != nil {
				return nil, err
			}
			if n.Status != "completed" {
				n.Status = "cancelled"
				now := time.Now().UTC()
				n.EndedAt = &now
			}
			if err := s.Store.InterruptTaskTurns(ctx, m, n.ID); err != nil {
				return nil, err
			}
			if err := m.Emit("node.updated", map[string]any{"node": n}); err != nil {
				return nil, err
			}
		}
		if err := s.Store.InterruptAllTaskConversations(ctx, m); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		m.Record.View.Task.Status = "cancelled"
		m.Record.View.Task.EndedAt = &now
		m.Record.View.Cleanup = "pending"
		if err := s.queueNotice(ctx, m, m.Record.Current(), "cancelled"); err != nil {
			return nil, err
		}
		return m.Record.View, nil
	})
	if err == nil {
		s.interruptOldTurns(ctx, id)
		s.KickTaskResponse(out)
	}
	return out, err
}

// prepareNode queues the node message of current node n: the step's description, its inputs and where it works, then
// what the join merged (joined, for the first node of a join) and the retry note.
func (s *Service) prepareNode(ctx context.Context, m *store.TaskMutation, n *taskmodel.NodeRun, note, joined string) error {
	if n == nil || m.Record.ActiveNode(n.ID) == nil {
		return conflict("The task has no current node.")
	}
	step, _ := m.Record.View.Workflow.Step(n.StepID)
	inputs, versions, err := workflow.ResolveInputs(step, m.Record.View.Request, successful(m.Record))
	if err != nil {
		block(m.Record, n, err.Error())
		if e := s.queueNotice(ctx, m, n, "blocked"); e != nil {
			return e
		}
		return m.Emit("node.updated", map[string]any{"node": n})
	}
	n.Inputs, n.InputVersions = inputs, versions
	data, _ := json.Marshal(inputs)
	where := "The current working directory is a clean copy generated from the task's start configuration; it does not contain the files accumulated in your regular folder."
	if w := m.Record.View.Workspace; w != nil && w.State == taskmodel.WorkspaceStateActive {
		where = workspaceBlock(*w)
		if n.Branch != nil {
			if b := w.Branch(*n.Branch); b != nil && b.State == taskmodel.WorkspaceStateActive {
				where = branchWorkspaceBlock(*w, *b, m.Record.View.Workflow.JoinOf(*n.Branch))
			}
		}
	}
	if n.Branch != nil {
		where += " " + branchBlock(m.Record.View.Workflow, *n.Branch)
	}
	text := fmt.Sprintf("You are executing task %s, node %s (run %s).\nNode description: %s\nInputs: %s\n%s Check unread Inbox messages first. When you are done, explicitly submit output/edgeId with loopctl; if it fails, you must state the reason; ask questions through the Inbox.\n", m.Record.View.Task.ID, n.StepID, n.ID, step.Description, string(data), where)
	if joined != "" {
		text += joined + "\n"
	}
	if note != "" {
		text += "Retry note: " + note
	}
	if _, err = s.Store.EnqueueTaskTurnFor(ctx, m, n, text, "task_node", 1, nil); err != nil {
		return err
	}
	return m.Emit("node.updated", map[string]any{"node": n})
}

func (s *Service) closeQuestion(ctx context.Context, m *store.TaskMutation, n *taskmodel.NodeRun, reason string) error {
	if n.Question == nil || n.Question.Status != "open" {
		return nil
	}
	// The question service's close adapter performs the persistent close (wired in by the adjacent contract); the node
	// view keeps the history.
	if s.Source == nil || s.Source.CloseQuestion == nil {
		return errors.New("the question close adapter is not configured")
	}
	status, err := s.Source.CloseQuestion(ctx, m.Tx, n.Question.ID, reason)
	if err != nil {
		return err
	}
	switch status {
	case "closed", "answered", "expired":
	default:
		return errors.New("closing the question did not return a valid final state")
	}
	n.Question.Status = status
	n.Question.CanAnswer = false
	return nil
}

// queueNotice queues a notice to the Manager about the task, for node n (nil for none).
func (s *Service) queueNotice(ctx context.Context, m *store.TaskMutation, n *taskmodel.NodeRun, kind string) error {
	var nodeID *uuid.UUID
	if n != nil {
		nodeID = &n.ID
	}
	text := fmt.Sprintf("Task %s: %s.", m.Record.View.Task.Title, kind)
	if m.Record.View.Task.Reason != nil {
		text += " Reason: " + *m.Record.View.Task.Reason
	}
	if m.Record.Fork != nil && kind == "blocked" && n != nil && n.Reason != nil {
		// Inside a fork only that branch stopped.
		step, _ := m.Record.View.Workflow.Step(n.StepID)
		text = fmt.Sprintf("Task %s: the step %s, in a parallel branch, is blocked; the other branches go on. Reason: %s", m.Record.View.Task.Title, step.Title, *n.Reason)
	}
	_, err := m.Tx.Exec(ctx, `INSERT INTO task_notifications(id,task_id,node_run_id,kind,message_id,text) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.Must(uuid.NewV7()), m.Record.View.Task.ID, nodeID, kind, uuid.Must(uuid.NewV7()), text)
	return err
}

// AvailableAgents gives the current Manager only the configuration metadata needed to choose Agents for its project; it
// does not read any credentials.
func (s *Service) AvailableAgents(ctx context.Context, p auth.Principal) (taskmodel.AgentList, error) {
	if p.Kind != auth.KindAgent {
		return taskmodel.AgentList{}, apperr.Forbidden("Only the project's current Manager can choose Agents.")
	}
	a, err := s.Store.GetAgent(ctx, p.ID)
	if err != nil {
		return taskmodel.AgentList{}, err
	}
	if a.ProjectID == nil || !a.IsManager {
		return taskmodel.AgentList{}, apperr.Forbidden("Only the project's current Manager can choose Agents.")
	}
	agents, err := s.Store.ListAgents(ctx, store.ProjectScope(*a.ProjectID))
	if err != nil {
		return taskmodel.AgentList{}, err
	}
	out := taskmodel.AgentList{Agents: []taskmodel.AvailableAgent{}}
	for _, agent := range agents {
		clis, e := s.Store.ListAgentCLIs(ctx, agent.ID)
		if e != nil {
			return taskmodel.AgentList{}, e
		}
		if clis == nil {
			clis = []string{}
		}
		address, _ := agent.Address()
		out.Agents = append(out.Agents, taskmodel.AvailableAgent{ID: agent.ID, Name: agent.Name, Address: address, Prompt: agent.Prompt, CLIs: clis, ConfigVersion: agent.ConfigVersion})
	}
	return out, nil
}

func (s *Service) ManagerTasks(ctx context.Context, p auth.Principal, cursor string, limit int) (taskmodel.Page, error) {
	if p.Kind != auth.KindAgent {
		return taskmodel.Page{}, apperr.Forbidden("Only the project's current Manager can list the project's tasks.")
	}
	a, e := s.Store.GetAgent(ctx, p.ID)
	if e != nil {
		return taskmodel.Page{}, e
	}
	if a.ProjectID == nil || !a.IsManager {
		return taskmodel.Page{}, apperr.Forbidden("Only the project's current Manager can list the project's tasks.")
	}
	return s.Store.ListTasks(ctx, *a.ProjectID, cursor, limit)
}
