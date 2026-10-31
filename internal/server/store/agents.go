package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/sleepmodel"
)

// Agent 是常态 Agent 的基础配置。
type Agent struct {
	ThinkingLevel *string
	ServiceTier   *string
	ID            uuid.UUID
	ProjectID     *uuid.UUID // 为空表示平台持有
	IsManager     bool       // 是否担任所属项目的 Manager
	Name          string
	ModelProvider string
	ModelID       string
	Prompt        string
	PromptVersion int64
	ConfigVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	OrgID         *uuid.UUID // 所属项目的组织；平台 Agent 为空
	OrgName       string     // 所属项目的组织名与项目名，组成地址用；平台 Agent 为空
	ProjectName   string
}

// Address 返回 Agent 的 Inbox 地址；名称不满足命名规则时 ok 为 false。
func (a Agent) Address() (string, bool) {
	return naming.AgentAddress(a.OrgName, a.ProjectName, a.Name, a.ProjectID == nil)
}

// AgentSession 是 Agent 的会话记录。
type AgentSession struct {
	SessionFile string // 为空表示还没有会话
	State       string // none / active / missing
	Epoch       int
	UpdatedAt   time.Time
}

// AgentListItem 是列表项：配置加轻量状态。
type AgentListItem struct {
	Sleep         *sleepmodel.Record
	SleepRevision int64
	Agent
	Busy         bool
	SessionState string
}

// 进行中执行的状态集合（与 runs_one_active_per_agent 索引一致）。
const activeRunStatuses = `('starting', 'running', 'aborting')`

// agentColumns 引用表名 agents（查询中不能给 agents 起别名）。最后四列：是否担任项目 Manager，
// 所属项目的组织 ID、组织名与项目名（平台 Agent 为空）。
const agentColumns = `agents.id, agents.project_id, agents.name, agents.model_provider, agents.model_id, agents.prompt,
	agents.config_version, agents.created_at, agents.updated_at, agents.prompt_version,
	EXISTS (SELECT 1 FROM projects mp WHERE mp.manager_agent_id = agents.id),
	(SELECT ap.organization_id FROM projects ap WHERE ap.id = agents.project_id),
	coalesce((SELECT ao.name FROM projects ap JOIN organizations ao ON ao.id = ap.organization_id WHERE ap.id = agents.project_id), ''),
	coalesce((SELECT ap.name FROM projects ap WHERE ap.id = agents.project_id), ''), agents.thinking_level, agents.service_tier`

func agentFields(a *Agent) []any {
	return []any{&a.ID, &a.ProjectID, &a.Name, &a.ModelProvider, &a.ModelID, &a.Prompt, &a.ConfigVersion, &a.CreatedAt, &a.UpdatedAt, &a.PromptVersion,
		&a.IsManager, &a.OrgID, &a.OrgName, &a.ProjectName, &a.ThinkingLevel, &a.ServiceTier}
}

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(agentFields(&a)...)
	return a, err
}

func notFoundIfNoRows(err error, what string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound(what)
	}
	return err
}

