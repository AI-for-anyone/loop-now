package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/clicredential"
	cm "loop-now/internal/server/credentialmodel"
	"math"
	"time"
)

type LarkCLIGrant struct {
	Identity              cm.LarkIdentity
	RuntimeVariantVersion string
	Secret                []byte
	Generation            int64
	RefreshState          string
	LeaseID, LeaseAgentID *uuid.UUID
	LeaseUntil            *time.Time
	ErrorCode             *string
}

func larkGrantTx(ctx context.Context, tx pgx.Tx, b clicredential.LarkBinding, write, finish bool) (LarkCLIGrant, error) {
	var r LarkCLIGrant
	if !b.Valid() {
		return r, errors.New("Lark授权绑定无效")
	}
	if _, e := lockCredentialProject(ctx, tx, b.ProjectID, false); e != nil {
		return r, e
	}
	lock := " FOR SHARE OF g,v"
	if write {
		lock = " FOR UPDATE OF g,v"
	}
	e := tx.QueryRow(ctx, `SELECT i.brand,i.app_id,i.open_id,i.display_name,i.granted_scopes,i.verified_at,i.runtime_variant_version,v.secret,g.generation,g.refresh_state,g.lease_id,g.lease_agent_id,g.lease_until,g.error_code FROM lark_cli_grants g JOIN lark_cli_identities i ON i.version_id=g.version_id JOIN project_cli_versions v ON v.id=i.version_id AND v.account_id=i.account_id JOIN project_credential_accounts a ON a.id=i.account_id JOIN agents owner ON owner.id=$4 WHERE g.version_id=$1 AND i.account_id=$2 AND a.project_id=$3 AND a.purpose='cli' AND a.cli_name='lark-cli' AND owner.project_id=$3 AND (owner.deleted_at IS NULL OR $5)`+lock, b.VersionID, b.AccountID, b.ProjectID, b.AgentID, finish).Scan(&r.Identity.Brand, &r.Identity.AppID, &r.Identity.OpenID, &r.Identity.DisplayName, &r.Identity.GrantedScopes, &r.Identity.VerifiedAt, &r.RuntimeVariantVersion, &r.Secret, &r.Generation, &r.RefreshState, &r.LeaseID, &r.LeaseAgentID, &r.LeaseUntil, &r.ErrorCode)
	if e != nil {
		return r, notFoundIfNoRows(e, "Lark授权")
	}
	if !r.Identity.Valid() || r.Identity.Brand != b.Brand || r.Identity.AppID != b.AppID || r.Identity.OpenID != b.OpenID || r.RuntimeVariantVersion != b.RuntimeVariantVersion {
		return LarkCLIGrant{}, errors.New("Lark授权身份不匹配")
	}
	return r, nil
}
func (s *Store) GetLarkCLIGrant(ctx context.Context, b clicredential.LarkBinding) (LarkCLIGrant, error) {
	var r LarkCLIGrant
	e := s.inTx(ctx, func(tx pgx.Tx) error { var e error; r, e = larkGrantTx(ctx, tx, b, false, false); return e })
	return r, e
}
func (s *Store) ClaimLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, generation int64, lease uuid.UUID, until time.Time) (bool, error) {
	claimed := false
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := larkGrantTx(ctx, tx, b, true, false)
		if e != nil {
			return e
		}
		if r.Generation != generation {
			return nil
		}
		if r.RefreshState == "refreshing" {
			_, e = tx.Exec(ctx, `UPDATE lark_cli_grants SET refresh_state='unavailable',lease_id=NULL,lease_agent_id=NULL,lease_until=NULL,error_code='refresh_uncertain',updated_at=clock_timestamp() WHERE version_id=$1 AND lease_until<=clock_timestamp()`, b.VersionID)
			return e
		}
		if r.RefreshState != "ready" || lease == uuid.Nil || generation == math.MaxInt64 {
			return nil
		}
		// Use the database clock after both project and grant locks were acquired.
		tag, e := tx.Exec(ctx, `UPDATE lark_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_agent_id=$3,lease_until=$4,error_code=NULL,updated_at=clock_timestamp() WHERE version_id=$1 AND $4::timestamptz>clock_timestamp()`, b.VersionID, lease, b.AgentID, until)
		claimed = e == nil && tag.RowsAffected() == 1
		return e
	})
	return claimed, e
}
func (s *Store) CommitLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, generation int64, lease uuid.UUID, secret []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := larkGrantTx(ctx, tx, b, true, true)
		if e != nil {
			return e
		}
		if generation == math.MaxInt64 || r.Generation != generation || r.RefreshState != "refreshing" || r.LeaseID == nil || *r.LeaseID != lease || r.LeaseAgentID == nil || *r.LeaseAgentID != b.AgentID {
			return errors.New("Lark刷新租约不匹配")
		}
		var live bool
		if e = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM lark_cli_grants WHERE version_id=$1`, b.VersionID).Scan(&live); e != nil {
			return e
		}
		if !live || secret == nil {
			return errors.New("Lark刷新租约已结束")
		}
		if _, e = tx.Exec(ctx, `UPDATE project_cli_versions SET secret=$2,updated_at=clock_timestamp() WHERE id=$1`, b.VersionID, secret); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE lark_cli_grants SET generation=generation+1,refresh_state='ready',lease_id=NULL,lease_agent_id=NULL,lease_until=NULL,error_code=NULL,updated_at=clock_timestamp() WHERE version_id=$1 AND lease_until>clock_timestamp()`, b.VersionID)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Lark刷新提交期限已过")
		}
		return nil
	})
}
func (s *Store) FailLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, generation int64, lease uuid.UUID, code string) error {
	switch code {
	case "requires_login", "refresh_uncertain", "refresh_failed", "invalid_response", "timeout":
	default:
		code = "refresh_failed"
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := larkGrantTx(ctx, tx, b, true, true); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE lark_cli_grants SET refresh_state='unavailable',lease_id=NULL,lease_agent_id=NULL,lease_until=NULL,error_code=$5,updated_at=clock_timestamp() WHERE version_id=$1 AND generation=$2 AND lease_id=$3 AND lease_agent_id=$4 AND refresh_state='refreshing'`, b.VersionID, generation, lease, b.AgentID, code)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("Lark刷新失败租约不匹配")
		}
		return nil
	})
}
