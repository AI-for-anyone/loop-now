package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/taskmodel"
)

// PublishTaskLarkSwitchTx is the persistence half of final phase C. The caller
// must still own both real executor handoff leases and native consumer fences.
// It must not be invoked when OS handoff or a previous COMMIT is uncertain.
// Receipt is the separately frozen public projection, never a credential.
func (s *Store) PublishTaskLarkSwitchTx(ctx context.Context, m *TaskMutation, actor string, action uuid.UUID, next taskmodel.NodeRun, turnID uuid.UUID, prompt string, receipt json.RawMessage) (int64, bool, error) {
	var state string
	var reservedVersion int64
	var originalNode uuid.UUID
	var raw []byte
	err := m.Tx.QueryRow(ctx, `SELECT status,reservation_task_version,node_run_id,captured_request FROM task_lark_switch_actions WHERE actor_key=$1 AND action_id=$2 AND task_id=$3 FOR UPDATE`, actor, action, m.Record.View.Task.ID).Scan(&state, &reservedVersion, &originalNode, &raw)
	if err != nil {
		return 0, false, notFoundIfNoRows(err, "Lark切换动作")
	}
	if state == "succeeded" {
		var revision int64
		err := m.Tx.QueryRow(ctx, `SELECT revision FROM task_lark_credential_bindings WHERE actor_key=$1 AND action_id=$2 AND task_id=$3`, actor, action, m.Record.View.Task.ID).Scan(&revision)
		return revision, false, err
	}
	conflict := func() (int64, bool, error) {
		return 0, false, apperr.Conflict("task_conflict", "原切换预留或节点条件已变化")
	}
	old := m.Record.Current()
	if (state != "pending" && state != "handoff") || old == nil || old.ID != originalNode || old.Status != "blocked" || m.Record.View.Task.Status != "blocked" || m.Record.Ended() || m.Record.View.Task.Version != reservedVersion {
		return conflict()
	}
	var capture TaskLarkSwitchCapture
	if json.Unmarshal(raw, &capture) != nil || len(capture.Members) != 2 {
		return conflict()
	}
	slices.SortFunc(capture.Members, func(a, b TaskLarkMemberCapture) int { return bytes.Compare(a.RuntimeID[:], b.RuntimeID[:]) })
	if capture.Members[0].RuntimeID == capture.Members[1].RuntimeID || capture.Members[0].Role == capture.Members[1].Role {
		return conflict()
	}
	var wire map[string]json.RawMessage
	if json.Unmarshal(receipt, &wire) != nil || wire == nil || turnID == uuid.Nil || prompt == "" {
		return conflict()
	}
	if next.ID == uuid.Nil || next.ID == old.ID || next.TaskID != old.TaskID || next.AgentID != old.AgentID || next.RuntimeID != old.RuntimeID || next.StepID != old.StepID || next.Visit != old.Visit || next.Attempt != old.Attempt+1 || next.Output != nil || next.Question != nil || len(next.RunIDs) != 1 || next.RunIDs[0] != turnID {
		return conflict()
	}
	for _, expected := range capture.Members {
		var fenced bool
		if err := m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_lark_switch_fences WHERE task_id=$1 AND actor_key=$2 AND action_id=$3 AND runtime_id=$4 AND agent_id=$5 AND member_role=$6)`, m.Record.View.Task.ID, actor, action, expected.RuntimeID, expected.AgentID, expected.Role).Scan(&fenced); err != nil {
			return 0, false, err
		}
		if !fenced {
			return conflict()
		}
		current, err := s.CaptureTaskLarkMemberTx(ctx, m, TaskLarkFenceMember{AgentID: expected.AgentID, RuntimeID: expected.RuntimeID, Role: expected.Role})
		if err != nil {
			return 0, false, err
		}
		if current.BaseSnapshotSHA != expected.BaseSnapshotSHA || current.PreviousRevision != expected.PreviousRevision || current.Prepared != expected.Prepared || !sameLarkSession(current.SessionFile, expected.SessionFile) {
			return conflict()
		}
		var busy bool
		if err = m.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns t WHERE runtime_id=$1 AND (status IN ('starting','running','aborting') OR (status='queued' AND NOT (origin='sleep_wake' AND EXISTS(SELECT 1 FROM agent_sleeps z WHERE z.timer_run_id=t.id AND z.state='sleeping')))))`, expected.RuntimeID).Scan(&busy); err != nil {
			return 0, false, err
		}
		if busy {
			return conflict()
		}
	}
	target, err := s.CaptureTaskLarkTargetTx(ctx, m, capture.Target.AccountID, capture.Target.VersionID, capture.Target.AccountRevision)
	if err != nil {
		return 0, false, err
	}
	if target.Identity.Brand != capture.Target.Identity.Brand || target.Identity.AppID != capture.Target.Identity.AppID || target.Identity.OpenID != capture.Target.Identity.OpenID || target.RuntimeVariantVersion != capture.Target.RuntimeVariantVersion {
		return conflict()
	}
	var revision int64
	if err = m.Tx.QueryRow(ctx, `SELECT coalesce(max(revision),0)+1 FROM task_lark_credential_bindings WHERE task_id=$1`, m.Record.View.Task.ID).Scan(&revision); err != nil {
		return 0, false, err
	}
	if revision != capture.ExpectedBindingRevision+1 {
		return conflict()
	}
	if old.Question != nil && old.Question.Status == "open" {
		status, err := CloseInboxQuestion(ctx, m.Tx, old.Question.ID, "task_retry")
		if err != nil {
			return 0, false, err
		}
		old.Question.Status, old.Question.CanAnswer = status, false
	}
	// Invalidate only the two named members, never the task's other runtimes.
	for _, member := range capture.Members {
		rows, err := m.Tx.Query(ctx, `SELECT `+sleepColumns+` FROM agent_sleeps WHERE task_id=$1 AND runtime_id=$2 AND state IN ('requested','sleeping','woken') ORDER BY id FOR UPDATE`, m.Record.View.Task.ID, member.RuntimeID)
		if err != nil {
			return 0, false, err
		}
		sleeps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Sleep, error) { return scanSleep(row) })
		if err != nil {
			return 0, false, err
		}
		for i := range sleeps {
			if err = s.cancelSleepTx(ctx, m.Tx, m, &sleeps[i], "node_changed"); err != nil {
				return 0, false, err
			}
		}
	}
	now := time.Now().UTC()
	if old.EndedAt == nil {
		old.EndedAt = &now
	}
	if err = m.Emit("node.updated", map[string]any{"node": old}); err != nil {
		return 0, false, err
	}
	turn, err := scanTaskTurn(m.Tx.QueryRow(ctx, `INSERT INTO task_turns(id,task_id,runtime_id,node_run_id,user_message_id,assistant_message_id,status,origin,execution_mode,text,lark_binding_revision,lark_switch_action_id,request_id) VALUES($1,$2,$3,$4,$5,$6,'queued','task_node','task_node',$7,$8,$9,$10) RETURNING `+taskTurnColumns, turnID, next.TaskID, next.RuntimeID, next.ID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), prompt, revision, action, taskRequestID(ctx, m, "task_node")))
	if err != nil {
		return 0, false, err
	}
	_, err = m.Tx.Exec(ctx, `INSERT INTO task_lark_credential_bindings(task_id,revision,project_id,account_id,version_id,app_id,user_id,brand,runtime_variant_version,actor_key,action_id,continuation_node_run_id,continuation_turn_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, next.TaskID, revision, m.Record.View.Task.ProjectID, target.AccountID, target.VersionID, target.Identity.AppID, target.Identity.OpenID, target.Identity.Brand, target.RuntimeVariantVersion, actor, action, next.ID, turnID)
	if err != nil {
		return 0, false, err
	}
	for _, member := range capture.Members {
		if _, err = m.Tx.Exec(ctx, `INSERT INTO task_lark_binding_members(task_id,revision,runtime_id,agent_id,member_role,previous_revision,base_snapshot_sha) VALUES($1,$2,$3,$4,$5,$6,$7)`, next.TaskID, revision, member.RuntimeID, member.AgentID, member.Role, member.PreviousRevision, member.BaseSnapshotSHA); err != nil {
			return 0, false, err
		}
	}
	next.Sleep = nil
	if err = m.Tx.QueryRow(ctx, `SELECT sleep_revision FROM task_instances WHERE id=$1`, next.RuntimeID).Scan(&next.SleepRevision); err != nil {
		return 0, false, err
	}
	m.Record.View.Nodes = append(m.Record.View.Nodes, next)
	m.Record.View.Task.CurrentNodeRunID = &next.ID
	m.Record.View.Task.CurrentStepID = &next.StepID
	m.Record.View.Task.CurrentAgentID = &next.AgentID
	m.Record.View.Task.Status, m.Record.View.Task.Reason = "running", nil
	if err = s.taskLifecycle(ctx, m.Tx, turn, EventRunQueued, map[string]any{"origin": "task_node", "larkBindingRevision": revision, "sourceActionId": action}); err != nil {
		return 0, false, err
	}
	if err = m.Emit("message.created", taskMessage(turn, false)); err != nil {
		return 0, false, err
	}
	if err = m.Emit("node.updated", map[string]any{"node": next}); err != nil {
		return 0, false, err
	}
	tag, err := m.Tx.Exec(ctx, `UPDATE task_lark_switch_actions SET status='succeeded',receipt=$4,finished_at=clock_timestamp() WHERE actor_key=$1 AND action_id=$2 AND task_id=$3 AND status IN ('pending','handoff')`, actor, action, next.TaskID, receipt)
	if err != nil {
		return 0, false, err
	}
	if tag.RowsAffected() != 1 {
		return 0, false, errors.New("Lark切换预留未完成")
	}
	if _, err = m.Tx.Exec(ctx, `DELETE FROM task_lark_switch_fences WHERE actor_key=$1 AND action_id=$2 AND task_id=$3`, actor, action, next.TaskID); err != nil {
		return 0, false, err
	}
	m.Changed()
	return revision, true, nil
}

func sameLarkSession(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
