package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/clicredential"
	"time"
)

type GoogleCLIGrant struct {
	AccountLabel        string
	Binding             clicredential.Binding
	Secret              []byte
	Generation          int64
	RefreshState        string
	LeaseID             *uuid.UUID
	LeaseAgentID        *uuid.UUID
	LeaseVerificationID *uuid.UUID
	LeaseUntil          *time.Time
	ErrorCode           *string
}

// finishClaim is reserved for conditional Commit/Fail of an existing lease.
// It never grants read access or permits a deleted owner to start a refresh.
func googleGrantTx(ctx context.Context, tx pgx.Tx, b clicredential.Binding, write, finishClaim bool) (GoogleCLIGrant, error) {
	out := GoogleCLIGrant{Binding: b}
	if !b.Valid() {
		return out, errors.New("CLI授权绑定无效")
	}
	if _, e := lockCredentialProject(ctx, tx, b.ProjectID, false); e != nil {
		return out, e
	}
	mode := "FOR SHARE OF g,v"
	if write {
		mode = "FOR UPDATE OF g,v"
	}
	e := tx.QueryRow(ctx, `SELECT v.account_label,v.secret,g.generation,g.refresh_state,g.lease_id,g.lease_agent_id,g.lease_verification_id,g.lease_until,g.error_code FROM google_cli_grants g JOIN project_cli_versions v ON v.id=g.version_id AND v.account_id=g.account_id JOIN project_credential_accounts a ON a.id=v.account_id JOIN agents owner ON owner.id=$4 WHERE g.version_id=$1 AND g.account_id=$2 AND a.project_id=$3 AND a.purpose='cli' AND a.cli_name='gcloud' AND v.auth_method='oauth' AND owner.project_id=$3 AND (owner.deleted_at IS NULL OR $5) `+mode, b.VersionID, b.AccountID, b.ProjectID, b.AgentID, finishClaim).Scan(&out.AccountLabel, &out.Secret, &out.Generation, &out.RefreshState, &out.LeaseID, &out.LeaseAgentID, &out.LeaseVerificationID, &out.LeaseUntil, &out.ErrorCode)
	return out, notFoundIfNoRows(e, "CLI授权")
}
func (s *Store) GetGoogleCLIGrant(ctx context.Context, b clicredential.Binding) (GoogleCLIGrant, error) {
	var out GoogleCLIGrant
	e := s.inTx(ctx, func(tx pgx.Tx) error { var e error; out, e = googleGrantTx(ctx, tx, b, false, false); return e })
	return out, e
}
func (s *Store) ClaimGoogleCLIRefresh(ctx context.Context, b clicredential.Binding, generation int64, lease uuid.UUID, until time.Time) (bool, error) {
	claimed := false
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := googleGrantTx(ctx, tx, b, true, false)
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
		tag, e := tx.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_agent_id=$4,lease_verification_id=NULL,lease_until=$3,error_code=NULL,updated_at=clock_timestamp() WHERE version_id=$1`, b.VersionID, lease, until, b.AgentID)
		claimed = e == nil && tag.RowsAffected() == 1
		return e
	})
	return claimed, e
}
func (s *Store) CommitGoogleCLIRefresh(ctx context.Context, b clicredential.Binding, generation int64, lease uuid.UUID, secret []byte) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := googleGrantTx(ctx, tx, b, true, true)
		if e != nil {
			return e
		}
		if r.Generation != generation || r.RefreshState != "refreshing" || r.LeaseID == nil || *r.LeaseID != lease || r.LeaseAgentID == nil || *r.LeaseAgentID != b.AgentID {
			return errors.New("CLI刷新租约不匹配")
		}
		// Lease expiration is checked with the database clock before writing the credential.
		var live bool
		if e = tx.QueryRow(ctx, `SELECT lease_until>clock_timestamp() FROM google_cli_grants WHERE version_id=$1`, b.VersionID).Scan(&live); e != nil || !live {
			return errors.New("CLI刷新租约已结束")
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
func (s *Store) FailGoogleCLIRefresh(ctx context.Context, b clicredential.Binding, generation int64, lease uuid.UUID, code string) error {
	switch code {
	case "requires_login", "refresh_uncertain", "refresh_failed", "invalid_response", "timeout":
	default:
		code = "refresh_failed"
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := googleGrantTx(ctx, tx, b, true, true); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='unavailable',lease_id=NULL,lease_agent_id=NULL,lease_verification_id=NULL,lease_until=NULL,error_code=$4,updated_at=clock_timestamp() WHERE version_id=$1 AND generation=$2 AND lease_id=$3 AND lease_agent_id=$5 AND refresh_state='refreshing'`, b.VersionID, generation, lease, code, b.AgentID)
		return e
	})
}
