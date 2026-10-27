package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"time"
)

func (s *Store) newProjectLarkAuthOperation(ctx context.Context, project uuid.UUID, in cm.CLIAuthStart, expires time.Time) (cm.CLIAuthOperation, bool, error) {
	var out cm.CLIAuthOperation
	created := false
	// Decode again to apply the exact presence/branch contract even for internal
	// callers. No secret data is part of this canonical request fingerprint.
	b, e := json.Marshal(in)
	if e != nil {
		return out, false, apperr.Invalid("body", "Lark登录字段无效")
	}
	var checked cm.CLIAuthStart
	if json.Unmarshal(b, &checked) != nil || checked.CLI != "lark-cli" {
		return out, false, apperr.Invalid("body", "Lark登录字段无效")
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	e = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if e := expireCLIOperationsTx(ctx, tx, project); e != nil {
			return e
		}
		var old, oldCLI string
		e := tx.QueryRow(ctx, `SELECT request_hash,cli_name FROM project_cli_auth_operations WHERE id=$1 AND project_id=$2`, in.OperationID, project).Scan(&old, &oldCLI)
		if e == nil {
			if old != hash {
				return cliInProgress(project, in.OperationID, oldCLI)
			}
			out, e = cliOperationTx(ctx, tx, project, in.OperationID, false)
			return e
		}
		if e != pgx.ErrNoRows {
			return e
		}
		var sourceVersion *uuid.UUID
		if in.AccountID == nil {
			r, e := projectAccountTx(ctx, tx, project, *in.SourceAccountID, true)
			if e != nil {
				return e
			}
			if r.View.DeletedAt != nil {
				return projectAccountDeleted(project, r.View.ID)
			}
			if r.View.Purpose != PurposeCLI || r.View.CLI == nil || *r.View.CLI != "lark-cli" || r.CLI == nil || r.View.AuthMethod != nil {
				return apperr.Invalid("sourceAccountId", "必须选择同项目已有Lark应用配置")
			}
			if e = credentialCAS("account", r.View.ID, *in.ExpectedSourceRevision, r.View.Revision); e != nil {
				return e
			}
			sourceVersion = r.View.CurrentVersionID
		} else {
			r, e := projectAccountTx(ctx, tx, project, *in.AccountID, true)
			if e != nil {
				return e
			}
			if r.View.DeletedAt != nil {
				return projectAccountDeleted(project, r.View.ID)
			}
			if r.View.LarkIdentity == nil {
				return apperr.Invalid("accountId", "必须选择已有Lark用户账号")
			}
			if e = credentialCAS("account", r.View.ID, *in.ExpectedRevision, r.View.Revision); e != nil {
				return e
			}
		}
		var active uuid.UUID
		e = tx.QueryRow(ctx, `SELECT id FROM project_cli_auth_operations WHERE project_id=$1 AND cli_name='lark-cli' AND target_account_id IS NOT DISTINCT FROM $2::uuid AND status IN('pending','awaiting_authorization')`, project, in.AccountID).Scan(&active)
		if e == nil {
			return cliInProgress(project, active, "lark-cli")
		}
		if e != pgx.ErrNoRows {
			return e
		}
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM project_cli_auth_operations WHERE id=$1)`, in.OperationID).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return apperr.NotFound("CLI登录操作")
		}
		_, e = tx.Exec(ctx, `INSERT INTO project_cli_auth_operations(id,project_id,cli_name,auth_type,request_hash,target_account_id,target_revision,expires_at,source_account_id,source_version_id,source_revision)VALUES($1,$2,'lark-cli','lark_user',$3,$4,$5,$6,$7,$8,$9)`, in.OperationID, project, hash, in.AccountID, in.ExpectedRevision, expires, in.SourceAccountID, sourceVersion, in.ExpectedSourceRevision)
		if e != nil {
			return e
		}
		out, e = cliOperationTx(ctx, tx, project, in.OperationID, false)
		created = e == nil
		return e
	})
	return out, created, e
}

// GetProjectLarkAuthSource resolves only the version already captured by the
// operation. It never repairs a source with the source account's current value.
func (s *Store) GetProjectLarkAuthSource(ctx context.Context, project, id uuid.UUID) (CredentialRow, error) {
	var row CredentialRow
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, false); e != nil {
			return e
		}
		op, e := cliOperationTx(ctx, tx, project, id, false)
		if e != nil {
			return e
		}
		if op.CLI != "lark-cli" || cliOperationTerminal(op.Status) {
			return apperr.Conflict("cli_auth_in_progress", "Lark操作已结束")
		}
		var version, account uuid.UUID
		if op.TargetAccountID == nil {
			if op.SourceVersionID == nil || op.SourceAccountID == nil {
				return apperr.Invalid("sourceAccountId", "应用来源缺失")
			}
			version, account = *op.SourceVersionID, *op.SourceAccountID
		} else {
			r, e := projectAccountTx(ctx, tx, project, *op.TargetAccountID, false)
			if e != nil {
				return e
			}
			if r.View.DeletedAt != nil || op.TargetRevision == nil || r.View.Revision != *op.TargetRevision || r.View.LarkIdentity == nil || r.CLI == nil {
				return apperr.PreconditionFailed("Lark目标已改变")
			}
			row = *r.CLI
			return nil
		}
		row.ID = version
		row.Purpose = PurposeCLI
		row.CLIName = "lark-cli"
		return tx.QueryRow(ctx, `SELECT template_source,template_version,secret,field_names,env_names,file_paths,updated_at FROM project_cli_versions WHERE id=$1 AND account_id=$2 AND cli_name='lark-cli' AND auth_method='static'`, version, account).Scan(&row.TemplateSource, &row.TemplateVersion, &row.Secret, &row.FieldNames, &row.EnvNames, &row.FilePaths, &row.UpdatedAt)
	})
	return row, e
}

func (s *Store) SetProjectLarkAuthorization(ctx context.Context, project, id uuid.UUID, url string, code *string, expires time.Time) (cm.CLIAuthOperation, error) {
	var out cm.CLIAuthOperation
	// URL safety is checked by the fixed-brand protocol adapter before this
	// internal write; there is no public endpoint accepting a provider URL.
	if url == "" || expires.IsZero() || code != nil && (*code == "" || len(*code) > 200) {
		return out, apperr.Invalid("authorization", "Lark授权交互无效")
	}
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if e := expireCLIOperationsTx(ctx, tx, project); e != nil {
			return e
		}
		var e error
		out, e = cliOperationTx(ctx, tx, project, id, true)
		if e != nil || cliOperationTerminal(out.Status) {
			return e
		}
		if out.CLI != "lark-cli" {
			return apperr.Invalid("cli", "Lark授权操作类型不匹配")
		}
		if _, e = tx.Exec(ctx, `UPDATE project_cli_auth_operations SET status='awaiting_authorization',authorization_url=$2,user_code=$3,expires_at=LEAST(expires_at,$4) WHERE id=$1 AND $4::timestamptz>clock_timestamp()`, id, url, code, expires); e != nil {
			return e
		}
		out, e = cliOperationTx(ctx, tx, project, id, false)
		return e
	})
	return out, e
}