// CreateAgent 在指定范围内创建 Agent 及其会话记录。项目范围时，项目必须未删除；
// 以 FOR SHARE 锁住项目行，与删除项目串行化，不会在刚删除的项目中建出 Agent。
func (s *Store) CreateAgent(ctx context.Context, scope Scope, name, provider, modelID, prompt string, modelOptions ...*string) (Agent, error) {
	var a Agent
	var level, tier *string
	if len(modelOptions) > 0 {
		level = modelOptions[0]
	}
	if len(modelOptions) > 1 {
		tier = modelOptions[1]
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if !scope.Platform {
			if err := lockLiveProject(ctx, tx, scope.ProjectID, "FOR SHARE"); err != nil {
				return err
			}
		}
		var err error
		a, err = scanAgent(tx.QueryRow(ctx, `INSERT INTO agents (id, project_id, name, model_provider, model_id, prompt, thinking_level, service_tier)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+agentColumns,
			uuid.Must(uuid.NewV7()), scope.projectArg(), name, provider, modelID, prompt, level, tier))
		if err != nil {
			return agentNameConflict(err, name)
		}
		_, err = tx.Exec(ctx, `INSERT INTO agent_sessions (agent_id) VALUES ($1)`, a.ID)
		return err
	})
	return a, err
}

// GetAgent 返回未删除的 Agent。
func (s *Store) GetAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id = $1 AND deleted_at IS NULL`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// ListAgents 列出某个范围内未删除的 Agent，附带是否忙碌与会话状态。
func (s *Store) ListAgents(ctx context.Context, scope Scope) ([]AgentListItem, error) {
	return s.listAgents(ctx, `agents.project_id IS NOT DISTINCT FROM $1`, scope.projectArg())
}

// GetAgentListItem 返回一个未删除 Agent 的列表项。
func (s *Store) GetAgentListItem(ctx context.Context, id uuid.UUID) (AgentListItem, error) {
	items, err := s.listAgents(ctx, `agents.id = $1`, id)
	if err != nil {
		return AgentListItem{}, err
	}
	if len(items) == 0 {
		return AgentListItem{}, apperr.NotFound("Agent")
	}
	return items[0], nil
}

// ListAgentsFiltered 按筛选条件列出所有未删除项目与平台中未删除的 Agent（概览使用）。
func (s *Store) ListAgentsFiltered(ctx context.Context, f ScopeFilter) ([]AgentListItem, error) {
	pid, platform := f.args()
	return s.listAgents(ctx, `($1::uuid IS NULL OR agents.project_id = $1) AND (NOT $2 OR agents.project_id IS NULL)
		AND (agents.project_id IS NULL OR EXISTS (SELECT 1 FROM projects lp WHERE lp.id = agents.project_id AND lp.deleted_at IS NULL))`, pid, platform)
}

func (s *Store) listAgents(ctx context.Context, where string, args ...any) ([]AgentListItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+agentColumns+`,
			EXISTS (SELECT 1 FROM runs r WHERE r.agent_id = agents.id AND r.status IN `+activeRunStatuses+`),
			coalesce(sess.state, 'none')
		FROM agents LEFT JOIN agent_sessions sess ON sess.agent_id = agents.id
		WHERE agents.deleted_at IS NULL AND `+where+`
		ORDER BY agents.created_at, agents.id`, args...)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (AgentListItem, error) {
		var it AgentListItem
		err := row.Scan(append(agentFields(&it.Agent), &it.Busy, &it.SessionState)...)
		return it, err
	})
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Sleep, items[i].SleepRevision, err = s.ActiveSleepProjection(ctx, items[i].ID, uuid.Nil)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

// AgentPatch 是修改 Agent 的字段；nil 表示不改。
type AgentPatch struct {
	ServiceTier           *string
	ServiceTierSet        bool
	ThinkingLevel         *string
	ThinkingLevelSet      bool
	Validate              func(Agent) error
	ExpectedPromptVersion *int64
	Name                  *string
	ModelProvider         *string
	ModelID               *string
	Prompt                *string
}

// UpdateAgent 修改 Agent；除显示名称外的修改会递增配置版本。
func (s *Store) UpdateAgent(ctx context.Context, id uuid.UUID, p AgentPatch) (Agent, error) {
	var a Agent
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := lockAgentProjectTx(ctx, tx, id); err != nil {
			return err
		}
		cur, err := lockAgent(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.Prompt != nil {
			if p.ExpectedPromptVersion == nil || *p.ExpectedPromptVersion < 1 {
				return apperr.Invalid("expectedPromptVersion", "须提供正整数Prompt编辑版本")
			}
			if *p.ExpectedPromptVersion != cur.PromptVersion {
				return apperr.PreconditionFailed("Agent Prompt已改变，请读取最新文本并核对")
			}
		}
		next := cur
		bump := false
		promptInc := 0
		if p.Name != nil {
			next.Name = *p.Name
		}
		if p.ModelProvider != nil && *p.ModelProvider != cur.ModelProvider {
			next.ModelProvider, bump = *p.ModelProvider, true
		}
		if p.ModelID != nil && *p.ModelID != cur.ModelID {
			next.ModelID, bump = *p.ModelID, true
		}
		if p.Prompt != nil && *p.Prompt != cur.Prompt {
			next.Prompt, bump = *p.Prompt, true
			promptInc = 1
		}
		if p.ThinkingLevelSet && !sameThinking(cur.ThinkingLevel, p.ThinkingLevel) {
			next.ThinkingLevel, bump = p.ThinkingLevel, true
		}
		if p.ServiceTierSet && !sameThinking(cur.ServiceTier, p.ServiceTier) {
			next.ServiceTier, bump = p.ServiceTier, true
		}
		if p.Validate != nil {
			if err := p.Validate(next); err != nil {
				return err
			}
		}
		inc := 0
		if bump {
			inc = 1
		}
		a, err = scanAgent(tx.QueryRow(ctx, `UPDATE agents SET name = $2, model_provider = $3, model_id = $4, prompt = $5,
				config_version = config_version + $6, prompt_version=prompt_version+$7, thinking_level=$8, service_tier=$9, updated_at = now()
			WHERE id = $1 RETURNING `+agentColumns, id, next.Name, next.ModelProvider, next.ModelID, next.Prompt, inc, promptInc, next.ThinkingLevel, next.ServiceTier))
		return agentNameConflict(err, next.Name)
	})
	return a, err
}

func sameThinking(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// AgentBusy 判断 Agent 是否有进行中的执行。
func (s *Store) AgentBusy(ctx context.Context, id uuid.UUID) (bool, error) {
	var busy bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE agent_id = $1 AND status IN `+activeRunStatuses+`)`, id).Scan(&busy)
	return busy, err
}

// SoftDeleteAgent 软删除空闲的 Agent：设置 deleted_at 并删除其凭证密文。
// 有进行中的执行或排队消息时返回 agent_not_idle。
func (s *Store) SoftDeleteAgent(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// 加锁顺序统一为“先项目、后 Agent”（与删除项目、指定 Manager、创建 Agent 一致），避免与它们互相等待。
		if _, err := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id = (SELECT project_id FROM agents WHERE id = $1) FOR UPDATE`, id); err != nil {
			return err
		}
		if _, err := lockAgent(ctx, tx, id); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, id); err != nil {
			return err
		}
		var taskActive bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_instances i JOIN task_records t ON t.id=i.task_id WHERE i.agent_id=$1 AND t.ended_at IS NULL)`, id).Scan(&taskActive); err != nil {
			return err
		}
		if taskActive {
			return apperr.Conflict(apperr.CodeTaskNotIdle, "Agent参与的任务尚未结束，不能删除")
		}
		if err := purgeAgentCredentials(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		if err := scrubAgentSnapshotsTx(ctx, tx, id); err != nil {
			return err
		}
		// 担任项目 Manager 时，同时取消。
		if _, err := tx.Exec(ctx, `UPDATE projects SET manager_agent_id = NULL, updated_at = now() WHERE manager_agent_id = $1`, id); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, id, "owner_deleted"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE agents SET deleted_at = now(), updated_at = now() WHERE id = $1`, id)
		return err
	})
}

