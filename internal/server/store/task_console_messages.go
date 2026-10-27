package store

import (
	"context"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
)

// TaskConsoleRecoverySourceTx captures the actual active node run BEFORE
// RecoverTaskTurns changes its status. Never infer causality from old failures.
func (s *Store) TaskConsoleRecoverySourceTx(ctx context.Context, m *TaskMutation) (uuid.UUID, error) {
	n := m.Record.Current()
	if n == nil || n.Status == "blocked" || m.Record.Ended() {
		return uuid.Nil, nil
	}
	var source uuid.UUID
	err := m.Tx.QueryRow(ctx, `SELECT id FROM task_turns WHERE task_id=$1 AND node_run_id=$2 AND runtime_id=$3 AND execution_mode='task_node' AND status IN ('starting','running','aborting') ORDER BY queue_seq LIMIT 1`, m.Record.View.Task.ID, n.ID, n.RuntimeID).Scan(&source)
	if err == pgx.ErrNoRows {
		return uuid.Nil, nil
	}
	return source, err
}

// TaskConsoleGuardTx is the single predicate for capability, POST and strict
// claim/sleep hooks. It reads original scope facts only; never Resolve/refresh.
func (s *Store) TaskConsoleGuardTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage) (string, error) {
	if m.Record.Ended() {
		return "task_ended", nil
	}
	n := m.Record.Current()
	if n == nil || n.ID != target.NodeRunID {
		return "node_changed", nil
	}
	if target.TaskID != m.Record.View.Task.ID || n.AgentID != target.AgentID || n.RuntimeID != target.RuntimeID {
		return "target_mismatch", nil
	}
	if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, target.RuntimeID); err != nil {
		return "", err
	} else if fenced {
		return "chat_unavailable", nil
	}
	var inst TaskInstance
	inst.ID = target.RuntimeID
	err := m.Tx.QueryRow(ctx, `SELECT task_id,agent_id,prepared,retired,session_state,session_file,snapshot FROM task_instances WHERE id=$1`, inst.ID).Scan(&inst.TaskID, &inst.AgentID, &inst.Prepared, &inst.Retired, &inst.SessionState, &inst.SessionFile, &inst.Snapshot)
	if err == pgx.ErrNoRows {
		return "instance_unavailable", nil
	}
	if err != nil {
		return "", err
	}
	if inst.TaskID != target.TaskID || inst.AgentID != target.AgentID || !inst.Prepared || inst.Retired || m.Record.View.Task.Status == "preparing" {
		return "instance_unavailable", nil
	}
	mode := "task_node"
	if n.Status == "blocked" {
		mode = "task_conversation"
	}
	if target.ExecutionMode != "" && target.ExecutionMode != mode {
		return "chat_unavailable", nil
	}
	if mode == "task_conversation" {
		eligible, e := s.TaskConsoleEligibleTx(ctx, m, n.ID)
		if e != nil {
			return "", e
		}
		if !eligible {
			return "chat_unavailable", nil
		}
		if inst.SessionState != "active" || inst.SessionFile == nil || *inst.SessionFile == "" {
			return "chat_unavailable", nil
		}
		f, e := os.Open(*inst.SessionFile)
		if e != nil {
			return "chat_unavailable", nil
		}
		info, e := f.Stat()
		f.Close()
		if e != nil || !info.Mode().IsRegular() {
			return "chat_unavailable", nil
		}
	}
	// Original captured configuration/credentials, never current Agent accounts.
	snap, e := taskruntime.Decode(inst.Snapshot)
	if e != nil || snap.Scope().TaskID() != target.TaskID || snap.Scope().AgentID() != target.AgentID {
		return "chat_unavailable", nil
	}
	cfg := snap.Configuration()
	if cfg.Provider == "" || cfg.ModelID == "" {
		return "chat_unavailable", nil
	}
	creds, e := snap.Credentials()
	if e != nil {
		return "chat_unavailable", nil
	}
	if b := creds.ModelAuth; b != nil {
		if b.AgentID != target.AgentID || b.Provider != cfg.Provider {
			return "chat_unavailable", nil
		}
		v, e := scanModelAuthVersion(m.Tx.QueryRow(ctx, `SELECT `+modelAuthVersionColumns+` FROM model_auth_versions WHERE id=$1`, b.VersionID))
		if e == pgx.ErrNoRows {
			return "chat_unavailable", nil
		}
		if e != nil {
			return "", e
		}
		if v.Provider != b.Provider || v.RefreshState == "unavailable" {
			return "chat_unavailable", nil
		}
		ownerOK := false
		if b.OwnerKind == "project" {
			ownerOK = b.ProjectID != nil && *b.ProjectID == m.Record.View.Task.ProjectID && b.AccountID != nil && v.ProjectAccountID != nil && *b.AccountID == *v.ProjectAccountID
		} else if b.OwnerKind == "" {
			ownerOK = v.ProjectAccountID == nil && v.AgentID == b.AgentID
			if !ownerOK {
				if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_model_legacy_bindings WHERE version_id=$1 AND source_agent_id=$2 AND provider=$3 AND project_id=$4 AND account_id=$5)`, v.ID, b.AgentID, b.Provider, m.Record.View.Task.ProjectID, v.ProjectAccountID).Scan(&ownerOK); e != nil {
					return "", e
				}
			}
		}
		if !ownerOK {
			return "chat_unavailable", nil
		}
		credential, e := modelcredential.Decode(v.Secret)
		if e != nil || credential.Kind() != v.AuthMethod {
			return "chat_unavailable", nil
		}
	} else {
		credential, e := modelcredential.LegacySingleKey(cfg.Provider, creds.Model)
		if e != nil || credential.Kind() == "" {
			return "chat_unavailable", nil
		}
	}
	return "", nil
}

func TaskConsoleConflict(m *TaskMutation, target TaskConsoleLineage, reason string) error {
	e := apperr.Conflict(apperr.CodeTaskConflict, "原任务节点或运行条件已变化，请保留原草稿并核对目标")
	e.Details = taskmodel.TaskNodeMessageConflictDetails{TaskID: target.TaskID, ExpectedNodeRunID: target.NodeRunID, CurrentNodeRunID: m.Record.View.Task.CurrentNodeRunID, ExpectedAgentID: target.AgentID, ExpectedRuntimeID: target.RuntimeID, TaskVersion: m.Record.View.Task.Version, Reason: reason}
	return e
}

// RecordTaskConsoleEligibilityTx is called only at the two reviewed block
// branches, in the same transaction that makes the current node blocked.
func (s *Store) RecordTaskConsoleEligibilityTx(ctx context.Context, m *TaskMutation, source uuid.UUID) error {
	n := m.Record.Current()
	if n == nil || n.Status != "blocked" || m.Record.Ended() || source == uuid.Nil {
		return apperr.Conflict(apperr.CodeTaskConflict, "执行异常来源不匹配")
	}
	tag, err := m.Tx.Exec(ctx, `INSERT INTO task_console_chat_eligibility(task_id,node_run_id,source_run_id)
 SELECT task_id,node_run_id,id FROM task_turns t WHERE id=$1 AND task_id=$2 AND node_run_id=$3 AND runtime_id=$4 AND execution_mode='task_node' AND status IN ('failed','interrupted')
 AND (error_code='server_restarted' OR (error_code IS DISTINCT FROM 'task_conflict' AND EXISTS(SELECT 1 FROM lifecycle_events e WHERE e.task_turn_id=t.id AND e.type='run.running')))
 ON CONFLICT(task_id,node_run_id) DO UPDATE SET source_run_id=EXCLUDED.source_run_id WHERE task_console_chat_eligibility.source_run_id=EXCLUDED.source_run_id`, source, m.Record.View.Task.ID, n.ID, n.RuntimeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return apperr.Conflict(apperr.CodeTaskConflict, "执行异常来源不匹配")
	}
	return nil
}

func (s *Store) TaskConsoleEligibleTx(ctx context.Context, m *TaskMutation, node uuid.UUID) (bool, error) {
	var eligible bool
	err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_console_chat_eligibility e JOIN task_turns t ON t.id=e.source_run_id WHERE e.task_id=$1 AND e.node_run_id=$2 AND t.task_id=e.task_id AND t.node_run_id=e.node_run_id AND t.execution_mode='task_node' AND t.status IN ('failed','interrupted'))`, m.Record.View.Task.ID, node).Scan(&eligible)
	return eligible, err
}

