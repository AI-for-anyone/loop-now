package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"regexp"
	"time"
)

const cliOperationColumns = `id,project_id,cli_name,auth_type,target_account_id,target_revision,name,status,account_id,started_at,expires_at,completed_at,authorization_url,error_code,user_code`

// A final deadline fence failed after sealing. The entire transaction rolls
// back; the service records its terminal state using a fresh bounded context.
var ErrCLIAuthExpired = errors.New("CLI登录提交期限已过")

var githubUserCode = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func scanCLIOperation(row pgx.Row) (cm.CLIAuthOperation, error) {
	var o cm.CLIAuthOperation
	e := row.Scan(&o.ID, &o.ProjectID, &o.CLI, &o.AuthType, &o.TargetAccountID, &o.TargetRevision, &o.Name, &o.Status, &o.AccountID, &o.StartedAt, &o.ExpiresAt, &o.CompletedAt, &o.AuthorizationURL, &o.ErrorCode, &o.UserCode)
	return o, e
}
func cliOperationTx(ctx context.Context, tx pgx.Tx, project, id uuid.UUID, lock bool) (cm.CLIAuthOperation, error) {
	mode := ""
	if lock {
		mode = " FOR UPDATE"
	}
	o, e := scanCLIOperation(tx.QueryRow(ctx, `SELECT `+cliOperationColumns+` FROM project_cli_auth_operations WHERE id=$1 AND project_id=$2`+mode, id, project))
	if e == nil && o.CLI == "lark-cli" {
		e = tx.QueryRow(ctx, `SELECT source_account_id,source_version_id,source_revision FROM project_cli_auth_operations WHERE id=$1 AND project_id=$2`, id, project).Scan(&o.SourceAccountID, &o.SourceVersionID, &o.SourceRevision)
	}
	return o, notFoundIfNoRows(e, "CLI登录操作")
}
func cliOperationTerminal(status string) bool {
	return status != "pending" && status != "awaiting_authorization"
}
func cliInProgress(project, id uuid.UUID, cli string) error {
	e := apperr.Conflict("cli_auth_in_progress", "已有CLI登录操作或操作内容冲突")
	e.Details = struct {
		ProjectID   uuid.UUID `json:"projectId"`
		OperationID uuid.UUID `json:"operationId"`
		CLI         string    `json:"cli"`
	}{project, id, cli}
	return e
}
func expireCLIOperationsTx(ctx context.Context, tx pgx.Tx, project uuid.UUID) error {
	_, e := tx.Exec(ctx, `UPDATE project_cli_auth_operations SET status='expired',authorization_url=NULL,user_code=NULL,error_code='auth_timeout',completed_at=clock_timestamp() WHERE project_id=$1 AND status IN('pending','awaiting_authorization') AND expires_at<=clock_timestamp()`, project)
	return e
}
func (s *Store) NewProjectCLIAuthOperation(ctx context.Context, project uuid.UUID, in cm.CLIAuthStart, expires time.Time) (cm.CLIAuthOperation, bool, error) {
	if in.CLI == "lark-cli" {
		return s.newProjectLarkAuthOperation(ctx, project, in, expires)
	}
	if in.SourceAccountID != nil || in.ExpectedSourceRevision != nil {
		return cm.CLIAuthOperation{}, false, apperr.Invalid("body", "该CLI不接受应用来源字段")
	}
	var out cm.CLIAuthOperation
	created := false
	raw, _ := json.Marshal(in)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if e := expireCLIOperationsTx(ctx, tx, project); e != nil {
			return e
		}
		var oldHash, oldCLI string
		e := tx.QueryRow(ctx, `SELECT request_hash,cli_name FROM project_cli_auth_operations WHERE id=$1 AND project_id=$2`, in.OperationID, project).Scan(&oldHash, &oldCLI)
		if e == nil {
			if oldHash != hash {
				return cliInProgress(project, in.OperationID, oldCLI)
			}
			out, e = cliOperationTx(ctx, tx, project, in.OperationID, false)
			return e
		}
		if e != pgx.ErrNoRows {
			return e
		}
		if !(in.CLI == "gcloud" && in.AuthType == "google_user" || in.CLI == "gh" && in.AuthType == "github_user") || in.OperationID == uuid.Nil {
			return apperr.Invalid("cli", "CLI登录类型无效")
		}
		if in.AccountID == nil && (in.ExpectedRevision != nil || in.CLI == "gcloud" && in.Name == nil || in.CLI == "gh" && in.Name != nil) || in.AccountID != nil && (*in.AccountID == uuid.Nil || in.ExpectedRevision == nil || *in.ExpectedRevision < 1 || in.Name != nil) {
			return apperr.Invalid("body", "CLI登录目标无效")
		}
		if in.AccountID != nil {
			var owner uuid.UUID
			var purpose string
			var cli *string
			if e := tx.QueryRow(ctx, `SELECT project_id,purpose,cli_name FROM project_credential_accounts WHERE id=$1`, *in.AccountID).Scan(&owner, &purpose, &cli); e != nil {
				return notFoundIfNoRows(e, "项目账号")
			}
			if owner != project || purpose != PurposeCLI || cli == nil || *cli != in.CLI {
				return apperr.Invalid("accountId", "目标CLI用途或项目不匹配")
			}
			r, e := projectAccountTx(ctx, tx, project, *in.AccountID, true)
			if e != nil {
				return e
			}
			if r.View.DeletedAt != nil {
				return projectAccountDeleted(project, r.View.ID)
			}
			if r.View.Purpose != PurposeCLI || r.View.CLI == nil || *r.View.CLI != in.CLI || in.ExpectedRevision == nil {
				return apperr.Invalid("accountId", "目标CLI用途不匹配")
			}
			if e = credentialCAS("account", r.View.ID, *in.ExpectedRevision, r.View.Revision); e != nil {
				return e
			}
		}
		var active uuid.UUID
		e = tx.QueryRow(ctx, `SELECT id FROM project_cli_auth_operations WHERE project_id=$1 AND target_account_id IS NOT DISTINCT FROM $2::uuid AND cli_name=$3 AND status IN('pending','awaiting_authorization')`, project, in.AccountID, in.CLI).Scan(&active)
		if e == nil {
			return cliInProgress(project, active, in.CLI)
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
		out, e = scanCLIOperation(tx.QueryRow(ctx, `INSERT INTO project_cli_auth_operations(id,project_id,cli_name,auth_type,request_hash,target_account_id,target_revision,name,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)RETURNING `+cliOperationColumns, in.OperationID, project, in.CLI, in.AuthType, hash, in.AccountID, in.ExpectedRevision, in.Name, expires))
		created = e == nil
		return e
	})
	return out, created, err
}
func (s *Store) GetProjectCLIAuthOperation(ctx context.Context, project, id uuid.UUID) (cm.CLIAuthOperation, error) {
	var out cm.CLIAuthOperation
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if e := expireCLIOperationsTx(ctx, tx, project); e != nil {
			return e
		}
		var e error
		out, e = cliOperationTx(ctx, tx, project, id, false)
		return e
	})
	return out, e
}
func (s *Store) ListProjectCLIAuthOperations(ctx context.Context, project uuid.UUID, account *uuid.UUID, cliFilter ...string) ([]cm.CLIAuthOperation, error) {
	out := []cm.CLIAuthOperation{}
	cli := "gcloud"
	if len(cliFilter) > 0 {
		cli = cliFilter[0]
	}
	if len(cliFilter) > 1 || cli != "gcloud" && cli != "gh" && cli != "lark-cli" {
		return nil, apperr.Invalid("cli", "CLI登录筛选无效")
	}
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		if e := expireCLIOperationsTx(ctx, tx, project); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT `+cliOperationColumns+` FROM project_cli_auth_operations WHERE project_id=$1 AND cli_name=$3 AND status IN('pending','awaiting_authorization') AND ($2::uuid IS NULL OR target_account_id=$2) ORDER BY started_at,id`, project, account, cli)
		if e != nil {
			return e
		}
		for rows.Next() {
			o, e := scanCLIOperation(rows)
			if e != nil {
				rows.Close()
				return e
			}
			out = append(out, o)
		}
		e = rows.Err()
		rows.Close()
		if e == nil && cli == "lark-cli" {
			for i := range out {
				if e = tx.QueryRow(ctx, `SELECT source_account_id,source_version_id,source_revision FROM project_cli_auth_operations WHERE id=$1`, out[i].ID).Scan(&out[i].SourceAccountID, &out[i].SourceVersionID, &out[i].SourceRevision); e != nil {
					return e
				}
			}
		}
		return e
	})
	return out, e
}
func (s *Store) SetProjectCLIAuthURL(ctx context.Context, project, id uuid.UUID, url string) (cm.CLIAuthOperation, error) {
	var out cm.CLIAuthOperation
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
		if out.CLI != "gcloud" {
			return apperr.Invalid("cli", "Google授权操作类型不匹配")
		}
		out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='awaiting_authorization',authorization_url=$2 WHERE id=$1 RETURNING `+cliOperationColumns, id, url))
		return e
	})
	return out, e
}