// agentNameConflict 把同一范围内重名的唯一约束冲突转为 name_conflict。
func agentNameConflict(err error, name string) error {
	switch {
	case isUniqueViolation(err, "agents_name_per_project"):
		return apperr.Conflict(apperr.CodeNameConflict, "项目内已有名为 "+name+" 的 Agent")
	case isUniqueViolation(err, "agents_name_platform"):
		return apperr.Conflict(apperr.CodeNameConflict, "平台中已有名为 "+name+" 的 Agent")
	}
	return err
}

// lockAgent 以 FOR UPDATE 锁定未删除的 Agent 行，与发送消息、删除等操作串行化。
func lockAgent(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// ensureIdle 在 Agent 有进行中的执行或排队消息时返回 agent_not_idle。
func ensureIdle(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE agent_id = $1 AND status IN ('queued', 'starting', 'running', 'aborting') AND NOT(status='queued' AND origin='sleep_wake'))`, id).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return apperr.Conflict(apperr.CodeAgentNotIdle, "Agent 正在回复或还有排队消息，请等它空闲后再操作")
	}
	return nil
}

// bumpConfig 递增配置版本；调用前须已用 lockAgent 锁定该 Agent。
func bumpConfig(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE agents SET config_version = config_version + 1, updated_at = now() WHERE id = $1`, id)
	return err
}