type TaskConsoleLineage struct {
	RunID, TaskID, NodeRunID, AgentID, RuntimeID uuid.UUID
	ExecutionMode                                string
}

func (s *Store) TaskConsoleLineageTx(ctx context.Context, tx pgx.Tx, run uuid.UUID) (TaskConsoleLineage, bool, error) {
	var l TaskConsoleLineage
	err := tx.QueryRow(ctx, `SELECT run_id,task_id,node_run_id,agent_id,runtime_id,execution_mode FROM task_console_message_lineage WHERE run_id=$1`, run).Scan(&l.RunID, &l.TaskID, &l.NodeRunID, &l.AgentID, &l.RuntimeID, &l.ExecutionMode)
	if err == pgx.ErrNoRows {
		return l, false, nil
	}
	return l, err == nil, err
}

func (s *Store) AttachTaskConsoleLineageTx(ctx context.Context, m *TaskMutation, turn TaskTurn, agent uuid.UUID, mode string) error {
	if mode != "task_node" && mode != "task_conversation" {
		return apperr.Conflict(apperr.CodeTaskConflict, "节点对话模式不匹配")
	}
	tag, err := m.Tx.Exec(ctx, `INSERT INTO task_console_message_lineage(run_id,task_id,node_run_id,agent_id,runtime_id,execution_mode)
 SELECT t.id,t.task_id,t.node_run_id,i.agent_id,t.runtime_id,$6 FROM task_turns t JOIN task_instances i ON i.id=t.runtime_id
 WHERE t.id=$1 AND t.task_id=$2 AND t.node_run_id=$3 AND t.runtime_id=$4 AND i.task_id=t.task_id AND i.agent_id=$5
 ON CONFLICT(run_id) DO UPDATE SET run_id=EXCLUDED.run_id WHERE task_console_message_lineage.task_id=EXCLUDED.task_id AND task_console_message_lineage.node_run_id=EXCLUDED.node_run_id AND task_console_message_lineage.agent_id=EXCLUDED.agent_id AND task_console_message_lineage.runtime_id=EXCLUDED.runtime_id AND task_console_message_lineage.execution_mode=EXCLUDED.execution_mode`, turn.ID, m.Record.View.Task.ID, turn.NodeRunID, turn.RuntimeID, agent, mode)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return apperr.Conflict(apperr.CodeTaskConflict, "节点对话来源不匹配")
	}
	return nil
}