func (s *Store) SetProjectGitHubAuthorization(ctx context.Context, project, id uuid.UUID, url, code string, expires time.Time) (cm.CLIAuthOperation, error) {
	var out cm.CLIAuthOperation
	if url != "https://github.com/login/device" || !githubUserCode.MatchString(code) || expires.IsZero() {
		return out, apperr.Invalid("authorization", "GitHub授权交互无效")
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
		if out.CLI != "gh" || out.AuthType != "github_user" {
			return apperr.Invalid("cli", "GitHub授权操作类型不匹配")
		}
		var live bool
		if e = tx.QueryRow(ctx, `SELECT least($1::timestamptz,$2::timestamptz)>clock_timestamp()`, expires, out.ExpiresAt).Scan(&live); e != nil {
			return e
		}
		if !live {
			out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='expired',authorization_url=NULL,user_code=NULL,error_code='auth_timeout',completed_at=clock_timestamp() WHERE id=$1 RETURNING `+cliOperationColumns, id))
			return e
		}
		out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='awaiting_authorization',authorization_url=$2,user_code=$3,expires_at=least(expires_at,$4) WHERE id=$1 RETURNING `+cliOperationColumns, id, url, code, expires))
		return e
	})
	return out, e
}
func (s *Store) EndProjectCLIAuthOperation(ctx context.Context, project, id uuid.UUID, status, code string) (cm.CLIAuthOperation, error) {
	var out cm.CLIAuthOperation
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		// May run after owner deletion; never changes credentials or revives an op.
		if _, e := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id=$1 FOR UPDATE`, project); e != nil {
			return e
		}
		var e error
		out, e = cliOperationTx(ctx, tx, project, id, true)
		if e != nil || cliOperationTerminal(out.Status) {
			return e
		}
		if status != "failed" && status != "cancelled" && status != "expired" {
			return apperr.Invalid("status", "CLI登录终态无效")
		}
		source := out
		out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status=$2,error_code=$3,authorization_url=NULL,user_code=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+cliOperationColumns, id, status, code))
		if e == nil && out.CLI == "lark-cli" {
			out.SourceAccountID = source.SourceAccountID
			out.SourceVersionID = source.SourceVersionID
			out.SourceRevision = source.SourceRevision
		}
		return e
	})
	return out, e
}
func (s *Store) CompleteProjectCLIAuthOperation(ctx context.Context, project, id uuid.UUID, w ProjectCredentialWrite) (cm.CLIAuthOperation, *ProjectCredentialRecord, error) {
	var out cm.CLIAuthOperation
	var saved *ProjectCredentialRecord
	e := s.inTx(ctx, func(tx pgx.Tx) error {
		var deleted *time.Time
		if e := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id=$1 FOR UPDATE`, project).Scan(&deleted); e != nil {
			return notFoundIfNoRows(e, "项目")
		}
		initial, e := cliOperationTx(ctx, tx, project, id, false)
		if e != nil {
			return e
		}
		defer func() {
			if out.CLI == "lark-cli" {
				out.SourceAccountID = initial.SourceAccountID
				out.SourceVersionID = initial.SourceVersionID
				out.SourceRevision = initial.SourceRevision
			}
		}()

		var target *ProjectCredentialRecord
		if initial.TargetAccountID != nil {
			r, e := projectAccountTx(ctx, tx, project, *initial.TargetAccountID, true)
			if e != nil {
				return e
			}
			target = &r
		}
		out, e = cliOperationTx(ctx, tx, project, id, true)
		if e != nil || cliOperationTerminal(out.Status) {
			return e
		}
		status, code := "", ""
		if deleted != nil {
			status, code = "cancelled", "auth_cancelled"
		} else if !out.ExpiresAt.After(time.Now()) {
			status, code = "expired", "auth_timeout"
		} else if target != nil && (target.View.DeletedAt != nil || out.TargetRevision == nil || target.View.Revision != *out.TargetRevision) {
			status, code = "failed", "version_conflict"
		}
		if status != "" {
			out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status=$2,error_code=$3,authorization_url=NULL,user_code=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+cliOperationColumns, id, status, code))
			return e
		}
		if out.CLI == "gh" && (w.AuthMethod != "oauth" || w.GitHubIdentity == nil || !w.GitHubIdentity.Valid()) {
			return apperr.Invalid("identity", "GitHub核实身份缺失")
		}
		if out.CLI == "lark-cli" {
			if w.AuthMethod != "oauth" || w.LarkIdentity == nil || !w.LarkIdentity.Valid() {
				return apperr.Invalid("identity", "Lark核实身份缺失")
			}
			if out.TargetAccountID == nil {
				if out.SourceAccountID == nil || out.SourceVersionID == nil || out.SourceRevision == nil {
					return apperr.Invalid("sourceAccountId", "Lark应用来源缺失")
				}
				source, err := projectAccountTx(ctx, tx, project, *out.SourceAccountID, true)
				if err != nil {
					return err
				}
				if source.View.DeletedAt != nil || source.View.Revision != *out.SourceRevision || source.View.CurrentVersionID == nil || *source.View.CurrentVersionID != *out.SourceVersionID {
					out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='failed',error_code='version_conflict',authorization_url=NULL,user_code=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+cliOperationColumns, id))
					return e
				}
			}
			// Source/target and operation locks may have waited since the first
			// deadline check. Use a fresh database clock before any seal callback.
			var live bool
			if e = tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM project_cli_auth_operations WHERE id=$1`, id).Scan(&live); e != nil {
				return e
			}
			if !live {
				out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='expired',error_code='auth_timeout',authorization_url=NULL,user_code=NULL,completed_at=clock_timestamp() WHERE id=$1 RETURNING `+cliOperationColumns, id))
				return e
			}
		}
		var r ProjectCredentialRecord
		if target == nil {
			name := out.Name
			if out.CLI == "gh" {
				name = &w.GitHubIdentity.Login
			}
			if out.CLI == "lark-cli" {
				name = &w.LarkIdentity.DisplayName
			}
			if name == nil {
				return apperr.Invalid("name", "CLI登录名称缺失")
			}
			r, e = insertProjectAccountTx(ctx, tx, project, PurposeCLI, out.CLI, *name, w)
		} else {
			code := "target_changed"
			if e = terminateVerificationsTx(ctx, tx, project, &target.View.ID, &code); e != nil {
				return e
			}
			if _, e = insertProjectCredentialVersion(ctx, tx, target.View, w); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, target.View.ID); e != nil {
				return e
			}
			r, e = projectAccountTx(ctx, tx, project, target.View.ID, false)
		}
		if e != nil {
			return e
		}
		if e = bumpCredentialProject(ctx, tx, project); e != nil {
			return e
		}
		out, e = scanCLIOperation(tx.QueryRow(ctx, `UPDATE project_cli_auth_operations SET status='succeeded',account_id=$2,authorization_url=NULL,user_code=NULL,error_code=NULL,completed_at=clock_timestamp() WHERE id=$1 AND expires_at>clock_timestamp() RETURNING `+cliOperationColumns, id, r.View.ID))
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrCLIAuthExpired
		}
		if e == nil && out.CLI == "lark-cli" {
			out.SourceAccountID = initial.SourceAccountID
			out.SourceVersionID = initial.SourceVersionID
			out.SourceRevision = initial.SourceRevision
		}
		if e == nil {
			saved = &r
		}
		return e
	})
	if e != nil {
		saved = nil
	} else if saved != nil && out.TargetAccountID != nil && s.VerificationInvalidated != nil {
		s.VerificationInvalidated(project, out.TargetAccountID)
	}
	return out, saved, e
}
func (s *Store) RecoverProjectCLIAuthOperations(ctx context.Context) error {
	_, e := s.Pool.Exec(ctx, `UPDATE project_cli_auth_operations SET status='failed',error_code='server_restarted',authorization_url=NULL,user_code=NULL,completed_at=clock_timestamp() WHERE status IN('pending','awaiting_authorization')`)
	return e
}
