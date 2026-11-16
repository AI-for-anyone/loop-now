package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/sleepmodel"
)

// Agent is the base configuration of a regular Agent.
type Agent struct {
	ThinkingLevel *string
	ServiceTier   *string
	ID            uuid.UUID
	ProjectID     *uuid.UUID // nil means the platform owns it
	IsManager     bool       // whether it is the Manager of its project
	Name          string
	ModelProvider string
	ModelID       string
	Prompt        string
	PromptVersion int64
	ConfigVersion int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
	OrgID         *uuid.UUID // organization of its project; empty for a platform Agent
	OrgName       string     // organization and project names of its project, for the address; empty for a platform Agent
	ProjectName   string
}

// Address returns the Agent's Inbox address; ok is false when the name does not satisfy the naming rules.
func (a Agent) Address() (string, bool) {
	return naming.AgentAddress(a.OrgName, a.ProjectName, a.Name, a.ProjectID == nil)
}

// AgentSession is the Agent's session record.
type AgentSession struct {
	SessionFile string // empty means there is no session yet
	State       string // none / active / missing
	Epoch       int
	UpdatedAt   time.Time
}

// AgentListItem is a list item: the configuration plus lightweight status.
type AgentListItem struct {
	Sleep         *sleepmodel.Record
	SleepRevision int64
	Agent
	Busy         bool
	SessionState string
}

// The set of statuses of a run in progress (consistent with the runs_one_active_per_agent index).
const activeRunStatuses = `('starting', 'running', 'aborting')`

// agentColumns refers to the table name agents (queries must not alias agents). The last four columns: whether it is
// the project's Manager, and the organization ID, organization name and project name of its project (empty for a
// platform Agent).
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

func scanAgent(row db.Scanner) (Agent, error) {
	var a Agent
	err := row.Scan(agentFields(&a)...)
	return a, err
}

func notFoundIfNoRows(err error, what string) error {
	if errors.Is(err, db.ErrNoRows) {
		return apperr.NotFound(what)
	}
	return err
}

