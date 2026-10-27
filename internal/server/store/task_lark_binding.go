package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
)

// TaskLarkEffectiveBindingTx resolves only a successfully committed, explicit
// member overlay. The original snapshot must already have passed AAD/permission
// checks. Non-members retain revision zero; this never follows account current.
func (s *Store) TaskLarkEffectiveBindingTx(ctx context.Context, m *TaskMutation, member TaskLarkMemberCapture) (*clicredential.LarkBinding, int64, error) {
	if fenced, err := s.TaskLarkRuntimeFencedTx(ctx, m, member.RuntimeID); err != nil {
		return nil, 0, err
	} else if fenced {
		return nil, 0, apperr.Conflict("task_conflict", "任务实例正在凭证交接")
	}
	var binding clicredential.LarkBinding
	var revision int64
	var sha string
	err := m.Tx.QueryRow(ctx, `SELECT b.project_id,b.account_id,b.version_id,b.brand,b.app_id,b.user_id,b.runtime_variant_version,p.agent_id,p.base_snapshot_sha,b.revision FROM task_lark_binding_members p JOIN task_lark_credential_bindings b ON b.task_id=p.task_id AND b.revision=p.revision JOIN task_lark_switch_actions a ON a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.task_id=b.task_id AND a.status='succeeded' WHERE p.task_id=$1 AND p.runtime_id=$2 ORDER BY b.revision DESC LIMIT 1`, m.Record.View.Task.ID, member.RuntimeID).Scan(&binding.ProjectID, &binding.AccountID, &binding.VersionID, &binding.Brand, &binding.AppID, &binding.OpenID, &binding.RuntimeVariantVersion, &binding.AgentID, &sha, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if binding.ProjectID != m.Record.View.Task.ProjectID || binding.AgentID != member.AgentID || sha != member.BaseSnapshotSHA || !binding.Valid() {
		return nil, 0, apperr.Conflict("task_conflict", "任务覆盖与原授权作用域不匹配")
	}
	return &binding, revision, nil
}

// TaskLarkMemberRevisionTx is an A/C CAS check, not credential resolution: it
// may inspect the prior committed revision while the same action holds a fence.
func (s *Store) TaskLarkMemberRevisionTx(ctx context.Context, m *TaskMutation, runtime uuid.UUID) (int64, error) {
	var revision int64
	err := m.Tx.QueryRow(ctx, `SELECT coalesce(max(p.revision),0) FROM task_lark_binding_members p JOIN task_lark_credential_bindings b ON b.task_id=p.task_id AND b.revision=p.revision JOIN task_lark_switch_actions a ON a.actor_key=b.actor_key AND a.action_id=b.action_id AND a.task_id=b.task_id AND a.status='succeeded' WHERE p.task_id=$1 AND p.runtime_id=$2`, m.Record.View.Task.ID, runtime).Scan(&revision)
	return revision, err
}
