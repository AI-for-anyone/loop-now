package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/cliverify"
	"time"
)

// VerificationMetadata is trusted registry output, never an HTTP input.
type VerificationMetadata struct {
	TemplateSource, CredentialFormat, TemplateFingerprint, AdapterRevision, ProbeKind string
}

const verificationColumns = `o.id,o.project_id,o.account_id,o.version_id,o.cli_name,o.auth_method,o.template_source,o.credential_format,o.template_fingerprint,o.tool_version,o.adapter_revision,o.probe_kind,o.status,o.phase,o.started_at,o.expires_at,o.completed_at,o.result_outcome,o.verified_at,o.duration_ms,o.error_code,o.http_status,coalesce(a.deleted_at IS NULL AND a.current_cli_version_id=o.version_id,false)`

func scanVerification(row pgx.Row) (cliverify.Operation, error) {
	var op cliverify.Operation
	var outcome *string
	var verified *time.Time
	var duration *int64
	var code *string
	var httpStatus *int
	err := row.Scan(&op.ID, &op.ProjectID, &op.AccountID, &op.VersionID, &op.CLI, &op.AuthMethod, &op.TemplateSource, &op.CredentialFormat, &op.TemplateFingerprint, &op.ToolVersion, &op.AdapterRevision, &op.ProbeKind, &op.Status, &op.Phase, &op.StartedAt, &op.ExpiresAt, &op.CompletedAt, &outcome, &verified, &duration, &code, &httpStatus, &op.IsCurrentVersion)
	if outcome != nil && duration != nil {
		op.Result = &cliverify.Result{Outcome: *outcome, VerifiedAt: verified, DurationMS: *duration, ErrorCode: code, HTTPStatus: httpStatus}
	}
	return op, notFoundIfNoRows(err, "CLI验证操作")
}
func verificationTx(ctx context.Context, tx pgx.Tx, project, account, id uuid.UUID, lock bool) (cliverify.Operation, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF o"
	}
	return scanVerification(tx.QueryRow(ctx, `SELECT `+verificationColumns+` FROM cli_credential_verifications o JOIN project_credential_accounts a ON a.id=o.account_id WHERE o.project_id=$1 AND o.account_id=$2 AND o.id=$3`+suffix, project, account, id))
}
func lockVerificationAccount(ctx context.Context, tx pgx.Tx, project, account uuid.UUID, write bool) (ProjectCredentialRecord, error) {
	if _, err := lockCredentialProject(ctx, tx, project, false); err != nil {
		return ProjectCredentialRecord{}, err
	}
	return projectAccountTx(ctx, tx, project, account, write)
}
func verificationVersionConflict(project, account, expected uuid.UUID, current *uuid.UUID) error {
	e := apperr.PreconditionFailed("CLI凭证版本已更改，请重新读取")
	e.Details = cliverify.VersionConflictDetails{ProjectID: project, AccountID: account, ExpectedVersionID: expected, CurrentVersionID: current}
	return e
}
func verificationConflict(code string, op cliverify.Operation) error {
	e := apperr.Conflict(code, "验证操作冲突，请读取原操作")
	e.Details = cliverify.ConflictDetails{ProjectID: op.ProjectID, AccountID: op.AccountID, OperationID: op.ID, VersionID: op.VersionID}
	return e
}
func verificationActive(op cliverify.Operation) bool {
	return op.Status == "pending" || op.Status == "running"
}
func verificationHash(project, account, version uuid.UUID) string {
	h := sha256.Sum256([]byte(project.String() + "/" + account.String() + "/" + version.String()))
	return hex.EncodeToString(h[:])
}