// CreateAgent creates an Agent and its session record in the given scope. In a project scope the project must not be
// deleted; this is serialized with project deletion in write transactions, so an Agent cannot be created in a project
// that was just deleted.
func (s *Store) CreateAgent(ctx context.Context, scope Scope, name, provider, modelID, prompt string, modelOptions ...*string) (Agent, error) {
	var a Agent
	var level, tier *string
	if len(modelOptions) > 0 {
		level = modelOptions[0]
	}
	if len(modelOptions) > 1 {
		tier = modelOptions[1]
	}
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if !scope.Platform {
			if err := requireLiveProject(ctx, tx, scope.ProjectID); err != nil {
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

// GetAgent returns a non-deleted Agent.
func (s *Store) GetAgent(ctx context.Context, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(s.Pool.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id = $1 AND deleted_at IS NULL`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// ListAgents lists the non-deleted Agents in a scope, with whether each is busy and its session state.
func (s *Store) ListAgents(ctx context.Context, scope Scope) ([]AgentListItem, error) {
	return s.listAgents(ctx, `agents.project_id IS $1`, scope.projectArg())
}

// GetAgentListItem returns the list item of one non-deleted Agent.
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

// ListAgentsFiltered lists, by filter, the non-deleted Agents in all non-deleted projects and on the platform (used by
// the overview).
func (s *Store) ListAgentsFiltered(ctx context.Context, f ScopeFilter) ([]AgentListItem, error) {
	pid, platform := f.args()
	return s.listAgents(ctx, `($1 IS NULL OR agents.project_id = $1) AND (NOT $2 OR agents.project_id IS NULL)
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
	items, err := db.CollectRows(rows, func(row db.CollectableRow) (AgentListItem, error) {
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

// AgentPatch holds the fields to change on an Agent; nil means unchanged.
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

// UpdateAgent changes an Agent; any change other than the display name increments the config version.
func (s *Store) UpdateAgent(ctx context.Context, id uuid.UUID, p AgentPatch) (Agent, error) {
	var a Agent
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if err := lockAgentProjectTx(ctx, tx, id); err != nil {
			return err
		}
		cur, err := lockAgent(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.Prompt != nil {
			if p.ExpectedPromptVersion == nil || *p.ExpectedPromptVersion < 1 {
				return apperr.Invalid("expectedPromptVersion", "Must be a positive integer.")
			}
			if *p.ExpectedPromptVersion != cur.PromptVersion {
				return apperr.PreconditionFailed("The Agent prompt has changed. Read the latest text and review it.")
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
		if agentruntime.ForProvider(next.ModelProvider) != agentruntime.ForProvider(cur.ModelProvider) {
			if err := s.switchRuntimeTx(ctx, tx, id, cur.ModelProvider, next.ModelProvider); err != nil {
				return err
			}
		}
		inc := 0
		if bump {
			inc = 1
		}
		a, err = scanAgent(tx.QueryRow(ctx, `UPDATE agents SET name = $2, model_provider = $3, model_id = $4, prompt = $5,
				config_version = config_version + $6, prompt_version=prompt_version+$7, thinking_level=$8, service_tier=$9, updated_at = $10
			WHERE id = $1 RETURNING `+agentColumns, id, next.Name, next.ModelProvider, next.ModelID, next.Prompt, inc, promptInc, next.ThinkingLevel, next.ServiceTier, tx.Now()))
		return agentNameConflict(err, next.Name)
	})
	return a, err
}

func sameThinking(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// switchRuntimeTx is called when a model provider change switches the runtime (between Pi and Claude Code): the new
// runtime starts from a new session, and the old session is neither migrated nor deleted. The session is reset
// (session sequence number plus one) and any sleep in this session is cancelled, the same as a confirmed session
// restart; returns agent_not_idle when a run is in progress, because otherwise that run would record the old
// runtime's session back when it ends.
func (s *Store) switchRuntimeTx(ctx context.Context, tx *db.Tx, agentID uuid.UUID, from, to string) error {
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE agent_id = $1 AND status IN `+activeRunStatuses+`)`, agentID).Scan(&active); err != nil {
		return err
	}
	if active {
		return apperr.Conflict(apperr.CodeAgentNotIdle, "The Agent is replying. Wait for this reply to end before switching the runtime.")
	}
	if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "session_reopened"); err != nil {
		return err
	}
	var epoch int
	err := tx.QueryRow(ctx, `UPDATE agent_sessions SET session_file = NULL, state = 'none', epoch = epoch + 1, updated_at = $2
		WHERE agent_id = $1 RETURNING epoch`, agentID, tx.Now()).Scan(&epoch)
	if errors.Is(err, db.ErrNoRows) {
		return nil // no session row (should not happen; it exists from Agent creation): no session to reset
	}
	if err != nil {
		return err
	}
	return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionReset,
		Data: map[string]any{"fromEpoch": epoch - 1, "toEpoch": epoch, "reason": "runtime_changed",
			"fromRuntime": agentruntime.ForProvider(from), "toRuntime": agentruntime.ForProvider(to)}})
}

// AgentBusy reports whether the Agent has a run in progress.
func (s *Store) AgentBusy(ctx context.Context, id uuid.UUID) (bool, error) {
	var busy bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE agent_id = $1 AND status IN `+activeRunStatuses+`)`, id).Scan(&busy)
	return busy, err
}

// SoftDeleteAgent soft-deletes an idle Agent: sets deleted_at and deletes its encrypted credentials.
// Returns agent_not_idle when it has a run in progress or queued messages.
func (s *Store) SoftDeleteAgent(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
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
			return apperr.Conflict(apperr.CodeTaskNotIdle, "The Agent takes part in tasks that have not ended, so it cannot be deleted.")
		}
		if err := purgeAgentCredentials(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		if err := scrubAgentSnapshotsTx(ctx, tx, id); err != nil {
			return err
		}
		// If it is a project's Manager, clear that as well.
		if _, err := tx.Exec(ctx, `UPDATE projects SET manager_agent_id = NULL, updated_at = $2 WHERE manager_agent_id = $1`, id, tx.Now()); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, id, "owner_deleted"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE agents SET deleted_at = $2, updated_at = $2 WHERE id = $1`, id, tx.Now())
		return err
	})
}

// agentNameConflict turns a unique constraint violation for a duplicate name in the same scope into name_conflict.
func agentNameConflict(err error, name string) error {
	switch {
	case isUniqueViolation(err, "agents_name_per_project"):
		return apperr.Conflict(apperr.CodeNameConflict, "An Agent named \""+name+"\" already exists in this project.")
	case isUniqueViolation(err, "agents_name_platform"):
		return apperr.Conflict(apperr.CodeNameConflict, "A platform Agent named \""+name+"\" already exists.")
	}
	return err
}

// lockAgent reads a non-deleted Agent inside a write transaction. The name is kept from the PostgreSQL days, when it
// took a row lock; write transactions are now serialized, so no lock is needed anymore.
func lockAgent(ctx context.Context, tx *db.Tx, id uuid.UUID) (Agent, error) {
	a, err := scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id = $1 AND deleted_at IS NULL`, id))
	return a, notFoundIfNoRows(err, "Agent")
}

// ensureIdle returns agent_not_idle when the Agent has a run in progress or queued messages.
func ensureIdle(ctx context.Context, tx *db.Tx, id uuid.UUID) error {
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runs WHERE agent_id = $1 AND status IN ('queued', 'starting', 'running', 'aborting') AND NOT(status='queued' AND origin='sleep_wake'))`, id).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return apperr.Conflict(apperr.CodeAgentNotIdle, "The Agent is replying or has queued messages. Wait until it is idle, then try again.")
	}
	return nil
}

// bumpConfig increments the config version; the caller must already have locked the Agent with lockAgent.
func bumpConfig(ctx context.Context, tx *db.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE agents SET config_version = config_version + 1, updated_at = $2 WHERE id = $1`, id, tx.Now())
	return err
}

// GetAgentSession returns the Agent's session record.
func (s *Store) GetAgentSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	var file *string
	err := s.Pool.QueryRow(ctx, `SELECT session_file, state, epoch, updated_at FROM agent_sessions WHERE agent_id = $1`, agentID).
		Scan(&file, &sess.State, &sess.Epoch, &sess.UpdatedAt)
	if file != nil {
		sess.SessionFile = *file
	}
	return sess, notFoundIfNoRows(err, "Agent session")
}

// RecordSessionFile records a session file confirmed to be written to disk and sets the state to active.
func (s *Store) RecordSessionFile(ctx context.Context, agentID uuid.UUID, path string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE agent_sessions SET session_file = $2, state = 'active', updated_at = $3 WHERE agent_id = $1`, agentID, path, s.Pool.Now())
	return err
}

// MarkSessionMissing marks the session as missing (only takes effect for an active session) and records a
// session-missing event; runID is the run that discovered it was missing.
func (s *Store) MarkSessionMissing(ctx context.Context, agentID uuid.UUID, runID *uuid.UUID) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		var file *string
		var epoch int
		err := tx.QueryRow(ctx, `UPDATE agent_sessions SET state = 'missing', updated_at = $2 WHERE agent_id = $1 AND state = 'active'
			RETURNING session_file, epoch`, agentID, tx.Now()).Scan(&file, &epoch)
		if errors.Is(err, db.ErrNoRows) {
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

// ResetSession confirms a session restart: clears the session file, sets the state to none and increments the
// session sequence number.
// Returns agent_not_idle when a run is in progress.
func (s *Store) ResetSession(ctx context.Context, agentID uuid.UUID) (AgentSession, error) {
	var sess AgentSession
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "session_reopened"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE agent_sessions SET session_file = NULL, state = 'none', epoch = epoch + 1, updated_at = $2
			WHERE agent_id = $1 RETURNING state, epoch, updated_at`, agentID, tx.Now()).Scan(&sess.State, &sess.Epoch, &sess.UpdatedAt); err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionReset,
			Data: map[string]any{"fromEpoch": sess.Epoch - 1, "toEpoch": sess.Epoch}})
	})
	return sess, err
}

