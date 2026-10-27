package store

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"time"
)

const projectOperationColumns = `id,project_id,provider,target_account_id,target_revision,name,status,account_id,started_at,expires_at,completed_at,authorization_url,error_code`

func scanProjectOperation(r pgx.Row) (cm.Operation, error) {
	var o cm.Operation
	err := r.Scan(&o.ID, &o.ProjectID, &o.Provider, &o.TargetAccountID, &o.TargetRevision, &o.Name, &o.Status, &o.AccountID, &o.StartedAt, &o.ExpiresAt, &o.CompletedAt, &o.AuthorizationURL, &o.ErrorCode)
	return o, err
}
func projectOperationTx(ctx context.Context, tx pgx.Tx, project, id uuid.UUID, lock bool) (cm.Operation, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	o, e := scanProjectOperation(tx.QueryRow(ctx, `SELECT `+projectOperationColumns+` FROM project_model_auth_operations WHERE id=$1 AND project_id=$2`+suffix, id, project))
	return o, notFoundIfNoRows(e, "项目登录操作")
}
func (s *Store) NewProjectModelAuthOperation(ctx context.Context, project uuid.UUID, in cm.LoginStart, expires time.Time) (cm.Operation, bool, error) {
	var out cm.Operation
	created := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if in.AccountID != nil {
			r, e := projectAccountTx(ctx, tx, project, *in.AccountID, true)
			if e != nil {
				return e
			}
			if in.ExpectedRevision == nil {
				return apperr.Invalid("expectedRevision", "重登需要预期版本")
			}
			if e = credentialCAS("account", r.View.ID, *in.ExpectedRevision, r.View.Revision); e != nil {
				return e
			}
			if r.View.DeletedAt != nil {
				return projectAccountDeleted(project, r.View.ID)
			}
			if r.View.Purpose != PurposeModel || r.View.Provider == nil || *r.View.Provider != in.Provider {
				return apperr.Invalid("accountId", "仅可重登相同提供方模型账号")
			}
		}
		if _, e := tx.Exec(ctx, `UPDATE project_model_auth_operations SET status='expired',authorization_url=NULL,error_code='auth_timeout',completed_at=clock_timestamp() WHERE project_id=$1 AND status IN('pending','awaiting_authorization') AND expires_at<=clock_timestamp()`, project); e != nil {
			return e
		}
		old, e := scanProjectOperation(tx.QueryRow(ctx, `SELECT `+projectOperationColumns+` FROM project_model_auth_operations WHERE project_id=$1 AND provider=$2 AND target_account_id IS NOT DISTINCT FROM $3::uuid AND status IN('pending','awaiting_authorization')`, project, in.Provider, in.AccountID))
		if e == nil {
			sameName := (old.Name == nil && in.Name == nil) || (old.Name != nil && in.Name != nil && *old.Name == *in.Name)
			sameRevision := (old.TargetRevision == nil && in.ExpectedRevision == nil) || (old.TargetRevision != nil && in.ExpectedRevision != nil && *old.TargetRevision == *in.ExpectedRevision)
			if sameName && sameRevision {
				out = old
				return nil
			}
			conflict := apperr.Conflict(apperr.CodeProjectModelAuthInProgress, "目标账号已有不同登录操作")
			conflict.Details = struct {
				ProjectID   uuid.UUID `json:"projectId"`
				OperationID uuid.UUID `json:"operationId"`
			}{project, old.ID}
			return conflict
		}
		if e != pgx.ErrNoRows {
			return e
		}
		id := uuid.Must(uuid.NewV7())
		out, e = scanProjectOperation(tx.QueryRow(ctx, `INSERT INTO project_model_auth_operations(id,project_id,provider,target_account_id,target_revision,name,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+projectOperationColumns, id, project, in.Provider, in.AccountID, in.ExpectedRevision, in.Name, expires))
		created = e == nil
		return e
	})
	return out, created, err
}
func (s *Store) GetProjectModelAuthOperation(ctx context.Context, project, id uuid.UUID) (cm.Operation, error) {
	var out cm.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, false); e != nil {
			return e
		}
		var e error
		out, e = projectOperationTx(ctx, tx, project, id, false)
		return e
	})
	return out, err
}
func (s *Store) ListProjectModelAuthOperations(ctx context.Context, project uuid.UUID, provider string, account *uuid.UUID) ([]cm.Operation, error) {
	var out []cm.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if account != nil {
			if _, e := projectAccountTx(ctx, tx, project, *account, false); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(ctx, `UPDATE project_model_auth_operations SET status='expired',authorization_url=NULL,error_code='auth_timeout',completed_at=clock_timestamp() WHERE project_id=$1 AND status IN('pending','awaiting_authorization') AND expires_at<=clock_timestamp()`, project); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT `+projectOperationColumns+` FROM project_model_auth_operations WHERE project_id=$1 AND ($2='' OR provider=$2) AND ($3::uuid IS NULL OR target_account_id=$3) AND status IN('pending','awaiting_authorization') ORDER BY started_at,id`, project, provider, account)
		if e != nil {
			return e
		}
		out, e = pgx.CollectRows(rows, func(r pgx.CollectableRow) (cm.Operation, error) { return scanProjectOperation(r) })
		return e
	})
	return out, err
}
func (s *Store) SetProjectModelAuthURL(ctx context.Context, project, id uuid.UUID, url string) (cm.Operation, error) {
	var out cm.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		o, e := projectOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		if modelAuthTerminal(o.Status) {
			out = o
			return nil
		}
		if !o.ExpiresAt.After(time.Now()) {
			out, e = endProjectOperationTx(ctx, tx, id, "expired", "auth_timeout")
			return e
		}
		out, e = scanProjectOperation(tx.QueryRow(ctx, `UPDATE project_model_auth_operations SET status='awaiting_authorization',authorization_url=$2 WHERE id=$1 RETURNING `+projectOperationColumns, id, url))
		return e
	})
	return out, err
}
func endProjectOperationTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, status, code string) (cm.Operation, error) {
	return scanProjectOperation(tx.QueryRow(ctx, `UPDATE project_model_auth_operations SET status=$2,error_code=$3,authorization_url=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+projectOperationColumns, id, status, nullIfEmpty(code)))
}
func (s *Store) EndProjectModelAuthOperation(ctx context.Context, project, id uuid.UUID, status, code string) (cm.Operation, error) {
	if status != "failed" && status != "cancelled" && status != "expired" {
		return cm.Operation{}, apperr.Invalid("status", "非法登录终态")
	}
	var out cm.Operation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		o, e := projectOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		if modelAuthTerminal(o.Status) {
			out = o
			return nil
		}
		out, e = endProjectOperationTx(ctx, tx, id, status, code)
		return e
	})
	return out, err
}
func (s *Store) CompleteProjectModelAuthOperation(ctx context.Context, project, id uuid.UUID, w ProjectCredentialWrite) (cm.Operation, *ProjectCredentialRecord, error) {
	var out cm.Operation
	var saved *ProjectCredentialRecord
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		initial, e := projectOperationTx(ctx, tx, project, id, false)
		if e != nil {
			return e
		}
		var deleted *time.Time
		if e = tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1 FOR UPDATE`, project).Scan(&deleted); e != nil {
			return notFoundIfNoRows(e, "项目")
		}
		var target *ProjectCredentialRecord
		if initial.TargetAccountID != nil {
			r, e := projectAccountTx(ctx, tx, project, *initial.TargetAccountID, true)
			if e != nil {
				return e
			}
			target = &r
		}
		out, e = projectOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		if modelAuthTerminal(out.Status) {
			return nil
		}
		if deleted != nil {
			out, e = endProjectOperationTx(ctx, tx, id, "cancelled", "")
			return e
		}
		if !out.ExpiresAt.After(time.Now()) {
			out, e = endProjectOperationTx(ctx, tx, id, "expired", "auth_timeout")
			return e
		}
		if target != nil && (target.View.DeletedAt != nil || out.TargetRevision == nil || target.View.Revision != *out.TargetRevision) {
			out, e = endProjectOperationTx(ctx, tx, id, "failed", "configuration_changed")
			return e
		}
		var record ProjectCredentialRecord
		if target == nil {
			if out.Name == nil {
				return apperr.Invalid("name", "登录操作缺少账号名称")
			}
			record, e = insertProjectAccountTx(ctx, tx, project, PurposeModel, out.Provider, *out.Name, w)
		} else {
			if _, e = insertProjectCredentialVersion(ctx, tx, target.View, w); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, target.View.ID); e != nil {
				return e
			}
			record, e = projectAccountTx(ctx, tx, project, target.View.ID, false)
		}
		if e != nil {
			return e
		}
		if e = bumpCredentialProject(ctx, tx, project); e != nil {
			return e
		}
		out, e = scanProjectOperation(tx.QueryRow(ctx, `UPDATE project_model_auth_operations SET account_id=$2,status='succeeded',authorization_url=NULL,error_code=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+projectOperationColumns, id, record.View.ID))
		if e == nil {
			saved = &record
		}
		return e
	})
	if err != nil {
		saved = nil
	}
	return out, saved, err
}
