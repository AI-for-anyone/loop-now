// Package tasks 协调通用任务，业务字段由workflow定义，不内置任何业务流程。
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
	"github.com/jackc/pgx/v5"
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
)

type Options struct {
	InstanceID                       uuid.UUID
	LockRenewInterval, RecordTimeout time.Duration
	MaxSteps, MaxEdges, MaxVisits    int
	MaxRequest, MaxArtifact          int64
	ReconcileInterval                time.Duration
}
type Service struct {
	// Hooks 在任务节点第一次开始前调用插件的 node.before_start；为空时不调用。
	Hooks pluginhost.Hooker
	// Native gate is supplied only by the actual runtime manager at composition.
	LarkSwitchNative    TaskLarkNativeManager
	larkMu              sync.Mutex
	larkHeld            map[larkSwitchKey]*taskLarkHeld
	inboxDrivers        map[uuid.UUID]bool
	inboxCancels        map[uuid.UUID]context.CancelFunc
	queueMu             sync.Mutex
	eventsMu            sync.Mutex
	eventPosition       int64
	Coord               *coord.Coord
	Store               *store.Store
	Config              *agentconfig.Service
	Executor            executor.Executor
	Options             Options
	Log                 *slog.Logger
	Source              *Source
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	mu                  sync.Mutex
	drivers             map[uuid.UUID]bool
	cancels             map[uuid.UUID]context.CancelFunc
	started             bool
	QuestionMaintenance func(context.Context) error
	SystemNotice        func(context.Context, uuid.UUID, uuid.UUID, string) error
	// InboxCancelled 在节点排队消息被人取消、事务提交后调用（追加收件箱事件）；为空时不推送。
	InboxCancelled func(context.Context, store.InboxMessage)
}

func New(st *store.Store, cfg *agentconfig.Service, ex executor.Executor, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{Store: st, Config: cfg, Executor: ex, Options: o, Log: log, ctx: ctx, cancel: cancel, inboxDrivers: map[uuid.UUID]bool{}, inboxCancels: map[uuid.UUID]context.CancelFunc{}, drivers: map[uuid.UUID]bool{}, cancels: map[uuid.UUID]context.CancelFunc{}}
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

// ActionRecorded 报告这个身份的动作 ID 是否已经执行过：重放直接返回原结果，同步 Hook 不再询问插件。
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
	return apperr.Forbidden("只有人或当前项目Manager可操作任务")
}

