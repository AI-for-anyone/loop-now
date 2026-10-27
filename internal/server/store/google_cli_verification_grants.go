package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/clicredential"
	"time"
)

// Lock project -> account -> operation -> grant/version. Finish is solely for
// an existing exact lease; it never permits a cancelled operation to claim.
func verificationGoogleGrantTx(ctx context.Context, tx pgx.Tx, b clicredential.VerificationGrantBinding, write, finish bool) (GoogleCLIGrant, error) {
	var out GoogleCLIGrant
	if !b.Valid() {
		return out, errors.New("管理验证授权绑定无效")
	}
	if _, e := lockCredentialProject(ctx, tx, b.ProjectID, false); e != nil {
		return out, e
	}
	var current *uuid.UUID
	var deleted *time.Time
	if e := tx.QueryRow(ctx, `SELECT current_cli_version_id,deleted_at FROM project_credential_accounts WHERE id=$1 AND project_id=$2 AND purpose='cli' AND cli_name='gcloud' FOR SHARE`, b.AccountID, b.ProjectID).Scan(&current, &deleted); e != nil {
		return out, notFoundIfNoRows(e, "管理验证账号")
	}
	var version uuid.UUID
	var live *uuid.UUID
	var status string
	var unexpired bool
	if e := tx.QueryRow(ctx, `SELECT version_id,live_version_id,status,expires_at>clock_timestamp() FROM cli_credential_verifications WHERE id=$1 AND project_id=$2 AND account_id=$3 AND cli_name='gcloud' AND auth_method='oauth' FOR SHARE`, b.OperationID, b.ProjectID, b.AccountID).Scan(&version, &live, &status, &unexpired); e != nil {
		return out, notFoundIfNoRows(e, "管理验证操作")
	}
	if version != b.VersionID {
		return out, errors.New("管理验证版本不匹配")
	}
	if !finish && (deleted != nil || current == nil || *current != version || live == nil || *live != version || (status != "pending" && status != "running") || !unexpired) {
		return out, errors.New("管理验证操作已失效")
	}
	mode := "FOR SHARE OF g,v"
	if write {
		mode = "FOR UPDATE OF g,v"
	}
	e := tx.QueryRow(ctx, `SELECT v.account_label,v.secret,g.generation,g.refresh_state,g.lease_id,g.lease_agent_id,g.lease_verification_id,g.lease_until,g.error_code FROM google_cli_grants g JOIN project_cli_versions v ON v.id=g.version_id AND v.account_id=g.account_id WHERE g.version_id=$1 AND g.account_id=$2 AND v.auth_method='oauth' `+mode, b.VersionID, b.AccountID).Scan(&out.AccountLabel, &out.Secret, &out.Generation, &out.RefreshState, &out.LeaseID, &out.LeaseAgentID, &out.LeaseVerificationID, &out.LeaseUntil, &out.ErrorCode)
	if e != nil {
		return out, notFoundIfNoRows(e, "管理验证授权")
	}
	// The grant lock may have waited past the operation deadline. Operation
	// identity is protected by its SHARE lock, but wall-clock validity is not.
	// Recheck after every authorization lock has been acquired, before a new
	// consumer can claim or receive credentials. Existing exact leases retain
	// their independent finish lifecycle.
	if !finish {
		if e = tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM cli_credential_verifications WHERE id=$1`, b.OperationID).Scan(&unexpired); e != nil {
			return out, e
		}
		if !unexpired {
			return out, errors.New("管理验证操作已失效")
		}
	}
	return out, nil
}
func (s *Store) GetGoogleCLIVerificationGrant(ctx context.Context, b clicredential.VerificationGrantBinding) (GoogleCLIGrant, error) {
	var out GoogleCLIGrant
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		var e error
		out, e = verificationGoogleGrantTx(ctx, tx, b, false, false)
		return e
	})
	return out, e
}
func (s *Store) ClaimGoogleCLIVerificationRefresh(ctx context.Context, b clicredential.VerificationGrantBinding, generation int64, lease uuid.UUID, until time.Time) (bool, error) {
	claimed := false
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := verificationGoogleGrantTx(ctx, tx, b, true, false)
		if e != nil {
			return e
		}
		if r.Generation != generation {
			return nil
		}
		if r.RefreshState == "refreshing" {
			_, e = tx.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='unavailable',lease_id=NULL,lease_agent_id=NULL,lease_verification_id=NULL,lease_until=NULL,error_code='refresh_uncertain',updated_at=clock_timestamp() WHERE version_id=$1 AND lease_until<=clock_timestamp()`, b.VersionID)
			return e
		}
		if r.RefreshState != "ready" || lease == uuid.Nil || !until.After(time.Now()) {
			return nil
		}
		tag, e := tx.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_agent_id=NULL,lease_verification_id=$3,lease_until=$4,error_code=NULL,updated_at=clock_timestamp() WHERE version_id=$1 AND generation=$5 AND refresh_state='ready' AND EXISTS (SELECT 1 FROM cli_credential_verifications o WHERE o.id=$3 AND o.status IN ('pending','running') AND o.expires_at>clock_timestamp())`, b.VersionID, lease, b.OperationID, until, generation)
		claimed = e == nil && tag.RowsAffected() == 1
		return e
	})
	return claimed, e
}
func (s *Store) CommitGoogleCLIVerificationRefresh(ctx context.Context, b clicredential.VerificationGrantBinding, generation int64, lease uuid.UUID, secret []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := verificationGoogleGrantTx(ctx, tx, b, true, true)
		if e != nil {
			return e
		}
		if r.Generation != generation || r.RefreshState != "refreshing" || r.LeaseID == nil || *r.LeaseID != lease || r.LeaseVerificationID == nil || *r.LeaseVerificationID != b.OperationID || r.LeaseAgentID != nil {
			return errors.New("管理验证刷新租约不匹配")
		}
		var live bool
		if e = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM google_cli_grants WHERE version_id=$1`, b.VersionID).Scan(&live); e != nil || !live {
			return errors.New("管理验证刷新租约已结束")
		}
		if secret == nil {
			return errors.New("缺少CLI凭证回写")
		}
		if _, e = tx.Exec(ctx, `UPDATE project_cli_versions SET secret=$2,updated_at=clock_timestamp() WHERE id=$1 AND account_id=$3`, b.VersionID, secret, b.AccountID); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE google_cli_grants SET generation=generation+1,refresh_state='ready',lease_id=NULL,lease_agent_id=NULL,lease_verification_id=NULL,lease_until=NULL,error_code=NULL,updated_at=clock_timestamp() WHERE version_id=$1`, b.VersionID)
		return e
	})
}
func (s *Store) FailGoogleCLIVerificationRefresh(ctx context.Context, b clicredential.VerificationGrantBinding, generation int64, lease uuid.UUID, code string) error {
	switch code {
	case "requires_login", "refresh_uncertain", "refresh_failed", "invalid_response", "timeout":
	default:
		code = "refresh_failed"
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := verificationGoogleGrantTx(ctx, tx, b, true, true); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='unavailable',lease_id=NULL,lease_agent_id=NULL,lease_verification_id=NULL,lease_until=NULL,error_code=$4,updated_at=clock_timestamp() WHERE version_id=$1 AND generation=$2 AND lease_id=$3 AND lease_verification_id=$5 AND lease_agent_id IS NULL AND refresh_state='refreshing'`, b.VersionID, generation, lease, code, b.OperationID)
		return e
	})
}