// GetAgentSession 返回 Agent 的会话记录。
func (s *Store) GetAgentSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	var file *string
	err := s.Pool.QueryRow(ctx, `SELECT session_file, state, epoch, updated_at FROM agent_sessions WHERE agent_id = $1`, agentID).
		Scan(&file, &sess.State, &sess.Epoch, &sess.UpdatedAt)
	if file != nil {
		sess.SessionFile = *file
	}
	return sess, notFoundIfNoRows(err, "Agent 会话")
}

// RecordSessionFile 记录已确认写入磁盘的会话文件，状态设为 active。
func (s *Store) RecordSessionFile(ctx context.Context, agentID uuid.UUID, path string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agent_sessions SET session_file = $2, state = 'active', updated_at = now() WHERE agent_id = $1`, agentID, path)
	return err
}

// MarkSessionMissing 把会话标记为丢失（只对 active 会话生效），并记录会话丢失事件；runID 为发现丢失的那次执行。
func (s *Store) MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var file *string
		var epoch int
		err := tx.QueryRow(ctx, `UPDATE agent_sessions SET state = 'missing', updated_at = now() WHERE agent_id = $1 AND state = 'active'
			RETURNING session_file, epoch`, agentID).Scan(&file, &epoch)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		data := map[string]any{"epoch": epoch}
		if file != nil {
			data["sessionFile"] = *file
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, RunID: runID, Type: EventSessionMissing, Data: data})
	})
}

// ResetSession 确认重开会话：清除会话文件、状态设为 none、会话序号加一。
// 有进行中的执行时返回 agent_not_idle。
func (s *Store) ResetSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "session_reopened"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE agent_sessions SET session_file = NULL, state = 'none', epoch = epoch + 1, updated_at = now()
			WHERE agent_id = $1 RETURNING state, epoch, updated_at`, agentID).Scan(&sess.State, &sess.Epoch, &sess.UpdatedAt); err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionReset,
			Data: map[string]any{"fromEpoch": sess.Epoch - 1, "toEpoch": sess.Epoch}})
	})
	return sess, err
}

// ResumeSession 在空闲门禁内切换到已验证的受管会话，保留历史并递增 epoch。
// expectedFile 防止读取当前指针后又被其他维护操作改变；失败时整个事务回滚。
func (s *Store) ResumeSession(ctx context.Context, agentID uuid.UUID, expectedFile, targetFile string) (AgentSession, error) {
	var sess AgentSession
	var expectedFileArg any
	if expectedFile != "" {
		expectedFileArg = expectedFile
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "session_reopened"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE agent_sessions SET session_file = $3, state = 'active', epoch = epoch + 1, updated_at = now()
			WHERE agent_id = $1 AND session_file IS NOT DISTINCT FROM $2 RETURNING session_file, state, epoch, updated_at`, agentID, expectedFileArg, targetFile).
			Scan(&sess.SessionFile, &sess.State, &sess.Epoch, &sess.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
			return apperr.Conflict(apperr.CodeConsoleCommandFailed, "当前会话已变化，请重新列出后再恢复")
		} else if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionReset,
			Data: map[string]any{"fromEpoch": sess.Epoch - 1, "toEpoch": sess.Epoch}})
	})
	return sess, err
}

// DeletedAgentIDs 返回已软删除的 Agent，供执行器启动时清理其独立 HOME。
func (s *Store) DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM agents WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// AgentProjectAny 返回 Agent 的所属项目（平台 Agent 为 nil），已删除的 Agent 也能查到；不存在时返回 not_found。
func (s *Store) AgentProjectAny(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	var pid *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, id).Scan(&pid)
	return pid, notFoundIfNoRows(err, "Agent")
}