// historyActor允许同一动作在节点推进后重放缓存响应；新动作仍必须命中当前节点。
func historyActor(p auth.Principal, m *store.TaskMutation, nodeID uuid.UUID) error {
	if p.Kind != auth.KindAgent || p.TaskID != m.Record.View.Task.ID {
		return apperr.Forbidden("只能操作本任务实例")
	}
	for _, n := range m.Record.View.Nodes {
		if n.ID == nodeID && n.RuntimeID == p.RuntimeID && n.AgentID == p.ID {
			return nil
		}
	}
	return apperr.Forbidden("节点不属于当前任务实例")
}
func nodeActor(p auth.Principal, m *store.TaskMutation, nodeID uuid.UUID) error {
	n := m.Record.Current()
	if p.Kind != auth.KindAgent || p.TaskID != m.Record.View.Task.ID || n == nil || p.RuntimeID != n.RuntimeID || p.ID != n.AgentID {
		return apperr.Forbidden("只能操作当前任务实例的节点")
	}
	if n.ID != nodeID {
		return conflict("节点运行已变化，请重新读取任务")
	}
	return nil
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in taskmodel.CreateInput) (json.RawMessage, error) {
	if p.Kind != auth.KindAgent {
		return nil, apperr.Forbidden("任务只能由项目Manager通过Agent接口创建")
	}
	if strings.TrimSpace(in.Title) == "" || len([]rune(in.Title)) > 200 || in.ActionID == uuid.Nil || in.Request == nil {
		return nil, apperr.Invalid("task", "标题、原始要求或动作ID无效")
	}
	if err := in.Workflow.Validate(workflow.Limits{MaxSteps: s.Options.MaxSteps, MaxEdges: s.Options.MaxEdges}); err != nil {
		return nil, fieldError(err)
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, apperr.Invalid("task", "任务不能编码")
	}
	if int64(len(encoded)) > s.Options.MaxRequest {
		return nil, apperr.TooLarge("任务定义与输入超限")
	}
	a, err := s.Store.GetAgent(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if a.ProjectID == nil || !a.IsManager {
		return nil, apperr.Forbidden("只有当前项目Manager可创建任务")
	}
	action := bodyAction(p, in.ActionID, "createTask", in)
	out, err := s.Store.CreateTask(ctx, *a.ProjectID, p.ID, *action, func(tx pgx.Tx) (taskmodel.Record, []store.TaskInstance, error) {

		id := uuid.Must(uuid.NewV7())
		if in.SourceInboxMessageID != nil {
			var runtime *uuid.UUID
			if p.RuntimeID != uuid.Nil {
				runtime = &p.RuntimeID
			}
			var received bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inbox_messages WHERE id=$1 AND to_kind='agent' AND to_agent_id=$2 AND to_runtime_id IS NOT DISTINCT FROM $3::uuid)`, *in.SourceInboxMessageID, p.ID, runtime).Scan(&received); e != nil {
				return taskmodel.Record{}, nil, e
			}
			if !received {
				return taskmodel.Record{}, nil, apperr.Invalid("sourceInboxMessageId", "原消息须属于发起Manager的Inbox")
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
			level, e := s.Config.Models.ResolveThinking(capture.Agent.ModelProvider, capture.Agent.ModelID, capture.Agent.ThinkingLevel)
			if e != nil {
				return taskmodel.Record{}, nil, apperr.Invalid("thinkingLevel", e.Error())
			}
			capture.Configuration.ThinkingLevel = &level
			capture.Configuration.ServiceTier, e = agentconfig.ResolveServiceTier(capture.Agent.ModelProvider, capture.Agent.ServiceTier)
			if e != nil {
				return taskmodel.Record{}, nil, e
			}
			if capture.Agent.ProjectID == nil || *capture.Agent.ProjectID != *a.ProjectID {
				return taskmodel.Record{}, nil, apperr.Invalid("workflow.steps.agentId", "执行Agent必须属于本项目")
			}
			creds := taskruntime.Credentials{ModelAuth: capture.ModelAuth, Model: map[string]string{}, CLI: map[string]taskruntime.CredentialPayload{}}
			dbEntries := map[string][]clitemplate.DatabaseEntry{}
			for _, row := range capture.CredentialRows {
				if capture.ModelAuthManaged && row.Purpose == store.PurposeModel {
					continue
				}
				if row.LarkBinding != nil {
					if !row.LarkBinding.Valid() || row.LarkBinding.AgentID != aid || row.LarkBinding.ProjectID != project.ID {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "Lark授权绑定无效")
					}
					c, e := clicredential.DecodeLark(row.Secret)
					if e != nil || !c.Matches(*row.LarkBinding) {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "Lark授权不可用")
					}
					binding := *row.LarkBinding
					creds.LarkBinding = &binding
					continue
				}
				if row.GoogleBinding != nil {
					if !row.GoogleBinding.Valid() || row.GoogleBinding.AgentID != aid || row.GoogleBinding.ProjectID != project.ID {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "Google授权绑定无效")
					}
					if _, e := clicredential.DecodeGoogle(row.Secret); e != nil {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "Google授权不可用")
					}
					binding := *row.GoogleBinding
					creds.GoogleBinding = &binding
					continue
				}
				if row.Purpose == store.PurposeCLI {
					template, ok := s.Config.Templates.Get(row.CLIName)
					if !ok || row.TemplateSource != "builtin" || template.CredentialFormat() != row.TemplateVersion {
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "CLI凭证模板不可用，请重新录入")
					}
				}
				var payload clitemplate.Payload
				if e := json.Unmarshal(row.Secret, &payload); e != nil {
					return taskmodel.Record{}, nil, apperr.Invalid("credentials", "凭证不可用")
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
							return taskmodel.Record{}, nil, apperr.Invalid("credentials", "数据库捕获身份缺失")
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
						return taskmodel.Record{}, nil, apperr.Invalid("credentials", "数据库捕获配置无效")
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
				return taskmodel.Record{}, nil, apperr.Invalid("snapshot", "任务配置快照不可用")
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
		start, _ := in.Workflow.Step(in.Workflow.StartStepID)
		var inst store.TaskInstance
		for _, i := range instances {
			if i.AgentID == start.AgentID {
				inst = i
			}
		}
		n := freshNode(id, start, inst, 1, 1)
		r.View.Nodes = append(r.View.Nodes, n)
		setCurrent(&r, r.View.Nodes[0])
		return r, instances, nil
	})
	if err == nil {
		s.KickTaskResponse(out)
	}
	return out, err
}

func freshNode(taskID uuid.UUID, step workflow.Step, inst store.TaskInstance, visit, attempt int) taskmodel.NodeRun {
	return taskmodel.NodeRun{ID: uuid.Must(uuid.NewV7()), TaskID: taskID, StepID: step.ID, Visit: visit, Attempt: attempt, AgentID: step.AgentID, AgentName: inst.AgentName, RuntimeID: inst.ID, Status: "queued", Inputs: map[string]any{}, InputVersions: []workflow.InputVersion{}, RunIDs: []uuid.UUID{}}
}
func setCurrent(r *taskmodel.Record, n taskmodel.NodeRun) {
	r.View.Task.CurrentNodeRunID = &n.ID
	r.View.Task.CurrentStepID = &n.StepID
	r.View.Task.CurrentAgentID = &n.AgentID
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
func block(r *taskmodel.Record, reason string) {
	r.View.Task.Status = "blocked"
	r.View.Task.Reason = &reason
	if n := r.Current(); n != nil {
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
		return taskmodel.Detail{}, apperr.Forbidden("任务接口不向外部身份开放")
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
	return taskmodel.Detail{}, apperr.Forbidden("不是本任务参与方")
}

func (s *Service) Submit(ctx context.Context, p auth.Principal, id, nodeID uuid.UUID, in taskmodel.SubmitInput) (json.RawMessage, error) {
	if in.RunID != nil && *in.RunID == uuid.Nil {
		return nil, apperr.Invalid("runId", "runId不能为零UUID")
	}
	if in.Output == nil {
		return nil, apperr.Invalid("output", "必须提交结果对象")
	}
	action := bodyAction(p, in.ActionID, "submitTaskNode", struct {
		NodeID uuid.UUID
		Input  taskmodel.SubmitInput
	}{nodeID, in})
	action.Authorize = func(m *store.TaskMutation) error { return historyActor(p, m, nodeID) }
	return s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		if e := nodeActor(p, m, nodeID); e != nil {
			return nil, e
		}
		n := m.Record.Current()
		if m.Record.Ended() || n.Status == "blocked" || n.Status == "completed" || n.Output != nil {
			return nil, conflict("当前节点不能再次提交")
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
			return nil, apperr.Conflict(apperr.CodeSleepConflict, "节点已登记sleep，不能同时提交")
		}
		step, _ := m.Record.View.Workflow.Step(n.StepID)
		if err := workflow.ValidateValues(step.OutputFields, in.Output); err != nil {
			return nil, fieldError(err)
		}
		if _, err := m.Record.View.Workflow.SelectEdge(n.StepID, in.EdgeID, nil); err != nil {
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
					return nil, apperr.Invalid(field.Name, "成果不属于本任务")
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
		return nil, apperr.Invalid("runId", "runId不能为零UUID")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, apperr.Invalid("reason", "必须提供失败原因")
	}
	action := bodyAction(p, in.ActionID, "failTaskNode", struct {
		NodeID uuid.UUID
		Input  taskmodel.FailInput
	}{nodeID, in})
	action.Authorize = func(m *store.TaskMutation) error { return historyActor(p, m, nodeID) }
	return s.Store.MutateTask(ctx, id, action, func(m *store.TaskMutation) (any, error) {
		if e := nodeActor(p, m, nodeID); e != nil {
			return nil, e
		}
		if m.Record.Ended() || m.Record.Current().Status == "blocked" {
			return nil, conflict("任务已结束")
		}
		var runID uuid.UUID
		if in.RunID != nil {
			runID = *in.RunID
		}
		if err := s.Store.CheckTaskLarkRunTx(ctx, m, nodeID, m.Record.Current().RuntimeID, runID); err != nil {
			return nil, err
		}
		block(m.Record, in.Reason)
		if err := s.queueNotice(ctx, m, "blocked"); err != nil {
			return nil, err
		}
		n := m.Record.Current()
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
		old := m.Record.Current()
		if m.Record.View.Task.Status != "blocked" || old == nil || old.ID != in.ExpectedNodeRunID {
			return nil, conflict("任务或当前阻塞节点已变化")
		}
		if fenced, err := s.Store.TaskLarkRuntimeFencedTx(ctx, m, old.RuntimeID); err != nil || fenced {
			if err != nil {
				return nil, err
			}
			return nil, conflict("原实例正在凭证交接，不能并发重试节点")
		}
		if len(m.Record.View.Nodes) >= s.Options.MaxVisits {
			return nil, conflict("任务节点访问与重试记录达到上限，请取消任务")
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
		node := freshNode(id, step, inst, old.Visit, old.Attempt+1)
		m.Record.View.Nodes = append(m.Record.View.Nodes, node)
		setCurrent(m.Record, node)
		m.Record.View.Task.Reason = nil
		m.Record.View.Task.Status = "running"
		var prepared bool
		if e := m.Tx.QueryRow(ctx, `SELECT prepared FROM task_instances WHERE id=$1`, node.RuntimeID).Scan(&prepared); e != nil {
			return nil, e
		}
		if !prepared {
			m.Record.View.Task.Status = "preparing"
			return m.Record.View, nil
		}
		if err := s.prepareNode(ctx, m, in.Note); err != nil {
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
			return nil, conflict("任务已结束")
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
		if err := s.queueNotice(ctx, m, "cancelled"); err != nil {
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

func (s *Service) prepareNode(ctx context.Context, m *store.TaskMutation, note string) error {
	n := m.Record.Current()
	if n == nil {
		return conflict("当前节点不存在")
	}
	step, _ := m.Record.View.Workflow.Step(n.StepID)
	inputs, versions, err := workflow.ResolveInputs(step, m.Record.View.Request, successful(m.Record))
	if err != nil {
		block(m.Record, err.Error())
		if e := s.queueNotice(ctx, m, "blocked"); e != nil {
			return e
		}
		return m.Emit("node.updated", map[string]any{"node": n})
	}
	n.Inputs, n.InputVersions = inputs, versions
	data, _ := json.Marshal(inputs)
	text := fmt.Sprintf("你正在执行任务 %s 的节点 %s（运行 %s）。\n节点说明：%s\n输入：%s\n当前工作目录是按任务启动配置生成的干净副本，不包含常态目录积累的文件。先检查未读Inbox。完成后通过 loopctl 显式提交 output/edgeId；失败须说明原因，提问用 Inbox。\n", m.Record.View.Task.ID, n.StepID, n.ID, step.Description, string(data))
	if note != "" {
		text += "重试说明：" + note
	}
	if _, err = s.Store.EnqueueTaskTurn(ctx, m, text, "task_node", 1, nil); err != nil {
		return err
	}
	return m.Emit("node.updated", map[string]any{"node": n})
}

func (s *Service) closeQuestion(ctx context.Context, m *store.TaskMutation, n *taskmodel.NodeRun, reason string) error {
	if n.Question == nil || n.Question.Status != "open" {
		return nil
	}
	// 问题服务的关闭适配器在相邻契约接入时执行持久化关闭；节点视图保留历史。
	if s.Source == nil || s.Source.CloseQuestion == nil {
		return errors.New("统一问题关闭适配器未配置")
	}
	status, err := s.Source.CloseQuestion(ctx, m.Tx, n.Question.ID, reason)
	if err != nil {
		return err
	}
	switch status {
	case "closed", "answered", "expired":
	default:
		return errors.New("统一问题关闭后未返回有效终态")
	}
	n.Question.Status = status
	n.Question.CanAnswer = false
	return nil
}
func (s *Service) queueNotice(ctx context.Context, m *store.TaskMutation, kind string) error {
	var nodeID *uuid.UUID
	if n := m.Record.Current(); n != nil {
		nodeID = &n.ID
	}
	status := map[string]string{"blocked": "已阻塞", "completed": "已完成", "cancelled": "已取消"}[kind]
	text := fmt.Sprintf("任务 %s：%s。", m.Record.View.Task.Title, status)
	if m.Record.View.Task.Reason != nil {
		text += "；原因：" + *m.Record.View.Task.Reason
	}
	_, err := m.Tx.Exec(ctx, `INSERT INTO task_notifications(id,task_id,node_run_id,kind,message_id,text) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, uuid.Must(uuid.NewV7()), m.Record.View.Task.ID, nodeID, kind, uuid.Must(uuid.NewV7()), text)
	return err
}

// AvailableAgents只给当前Manager本项目选用所需的配置元数据，不读取任何凭证。
func (s *Service) AvailableAgents(ctx context.Context, p auth.Principal) (taskmodel.AgentList, error) {
	if p.Kind != auth.KindAgent {
		return taskmodel.AgentList{}, apperr.Forbidden("只有当前项目Manager可选用Agent")
	}
	a, err := s.Store.GetAgent(ctx, p.ID)
	if err != nil {
		return taskmodel.AgentList{}, err
	}
	if a.ProjectID == nil || !a.IsManager {
		return taskmodel.AgentList{}, apperr.Forbidden("只有当前项目Manager可选用Agent")
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
		return taskmodel.Page{}, apperr.Forbidden("只有当前项目Manager可查询项目任务")
	}
	a, e := s.Store.GetAgent(ctx, p.ID)
	if e != nil {
		return taskmodel.Page{}, e
	}
	if a.ProjectID == nil || !a.IsManager {
		return taskmodel.Page{}, apperr.Forbidden("只有当前项目Manager可查询项目任务")
	}
	return s.Store.ListTasks(ctx, *a.ProjectID, cursor, limit)
}