// EnqueueTaskConsoleMessageTx does not use Inbox or merge reminders. The direct
// text, target, fixed mode, wake and exact interrupted runs share this commit.
func (s *Store) EnqueueTaskConsoleMessageTx(ctx context.Context, m *TaskMutation, target TaskConsoleLineage, text, whenBusy string) (taskmodel.TaskNodeMessageReceipt, []uuid.UUID, error) {
	var receipt taskmodel.TaskNodeMessageReceipt
	reason, e := s.TaskConsoleGuardTx(ctx, m, target)
	if e != nil {
		return receipt, nil, e
	}
	if reason != "" {
		return receipt, nil, TaskConsoleConflict(m, target, reason)
	}
	mode := "task_node"
	if m.Record.Current().Status == "blocked" {
		mode = "task_conversation"
	}
	priority := 1
	if whenBusy == "interrupt" {
		priority = 0
	}
	turn, e := scanTaskTurn(m.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,priority,text,request_id,execution_mode) VALUES($1,$2,$3,$4,$5,$6,'queued','console_node',$7,$8,$9,$10) RETURNING `+taskTurnColumns, uuid.Must(uuid.NewV7()), target.TaskID, target.RuntimeID, target.NodeRunID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), priority, text, taskRequestID(ctx, m, "console_node"), mode))
	if e != nil {
		return receipt, nil, e
	}
	if e = s.AttachTaskConsoleLineageTx(ctx, m, turn, target.AgentID, mode); e != nil {
		return receipt, nil, e
	}
	if e = s.taskLifecycle(ctx, m.Tx, turn, EventRunQueued, map[string]any{"origin": turn.Origin}); e != nil {
		return receipt, nil, e
	}
	if e = s.WakeSleepForWorkTx(ctx, m.Tx, m, target.AgentID, target.RuntimeID, turn.ID); e != nil {
		return receipt, nil, e
	}
	turn, e = scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1`, turn.ID))
	if e != nil {
		return receipt, nil, e
	}
	interrupted := []uuid.UUID{}
	if whenBusy == "interrupt" {
		rows, e := m.Tx.Query(ctx, `UPDATE task_turns SET status='aborting' WHERE task_id=$1 AND runtime_id=$2 AND node_run_id=$3 AND id<>$4 AND status IN ('starting','running') RETURNING `+taskTurnColumns, target.TaskID, target.RuntimeID, target.NodeRunID, turn.ID)
		if e != nil {
			return receipt, nil, e
		}
		old, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
		if e != nil {
			return receipt, nil, e
		}
		for _, t := range old {
			interrupted = append(interrupted, t.ID)
			if e = s.taskAbortEvent(ctx, m, t); e != nil {
				return receipt, nil, e
			}
			if e = m.Emit("message.created", taskMessage(t, false)); e != nil {
				return receipt, nil, e
			}
		}
	}
	m.Record.Current().RunIDs = append(m.Record.Current().RunIDs, turn.ID)
	if e = m.Emit("message.created", taskMessage(turn, false)); e != nil {
		return receipt, nil, e
	}
	receipt = taskmodel.TaskNodeMessageReceipt{TaskID: target.TaskID, NodeRunID: target.NodeRunID, AgentID: target.AgentID, RuntimeID: target.RuntimeID, RunID: turn.ID, Message: taskMessage(turn, false), Status: "queued"}
	return receipt, interrupted, nil
}