// ResumeSession switches, inside the idle gate, to a verified managed session, keeping the history and incrementing
// the epoch. expectedFile guards against the current pointer being changed by another maintenance operation after it
// was read; on failure the whole transaction is rolled back.
func (s *Store) ResumeSession(ctx context.Context, agentID uuid.UUID, expectedFile, targetFile string) (AgentSession, error) {
	var sess AgentSession
	var expectedFileArg any
	if expectedFile != "" {
		expectedFileArg = expectedFile
	}
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := lockAgent(ctx, tx, agentID); err != nil {
			return err
		}
		if err := ensureIdle(ctx, tx, agentID); err != nil {
			return err
		}
		if err := s.CancelSleepsTx(ctx, tx, nil, agentID, "session_reopened"); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE agent_sessions SET session_file = $3, state = 'active', epoch = epoch + 1, updated_at = $4
			WHERE agent_id = $1 AND session_file IS $2 RETURNING session_file, state, epoch, updated_at`, agentID, expectedFileArg, targetFile, tx.Now()).
			Scan(&sess.SessionFile, &sess.State, &sess.Epoch, &sess.UpdatedAt); errors.Is(err, db.ErrNoRows) {
			return apperr.Conflict(apperr.CodeConsoleCommandFailed, "The current session has changed. List the sessions again before resuming.")
		} else if err != nil {
			return err
		}
		return s.insertEvent(ctx, tx, LifecycleEvent{AgentID: agentID, Type: EventSessionReset,
			Data: map[string]any{"fromEpoch": sess.Epoch - 1, "toEpoch": sess.Epoch}})
	})
	return sess, err
}

// DeletedAgentIDs returns the soft-deleted Agents, so the executor can clean up their separate HOME at startup.
func (s *Store) DeletedAgentIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM agents WHERE deleted_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	return db.CollectRows(rows, db.RowTo[uuid.UUID])
}

// AgentProjectAny returns the Agent's project (nil for a platform Agent); deleted Agents are found too. Returns
// not_found when it does not exist.
func (s *Store) AgentProjectAny(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	var pid *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, id).Scan(&pid)
	return pid, notFoundIfNoRows(err, "Agent")
}
