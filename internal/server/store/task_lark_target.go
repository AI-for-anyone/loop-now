package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/credentialmodel"
)

// TaskLarkTarget is a non-secret pinned target, captured under the original
// task transaction. Generation is intentionally not a credential identity.
type TaskLarkTarget struct {
	AccountID, VersionID  uuid.UUID
	AccountRevision       int64
	Identity              credentialmodel.LarkIdentity
	RuntimeVariantVersion string
}

// CaptureTaskLarkTargetTx does local verification only. It never refreshes or
// probes a provider, and reuses the login owner's canonical secret codec.
func (s *Store) CaptureTaskLarkTargetTx(ctx context.Context, m *TaskMutation, account, version uuid.UUID, expectedRevision int64) (TaskLarkTarget, error) {
	var out TaskLarkTarget
	if m.Record.Current() == nil {
		return out, apperr.Conflict("task_conflict", "原任务节点不可用")
	}
	var project uuid.UUID
	var current *uuid.UUID
	var deleted *time.Time
	var purpose, cli, auth, refreshState string
	var secret []byte
	err := m.Tx.QueryRow(ctx, `SELECT a.project_id,a.purpose,a.cli_name,a.revision,a.current_cli_version_id,a.deleted_at,v.auth_method,i.brand,i.app_id,i.open_id,i.display_name,i.granted_scopes,i.verified_at,i.runtime_variant_version,g.refresh_state,v.secret FROM project_credential_accounts a JOIN project_cli_versions v ON v.account_id=a.id AND v.id=$2 JOIN lark_cli_identities i ON i.version_id=v.id AND i.account_id=a.id JOIN lark_cli_grants g ON g.version_id=v.id WHERE a.id=$1 FOR SHARE OF a,v,i,g`, account, version).Scan(&project, &purpose, &cli, &out.AccountRevision, &current, &deleted, &auth, &out.Identity.Brand, &out.Identity.AppID, &out.Identity.OpenID, &out.Identity.DisplayName, &out.Identity.GrantedScopes, &out.Identity.VerifiedAt, &out.RuntimeVariantVersion, &refreshState, &secret)
	if err != nil {
		return out, notFoundIfNoRows(err, "Lark用户账号版本")
	}
	deny := func() (TaskLarkTarget, error) {
		return TaskLarkTarget{}, apperr.Conflict("task_conflict", "所选Lark用户授权版本已变化或不可用")
	}
	if account == uuid.Nil || version == uuid.Nil || project != m.Record.View.Task.ProjectID || purpose != "cli" || cli != "lark-cli" || auth != "oauth" || deleted != nil || current == nil || *current != version || out.AccountRevision != expectedRevision || refreshState == "unavailable" || out.RuntimeVariantVersion != clicredential.LarkUserVariant || !out.Identity.Valid() {
		return deny()
	}
	// GrantedScopes is provider-observed metadata, including reliable unknown
	// (nil). Requested scopes are not evidence of actual grants and do not add
	// a separate local switching gate; command authorization is provider-owned.
	credential, err := clicredential.DecodeLark(secret)
	clear(secret)
	binding := clicredential.LarkBinding{ProjectID: project, AccountID: account, VersionID: version, AgentID: m.Record.Current().AgentID, Brand: out.Identity.Brand, AppID: out.Identity.AppID, OpenID: out.Identity.OpenID, RuntimeVariantVersion: out.RuntimeVariantVersion}
	if err != nil || !credential.Matches(binding) {
		return deny()
	}
	// This separate statement runs after all row lock waits. Transaction-start
	// now() could otherwise accept a grant whose expiry passed during the wait.
	var now time.Time
	if err = m.Tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return TaskLarkTarget{}, err
	}
	if credential.RefreshExpiresAt <= now.UnixMilli() {
		return deny()
	}
	out.AccountID, out.VersionID = account, version
	return out, nil
}