// GuardTaskConsoleTurnTx is used by claim and the final pre-execution check.
// Ordinary Inbox turns retain their old behavior when no strict lineage exists.
func (s *Store) GuardTaskConsoleTurnTx(ctx context.Context, m *TaskMutation, turn TaskTurn) (bool, bool, error) {
	l, strict, e := s.TaskConsoleLineageTx(ctx, m.Tx, turn.ID)
	if e != nil {
		return strict, false, e
	}
	if fenced, e := s.TaskLarkRuntimeFencedTx(ctx, m, turn.RuntimeID); e != nil || fenced {
		// Coordination is temporary, not evidence that an old target expired.
		return strict, false, e
	}
	if !strict {
		return false, true, nil
	}
	current, e := scanTaskTurn(m.Tx.QueryRow(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE id=$1 AND task_id=$2`, turn.ID, m.Record.View.Task.ID))
	if e != nil {
		return true, false, e
	}
	reason, e := s.TaskConsoleGuardTx(ctx, m, l)
	if e != nil {
		return true, false, e
	}
	if reason == "" && current.Origin == "sleep_wake" {
		var live bool
		if e := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_sleeps WHERE id=$1 AND state='woken' AND wake_run_id=$2 AND source_run_id IN (SELECT run_id FROM task_console_message_lineage) AND mode=$3)`, current.WakeSleepID, current.ID, l.ExecutionMode).Scan(&live); e != nil {
			return true, false, e
		}
		if !live {
			reason = "node_changed"
		}
	}
	if reason == "" && (current.Status == "queued" || current.Status == "starting" || current.Status == "running") {
		return true, true, nil
	}
	if current.Status == "queued" || current.Status == "starting" || current.Status == "running" || current.Status == "aborting" {
		code, message := apperr.CodeTaskConflict, "原节点或执行条件已变化，未向其他节点执行该消息"
		if _, e = s.UpdateTaskTurnTx(ctx, m, current.ID, "interrupted", current.Reply, &code, &message); e != nil {
			return true, false, e
		}
		m.Changed()
	}
	return true, false, nil
}

