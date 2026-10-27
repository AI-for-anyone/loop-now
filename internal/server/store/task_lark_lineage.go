package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

// BindTaskLarkTurnTx stamps an actual queued successor only from persisted
// message/sleep/strict lineage of this exact dedicated continuation node.
// It never selects a recent run. Called within claim before starting.
func (s *Store) BindTaskLarkTurnTx(ctx context.Context, m *TaskMutation, t TaskTurn) error {
	if t.ExecutionMode == "task_conversation" {
		return s.stampTaskLarkMemberTurnTx(ctx, m, t)
	}
	var action, initial uuid.UUID
	var revision int64
	err := m.Tx.QueryRow(ctx, `SELECT b.action_id,b.revision,b.continuation_turn_id FROM task_lark_credential_bindings b JOIN task_lark_switch_actions a ON a.task_id=b.task_id AND a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.status='succeeded' JOIN task_lark_binding_members p ON p.task_id=b.task_id AND p.revision=b.revision AND p.member_role='publisher' WHERE b.task_id=$1 AND b.continuation_node_run_id=$2 AND p.runtime_id=$3`, t.TaskID, t.NodeRunID, t.RuntimeID).Scan(&action, &revision, &initial)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.stampTaskLarkMemberTurnTx(ctx, m, t)
	}
	if err != nil {
		return err
	}
	valid := t.ID == initial
	if !valid {
		err = m.Tx.QueryRow(ctx, `SELECT
 (t.origin='inbox_reminder' AND (EXISTS(SELECT 1 FROM inbox_messages msg WHERE msg.id=t.trigger_inbox_message_id AND msg.to_runtime_id=t.runtime_id) OR EXISTS(SELECT 1 FROM task_node_inbox_run_links l JOIN task_node_inbox_messages msg ON msg.message_id=l.message_id WHERE l.run_id=t.id AND l.role='notification' AND msg.task_id=t.task_id AND msg.node_run_id=t.node_run_id AND msg.runtime_id=t.runtime_id AND msg.execution_mode='task_node' AND msg.not_executed_reason IS NULL)))
 OR (t.origin='console_node' AND EXISTS(SELECT 1 FROM task_console_message_lineage l WHERE l.run_id=t.id AND l.task_id=t.task_id AND l.node_run_id=t.node_run_id AND l.runtime_id=t.runtime_id AND l.execution_mode='task_node'))
 OR (t.origin='sleep_wake' AND EXISTS(SELECT 1 FROM agent_sleeps z JOIN task_turns source ON source.id=z.source_run_id JOIN task_instances i ON i.id=t.runtime_id WHERE z.id=t.wake_sleep_id AND z.task_id=t.task_id AND z.runtime_id=t.runtime_id AND z.node_run_id=t.node_run_id AND z.mode='task_node' AND z.state='woken' AND z.wake_run_id=t.id AND z.revision=i.sleep_revision AND source.task_id=t.task_id AND source.runtime_id=t.runtime_id AND source.node_run_id=t.node_run_id AND source.lark_switch_action_id=$2 AND source.lark_binding_revision=$3))
 FROM task_turns t WHERE t.id=$1 AND t.task_id=$4 AND t.runtime_id=$5 AND t.node_run_id=$6`, t.ID, action, revision, t.TaskID, t.RuntimeID, t.NodeRunID).Scan(&valid)
		if err != nil {
			return err
		}
	}
	if !valid {
		return apperr.Conflict(apperr.CodeTaskConflict, "恢复回合缺少原节点真实消息或sleep承接来源")
	}
	_, err = m.Tx.Exec(ctx, `UPDATE task_turns SET lark_switch_action_id=$2,lark_binding_revision=$3 WHERE id=$1`, t.ID, action, revision)
	return err
}

// Other workflow nodes and ordinary task conversations keep their original
// node-action rules while recording the fixed overlay actually selected for
// this member. Permission/AAD is still checked by Source before execution.
func (s *Store) stampTaskLarkMemberTurnTx(ctx context.Context, m *TaskMutation, t TaskTurn) error {
	var action uuid.UUID
	var revision int64
	err := m.Tx.QueryRow(ctx, `SELECT b.action_id,b.revision FROM task_lark_binding_members p JOIN task_lark_credential_bindings b ON b.task_id=p.task_id AND b.revision=p.revision JOIN task_lark_switch_actions a ON a.task_id=b.task_id AND a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.status='succeeded' WHERE p.task_id=$1 AND p.runtime_id=$2 ORDER BY b.revision DESC LIMIT 1`, m.Record.View.Task.ID, t.RuntimeID).Scan(&action, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = m.Tx.Exec(ctx, `UPDATE task_turns SET lark_switch_action_id=$2,lark_binding_revision=$3 WHERE id=$1 AND task_id=$4 AND runtime_id=$5`, t.ID, action, revision, m.Record.View.Task.ID, t.RuntimeID)
	return err
}

// CheckTaskLarkRunTx preserves ordinary-node compatibility, but a dedicated
// recovery node requires the supplied exact running turn and stored binding.
func (s *Store) CheckTaskLarkRunTx(ctx context.Context, m *TaskMutation, node, runtime, run uuid.UUID) error {
	var strict bool
	if err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_lark_credential_bindings WHERE task_id=$1 AND continuation_node_run_id=$2)`, m.Record.View.Task.ID, node).Scan(&strict); err != nil {
		return err
	}
	if !strict {
		return nil
	}
	if run == uuid.Nil {
		return apperr.Conflict(apperr.CodeTaskConflict, "恢复节点必须显式提供本轮准确runId")
	}
	var valid bool
	err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns t JOIN task_lark_credential_bindings b ON b.task_id=t.task_id AND b.continuation_node_run_id=t.node_run_id AND b.action_id=t.lark_switch_action_id AND b.revision=t.lark_binding_revision JOIN task_lark_switch_actions a ON a.task_id=b.task_id AND a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.status='succeeded' WHERE t.id=$1 AND t.task_id=$2 AND t.node_run_id=$3 AND t.runtime_id=$4 AND t.status='running' AND t.execution_mode='task_node')`, run, m.Record.View.Task.ID, node, runtime).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return apperr.Conflict(apperr.CodeTaskConflict, "runId不属于本轮恢复节点或已经失效")
	}
	return nil
}

// TaskLarkSubmittedByTurnTx is a settle check, not active-run authorization:
// the executor may already have finalized this exact turn as completed.
func (s *Store) TaskLarkSubmittedByTurnTx(ctx context.Context, m *TaskMutation, t TaskTurn) (bool, error) {
	var valid bool
	err := m.Tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM task_lark_credential_bindings b WHERE b.task_id=$1 AND b.continuation_node_run_id=$2) OR EXISTS(SELECT 1 FROM task_turns x JOIN task_lark_credential_bindings b ON b.task_id=x.task_id AND b.continuation_node_run_id=x.node_run_id AND b.action_id=x.lark_switch_action_id AND b.revision=x.lark_binding_revision WHERE x.id=$3 AND x.task_id=$1 AND x.node_run_id=$2 AND x.runtime_id=$4 AND x.lark_submitted AND x.execution_mode='task_node')`, t.TaskID, t.NodeRunID, t.ID, t.RuntimeID).Scan(&valid)
	return valid, err
}