// BeginCLIVerification registers one immutable operation. Only a newly-created
// operation may enqueue work; replay returns created=false even after termination.
func (s *Store) BeginCLIVerification(ctx context.Context, project, account uuid.UUID, in cliverify.Start, meta VerificationMetadata, initiator string, actor *uuid.UUID, expires time.Time) (cliverify.Operation, bool, error) {
	var out cliverify.Operation
	created := false
	if err := in.Validate(); err != nil {
		return out, false, err
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, e := lockVerificationAccount(ctx, tx, project, account, true)
		if e != nil {
			return e
		}
		old, e := verificationTx(ctx, tx, project, account, in.OperationID, true)
		if e == nil {
			if old.VersionID != in.ExpectedVersionID {
				return verificationConflict(apperr.CodeVerificationOperationConflict, old)
			}
			out = old
			return nil
		}
		if !apperr.Is(e, apperr.CodeNotFound) {
			return e
		}
		if r.View.DeletedAt != nil {
			return projectAccountDeleted(project, account)
		}
		if r.View.Purpose != PurposeCLI || r.View.CLI == nil {
			return apperr.Invalid("accountId", "须为CLI账号")
		}
		if r.View.CurrentVersionID == nil || *r.View.CurrentVersionID != in.ExpectedVersionID {
			return verificationVersionConflict(project, account, in.ExpectedVersionID, r.View.CurrentVersionID)
		}
		if r.CLI == nil || r.TemplateSource != meta.TemplateSource || r.CLI.TemplateVersion != meta.CredentialFormat {
			return apperr.Invalid("accountId", "CLI模板来源或格式已更改")
		}
		if _, e = tx.Exec(ctx, `UPDATE cli_credential_verifications SET status='expired',phase=NULL,completed_at=clock_timestamp(),result_outcome='expired',verified_at=NULL,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code='timeout',http_status=NULL WHERE account_id=$1 AND status IN('pending','running') AND expires_at<=clock_timestamp()`, account); e != nil {
			return e
		}
		var active uuid.UUID
		e = tx.QueryRow(ctx, `SELECT id FROM cli_credential_verifications WHERE account_id=$1 AND status IN('pending','running') FOR UPDATE`, account).Scan(&active)
		if e == nil {
			other, e := verificationTx(ctx, tx, project, account, active, false)
			if e != nil {
				return e
			}
			return verificationConflict(apperr.CodeVerificationInProgress, other)
		}
		if e != pgx.ErrNoRows {
			return e
		}
		method := "static"
		if r.View.AuthMethod != nil {
			method = *r.View.AuthMethod
		}
		_, e = tx.Exec(ctx, `INSERT INTO cli_credential_verifications(id,project_id,account_id,version_id,live_version_id,cli_name,auth_method,request_hash,initiator_kind,initiator_id,template_source,credential_format,template_fingerprint,adapter_revision,probe_kind,expires_at)VALUES($1,$2,$3,$4,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, in.OperationID, project, account, in.ExpectedVersionID, *r.View.CLI, method, verificationHash(project, account, in.ExpectedVersionID), initiator, actor, meta.TemplateSource, meta.CredentialFormat, meta.TemplateFingerprint, meta.AdapterRevision, meta.ProbeKind, expires)
		if e != nil {
			if isUniqueViolation(e, "") {
				return verificationConflict(apperr.CodeVerificationOperationConflict, cliverify.Operation{ID: in.OperationID, ProjectID: project, AccountID: account, VersionID: in.ExpectedVersionID})
			}
			return e
		}
		out, e = verificationTx(ctx, tx, project, account, in.OperationID, false)
		created = e == nil
		return e
	})
	return out, created && err == nil, err
}
func (s *Store) GetCLIVerification(ctx context.Context, project, account, id uuid.UUID) (cliverify.Operation, error) {
	var out cliverify.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockVerificationAccount(ctx, tx, project, account, false); e != nil {
			return e
		}
		var e error
		out, e = verificationTx(ctx, tx, project, account, id, false)
		return e
	})
	return out, err
}
func (s *Store) CLIVerificationState(ctx context.Context, project, account uuid.UUID) (cliverify.State, error) {
	var out cliverify.State
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, e := lockVerificationAccount(ctx, tx, project, account, false)
		if e != nil {
			return e
		}
		if a.View.Purpose != PurposeCLI {
			return apperr.Invalid("accountId", "须为CLI账号")
		}
		out = cliverify.State{AccountID: account, CurrentVersionID: a.View.CurrentVersionID}
		if out.CurrentVersionID == nil || a.View.DeletedAt != nil {
			return nil
		}
		var id uuid.UUID
		e = tx.QueryRow(ctx, `SELECT id FROM cli_credential_verifications WHERE account_id=$1 AND version_id=$2 ORDER BY started_at DESC,id DESC LIMIT 1`, account, *out.CurrentVersionID).Scan(&id)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		v, e := verificationTx(ctx, tx, project, account, id, false)
		if e == nil {
			out.Operation = &v
		}
		return e
	})
	return out, err
}

// terminateVerificationsTx is also used by account/project mutation while their
// existing locks are held. It never opens another transaction or touches secrets.
func terminateVerificationsTx(ctx context.Context, tx pgx.Tx, project uuid.UUID, account *uuid.UUID, code *string) error {
	status, outcome := "cancelled", "cancelled"
	if code != nil {
		status, outcome = "failed", "failed"
	}
	_, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET status=$3,phase=NULL,completed_at=clock_timestamp(),result_outcome=$4,verified_at=NULL,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code=$5,http_status=NULL WHERE project_id=$1 AND ($2::uuid IS NULL OR account_id=$2) AND status IN('pending','running')`, project, account, status, outcome, code)
	return e
}
func (s *Store) CancelCLIVerification(ctx context.Context, project, account, id uuid.UUID) (cliverify.Operation, error) {
	var out cliverify.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockVerificationAccount(ctx, tx, project, account, false); e != nil {
			return e
		}
		op, e := verificationTx(ctx, tx, project, account, id, true)
		if e != nil {
			return e
		}
		if !verificationActive(op) {
			out = op
			return nil
		}
		_, e = tx.Exec(ctx, `UPDATE cli_credential_verifications SET status='cancelled',phase=NULL,completed_at=clock_timestamp(),result_outcome='cancelled',verified_at=NULL,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code=NULL,http_status=NULL WHERE id=$1`, id)
		if e != nil {
			return e
		}
		out, e = verificationTx(ctx, tx, project, account, id, false)
		return e
	})
	return out, err
}
func (s *Store) ClaimCLIVerification(ctx context.Context, project, account, id uuid.UUID) (cliverify.Operation, bool, error) {
	var out cliverify.Operation
	claimed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, e := lockVerificationAccount(ctx, tx, project, account, false)
		if e != nil {
			return e
		}
		out, e = verificationTx(ctx, tx, project, account, id, true)
		if e != nil {
			return e
		}
		if out.Status != "pending" {
			return nil
		}
		if a.View.DeletedAt != nil || a.View.CurrentVersionID == nil || *a.View.CurrentVersionID != out.VersionID {
			code := "target_changed"
			if e = terminateVerificationsTx(ctx, tx, project, &account, &code); e != nil {
				return e
			}
			out, e = verificationTx(ctx, tx, project, account, id, false)
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET status='running' WHERE id=$1 AND expires_at>clock_timestamp()`, id)
		if e != nil {
			return e
		}
		claimed = tag.RowsAffected() == 1
		if !claimed {
			if e = expireVerificationsTx(ctx, tx, &id); e != nil {
				return e
			}
		}
		out, e = verificationTx(ctx, tx, project, account, id, false)
		return e
	})
	return out, claimed && err == nil, err
}
func expireVerificationsTx(ctx context.Context, tx pgx.Tx, id *uuid.UUID) error {
	_, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET status='expired',phase=NULL,completed_at=clock_timestamp(),result_outcome='expired',verified_at=NULL,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code='timeout',http_status=NULL WHERE ($1::uuid IS NULL OR id=$1) AND status IN('pending','running') AND expires_at<=clock_timestamp()`, id)
	return e
}
func (s *Store) CLIVerificationPhase(ctx context.Context, project, account, id uuid.UUID, phase string, tool *string) error {
	switch phase {
	case "preparing_tool", "resolving_credentials", "probing", "recording":
	default:
		return errors.New("验证阶段无效")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockVerificationAccount(ctx, tx, project, account, false); e != nil {
			return e
		}
		_, e := verificationTx(ctx, tx, project, account, id, true)
		if e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET phase=$2,tool_version=coalesce($3,tool_version) WHERE id=$1 AND status='running' AND expires_at>clock_timestamp()`, id, phase, tool)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return errors.New("验证操作已结束")
		}
		return nil
	})
}
func (s *Store) FinishCLIVerification(ctx context.Context, project, account, id uuid.UUID, result cliverify.Result) (cliverify.Operation, bool, error) {
	var out cliverify.Operation
	changed := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, e := lockVerificationAccount(ctx, tx, project, account, false)
		if e != nil {
			return e
		}
		out, e = verificationTx(ctx, tx, project, account, id, true)
		if e != nil {
			return e
		}
		if !verificationActive(out) {
			return nil
		}
		if e = expireVerificationsTx(ctx, tx, &id); e != nil {
			return e
		}
		out, e = verificationTx(ctx, tx, project, account, id, false)
		if e != nil || !verificationActive(out) {
			return e
		}
		if a.View.DeletedAt != nil || a.View.CurrentVersionID == nil || *a.View.CurrentVersionID != out.VersionID {
			code := "target_changed"
			if e = terminateVerificationsTx(ctx, tx, project, &account, &code); e != nil {
				return e
			}
			out, e = verificationTx(ctx, tx, project, account, id, false)
			return e
		}
		status := "failed"
		var verified *time.Time
		switch result.Outcome {
		case "passed":
			status = "succeeded"
			now := time.Now().UTC()
			verified = &now
			result.ErrorCode = nil
		case "failed":
			if result.ErrorCode == nil {
				return errors.New("验证失败须有安全原因")
			}
		case "expired":
			status = "expired"
			c := "timeout"
			result.ErrorCode = &c
		case "cancelled":
			status = "cancelled"
			result.ErrorCode = nil
		default:
			return errors.New("验证结果无效")
		}
		tag, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET status=$2,phase=NULL,completed_at=clock_timestamp(),result_outcome=$3,verified_at=$4,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code=$5,http_status=$6 WHERE id=$1 AND status IN('pending','running')`, id, status, result.Outcome, verified, result.ErrorCode, result.HTTPStatus)
		if e != nil {
			return e
		}
		changed = tag.RowsAffected() == 1
		out, e = verificationTx(ctx, tx, project, account, id, false)
		return e
	})
	return out, changed && err == nil, err
}

// Recovery never retries a native probe or extracts credentials again.
func (s *Store) RecoverCLIVerifications(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if e := expireVerificationsTx(ctx, tx, nil); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE cli_credential_verifications SET status='failed',phase=NULL,completed_at=clock_timestamp(),result_outcome='failed',verified_at=NULL,duration_ms=greatest(0,floor(extract(epoch FROM clock_timestamp()-started_at)*1000))::bigint,error_code='server_restarted',http_status=NULL WHERE status IN('pending','running')`)
		return e
	})
}
func (s *Store) ExpireCLIVerifications(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error { return expireVerificationsTx(ctx, tx, nil) })
}