func (s *Store) GuardTaskConsoleDispatch(ctx context.Context, turn TaskTurn) (bool, error) {
	var strict bool
	if e := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_console_message_lineage WHERE run_id=$1)`, turn.ID).Scan(&strict); e != nil {
		return false, e
	}
	if !strict {
		return true, nil
	}
	valid := false
	_, e := s.MutateTask(ctx, turn.TaskID, nil, func(m *TaskMutation) (any, error) {
		m.Unchanged()
		_, ok, e := s.GuardTaskConsoleTurnTx(ctx, m, turn)
		valid = ok
		return nil, e
	})
	return valid, e
}

func (s *Store) ExpireTaskConsoleTurnsTx(ctx context.Context, m *TaskMutation, runtime uuid.UUID) error {
	rows, e := m.Tx.Query(ctx, `SELECT `+taskTurnColumns+` FROM task_turns WHERE task_id=$1 AND runtime_id=$2 AND status='queued' AND id IN (SELECT run_id FROM task_console_message_lineage) ORDER BY queue_seq`, m.Record.View.Task.ID, runtime)
	if e != nil {
		return e
	}
	turns, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskTurn, error) { return scanTaskTurn(r) })
	if e != nil {
		return e
	}
	for _, t := range turns {
		if _, _, e = s.GuardTaskConsoleTurnTx(ctx, m, t); e != nil {
			return e
		}
	}
	return s.ExpireTaskNodeInboxTx(ctx, m)
}

func (s *Store) HasTaskConsoleInterruptTx(ctx context.Context, m *TaskMutation, runtime, node uuid.UUID) (bool, error) {
	rows, e := m.Tx.Query(ctx, `SELECT l.run_id,l.task_id,l.node_run_id,l.agent_id,l.runtime_id,l.execution_mode FROM task_console_message_lineage l JOIN task_turns t ON t.id=l.run_id WHERE l.task_id=$1 AND l.runtime_id=$2 AND l.node_run_id=$3 AND l.execution_mode='task_node' AND t.status='queued' AND t.priority=0 ORDER BY t.queue_seq`, m.Record.View.Task.ID, runtime, node)
	if e != nil {
		return false, e
	}
	items, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TaskConsoleLineage, error) {
		var l TaskConsoleLineage
		e := r.Scan(&l.RunID, &l.TaskID, &l.NodeRunID, &l.AgentID, &l.RuntimeID, &l.ExecutionMode)
		return l, e
	})
	if e != nil {
		return false, e
	}
	for _, l := range items {
		reason, e := s.TaskConsoleGuardTx(ctx, m, l)
		if e != nil {
			return false, e
		}
		if reason == "" {
			return true, nil
		}
	}
	return false, nil
}

// TaskConsoleSleepSourceTx retains node ownership without changing the
// existing task_conversation mode or any ordinary Inbox sleep behavior.
func (s *Store) TaskConsoleSleepSourceTx(ctx context.Context, m *TaskMutation, turn TaskTurn) (TaskConsoleLineage, bool, error) {
	l, strict, e := s.TaskConsoleLineageTx(ctx, m.Tx, turn.ID)
	if e != nil || !strict {
		return l, strict, e
	}
	reason, e := s.TaskConsoleGuardTx(ctx, m, l)
	if e != nil {
		return l, true, e
	}
	if reason != "" {
		return l, true, sleepConflict()
	}
	return l, true, nil
}

func (s *Store) TaskConsoleSleepValidTx(ctx context.Context, tx pgx.Tx, m *TaskMutation, r Sleep) (bool, bool, error) {
	if m == nil {
		return false, true, nil
	}
	l, strict, e := s.TaskConsoleLineageTx(ctx, tx, r.SourceRunID)
	if e != nil || !strict {
		return strict, e == nil, e
	}
	if r.NodeRunID == nil || *r.NodeRunID != l.NodeRunID || r.RuntimeID == nil || *r.RuntimeID != l.RuntimeID || r.Mode != l.ExecutionMode {
		return true, false, nil
	}
	reason, e := s.TaskConsoleGuardTx(ctx, m, l)
	return true, reason == "", e
}

func (s *Store) InheritTaskConsoleLineageTx(ctx context.Context, m *TaskMutation, source uuid.UUID, turn TaskTurn) (bool, error) {
	l, strict, e := s.TaskConsoleLineageTx(ctx, m.Tx, source)
	if e != nil || !strict {
		return strict, e
	}
	reason, e := s.TaskConsoleGuardTx(ctx, m, l)
	if e != nil {
		return true, e
	}
	if reason != "" || turn.NodeRunID != l.NodeRunID || turn.RuntimeID != l.RuntimeID {
		return true, TaskConsoleConflict(m, l, "node_changed")
	}
	return true, s.AttachTaskConsoleLineageTx(ctx, m, turn, l.AgentID, l.ExecutionMode)
}
