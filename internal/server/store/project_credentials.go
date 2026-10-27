package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"net/http"
	"strings"
)

// ProjectCredentialRecord is an internal credential record, never an HTTP DTO.
type ProjectCredentialRecord struct {
	View           cm.Account
	Model          *ModelAuthVersion
	CLI            *CredentialRow
	TemplateSource string
}
type ProjectCredentialWrite struct {
	AuthMethod     string
	AccountLabel   *string
	GitHubIdentity *cm.GitHubIdentity
	LarkIdentity   *cm.LarkIdentity
	Meta           CredentialMeta
	TemplateSource string
	Secret         []byte
}

func lockCredentialProject(ctx context.Context, tx pgx.Tx, id uuid.UUID, write bool) (int64, error) {
	mode := "FOR SHARE"
	if write {
		mode = "FOR UPDATE"
	}
	var rev int64
	err := tx.QueryRow(ctx, `SELECT credentials_revision FROM projects WHERE id=$1 AND deleted_at IS NULL `+mode, id).Scan(&rev)
	return rev, notFoundIfNoRows(err, "项目")
}
func bumpCredentialProject(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE projects SET credentials_revision=credentials_revision+1 WHERE id=$1`, id)
	return err
}
func credentialCAS(kind string, id uuid.UUID, expected, current int64) error {
	if expected < 1 {
		return apperr.Invalid("expectedRevision", "必须提供正整数版本")
	}
	if expected == current {
		return nil
	}
	e := apperr.PreconditionFailed("凭证配置已更改，请重新读取")
	e.Details = cm.PreconditionDetails{ResourceKind: kind, ResourceID: id, ExpectedVersion: expected, CurrentVersion: current}
	return e
}
func projectAccountDeleted(project, id uuid.UUID) error {
	e := apperr.Conflict(apperr.CodeCredentialAccountDeleted, "账号已删除，不会自动切换")
	e.Details = struct {
		ProjectID uuid.UUID `json:"projectId"`
		AccountID uuid.UUID `json:"accountId"`
	}{project, id}
	return e
}
func projectCredentialRequired(a Agent) error {
	e := apperr.Conflict(apperr.CodeProjectCredentialRequired, "请使用项目凭证管理接口")
	e.Details = struct {
		AgentID   uuid.UUID `json:"agentId"`
		ProjectID uuid.UUID `json:"projectId"`
	}{a.ID, *a.ProjectID}
	return e
}

const projectAccountColumns = `id,project_id,purpose,provider,cli_name,name,revision,coalesce(current_model_version_id,current_cli_version_id),created_at,updated_at,deleted_at`

func projectAccountTx(ctx context.Context, tx pgx.Tx, project, id uuid.UUID, lock bool) (ProjectCredentialRecord, error) {
	var r ProjectCredentialRecord
	a := &r.View
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT `+projectAccountColumns+` FROM project_credential_accounts WHERE id=$1 AND project_id=$2`+suffix, id, project).Scan(&a.ID, &a.ProjectID, &a.Purpose, &a.Provider, &a.CLI, &a.Name, &a.Revision, &a.CurrentVersionID, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt)
	if err != nil {
		return r, notFoundIfNoRows(err, "项目账号")
	}
	a.Status = "unavailable"
	if a.CurrentVersionID != nil {
		if a.Purpose == PurposeModel {
			v, e := scanModelAuthVersion(tx.QueryRow(ctx, `SELECT `+modelAuthVersionColumns+` FROM model_auth_versions WHERE id=$1 AND project_account_id=$2`, *a.CurrentVersionID, a.ID))
			if e != nil {
				return r, e
			}
			r.Model = &v
			a.AuthMethod = &v.AuthMethod
			a.AccountLabel = v.AccountLabel
			a.Status = "saved"
			if v.AuthMethod == "oauth" {
				a.Status = "logged_in"
			}
			if v.RefreshState == "unavailable" {
				a.Status = "requires_login"
				if v.AuthMethod != "oauth" {
					a.Status = "unavailable"
				}
			}
		} else {
			var c CredentialRow
			c.Purpose = PurposeCLI
			c.CLIName = *a.CLI
			err = tx.QueryRow(ctx, `SELECT id,template_source,template_version,secret,field_names,env_names,file_paths,updated_at FROM project_cli_versions WHERE id=$1 AND account_id=$2`, *a.CurrentVersionID, a.ID).Scan(&c.ID, &r.TemplateSource, &c.TemplateVersion, &c.Secret, &c.FieldNames, &c.EnvNames, &c.FilePaths, &c.UpdatedAt)
			if err != nil {
				return r, err
			}
			r.CLI = &c
			a.Status = "saved"
			if c.CLIName == "gcloud" || c.CLIName == "gh" || c.CLIName == "lark-cli" {
				var method string
				var label *string
				if err = tx.QueryRow(ctx, `SELECT auth_method,account_label FROM project_cli_versions WHERE id=$1`, c.ID).Scan(&method, &label); err != nil {
					return r, err
				}
				if method != "static" {
					a.AuthMethod = &method
					a.AccountLabel = label
				}
				if method == "oauth" && c.CLIName == "gcloud" {
					var state string
					if err = tx.QueryRow(ctx, `SELECT refresh_state FROM google_cli_grants WHERE version_id=$1`, c.ID).Scan(&state); err != nil {
						return r, err
					}
					if state == "unavailable" {
						a.Status = "requires_login"
					} else {
						a.Status = "logged_in"
					}
				}
				if method == "oauth" && c.CLIName == "lark-cli" {
					identity := &cm.LarkIdentity{}
					var state string
					if err = tx.QueryRow(ctx, `SELECT i.brand,i.app_id,i.open_id,i.display_name,i.granted_scopes,i.verified_at,g.refresh_state FROM lark_cli_identities i JOIN lark_cli_grants g ON g.version_id=i.version_id WHERE i.version_id=$1 AND i.account_id=$2`, c.ID, a.ID).Scan(&identity.Brand, &identity.AppID, &identity.OpenID, &identity.DisplayName, &identity.GrantedScopes, &identity.VerifiedAt, &state); err != nil {
						return r, err
					}
					if !identity.Valid() {
						return r, errors.New("Lark身份元数据无效")
					}
					a.LarkIdentity = identity
					a.LarkApplication = &cm.LarkApplication{Brand: identity.Brand, AppID: identity.AppID}
					a.Status = "logged_in"
					if state == "unavailable" {
						a.Status = "requires_login"
					}
				}
				if method == "oauth" && c.CLIName == "gh" {
					identity := &cm.GitHubIdentity{}
					if err = tx.QueryRow(ctx, `SELECT host,user_id,login,granted_scopes,verified_at FROM github_cli_identities WHERE version_id=$1 AND account_id=$2`, c.ID, a.ID).Scan(&identity.Host, &identity.UserID, &identity.Login, &identity.GrantedScopes, &identity.VerifiedAt); err != nil {
						return r, err
					}
					if !identity.Valid() {
						return r, errors.New("GitHub身份元数据无效")
					}
					a.GitHubIdentity = identity
					a.Status = "logged_in"
				}
			}
			c.TemplateSource = r.TemplateSource
			a.Template = &cm.Template{Source: r.TemplateSource, CredentialFormat: c.TemplateVersion, FieldNames: c.FieldNames, Warnings: []clitemplate.Warning{}, Status: "available"}
		}
	}
	if a.DeletedAt != nil {
		a.Status = "deleted"
	}
	return r, nil
}
func projectAccountsTx(ctx context.Context, tx pgx.Tx, project uuid.UUID, purpose, slot string) ([]ProjectCredentialRecord, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM project_credential_accounts WHERE project_id=$1 AND deleted_at IS NULL AND ($2='' OR purpose=$2) AND ($3='' OR slot_key=$3) ORDER BY created_at,id`, project, purpose, slot)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, err
	}
	out := []ProjectCredentialRecord{}
	for _, id := range ids {
		r, e := projectAccountTx(ctx, tx, project, id, false)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Store) ListProjectCredentials(ctx context.Context, project uuid.UUID, purpose, slot string) ([]ProjectCredentialRecord, int64, error) {
	var out []ProjectCredentialRecord
	var rev int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var e error
		rev, e = lockCredentialProject(ctx, tx, project, false)
		if e != nil {
			return e
		}
		out, e = projectAccountsTx(ctx, tx, project, purpose, slot)
		return e
	})
	return out, rev, err
}
func (s *Store) GetProjectCredential(ctx context.Context, project, id uuid.UUID) (ProjectCredentialRecord, error) {
	var out ProjectCredentialRecord
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, false); e != nil {
			return e
		}
		var e error
		out, e = projectAccountTx(ctx, tx, project, id, false)
		return e
	})
	return out, err
}
func insertProjectCredentialVersion(ctx context.Context, tx pgx.Tx, account cm.Account, w ProjectCredentialWrite) (uuid.UUID, error) {
	if w.Secret == nil {
		return uuid.Nil, errors.New("缺少凭证内容")
	}
	githubOAuth := account.Purpose == PurposeCLI && account.CLI != nil && *account.CLI == "gh" && w.AuthMethod == "oauth"
	if githubOAuth && (w.GitHubIdentity == nil || !w.GitHubIdentity.Valid() || w.AccountLabel == nil || *w.AccountLabel != w.GitHubIdentity.Login) || !githubOAuth && w.GitHubIdentity != nil {
		return uuid.Nil, apperr.Invalid("identity", "GitHub身份与凭证用途不匹配")
	}
	larkOAuth := account.Purpose == PurposeCLI && account.CLI != nil && *account.CLI == "lark-cli" && w.AuthMethod == "oauth"
	if larkOAuth && (w.LarkIdentity == nil || !w.LarkIdentity.Valid() || w.AccountLabel == nil || *w.AccountLabel != w.LarkIdentity.OpenID) || !larkOAuth && w.LarkIdentity != nil {
		return uuid.Nil, apperr.Invalid("identity", "Lark身份与凭证用途不匹配")
	}
	id := uuid.Must(uuid.NewV7())
	var err error
	if account.Purpose == PurposeModel {
		_, err = tx.Exec(ctx, `INSERT INTO model_auth_versions(id,project_account_id,provider,auth_method,account_label,secret)VALUES($1,$2,$3,$4,$5,$6)`, id, account.ID, *account.Provider, w.AuthMethod, w.AccountLabel, w.Secret)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, account.ID, id)
		}
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO project_cli_versions(id,account_id,cli_name,template_source,template_version,secret,field_names,env_names,file_paths)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, account.ID, *account.CLI, w.TemplateSource, w.Meta.TemplateVersion, w.Secret, nonNilStrings(w.Meta.FieldNames), nonNilStrings(w.Meta.EnvNames), nonNilStrings(w.Meta.FilePaths))
		if err == nil && account.CLI != nil && (*account.CLI == "gcloud" || *account.CLI == "gh" || *account.CLI == "lark-cli") && w.AuthMethod != "" {
			_, err = tx.Exec(ctx, `UPDATE project_cli_versions SET auth_method=$2,account_label=$3 WHERE id=$1`, id, w.AuthMethod, w.AccountLabel)
			if err == nil && w.AuthMethod == "oauth" && *account.CLI == "gcloud" {
				_, err = tx.Exec(ctx, `INSERT INTO google_cli_grants(version_id,account_id)VALUES($1,$2)`, id, account.ID)
			}
		}
		if err == nil && larkOAuth {
			i := w.LarkIdentity
			_, err = tx.Exec(ctx, `INSERT INTO lark_cli_identities(version_id,account_id,brand,app_id,open_id,display_name,granted_scopes,verified_at,runtime_variant_version)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, account.ID, i.Brand, i.AppID, i.OpenID, i.DisplayName, i.GrantedScopes, i.VerifiedAt, clicredential.LarkUserVariant)
			if err == nil {
				_, err = tx.Exec(ctx, `INSERT INTO lark_cli_grants(version_id)VALUES($1)`, id)
			}
		}
		if err == nil && githubOAuth {
			i := w.GitHubIdentity
			_, err = tx.Exec(ctx, `INSERT INTO github_cli_identities(version_id,account_id,host,user_id,login,granted_scopes,verified_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, id, account.ID, i.Host, i.UserID, i.Login, i.GrantedScopes, i.VerifiedAt)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, account.ID, id)
		}
	}
	return id, err
}
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
func insertProjectAccountTx(ctx context.Context, tx pgx.Tx, project uuid.UUID, purpose, slot, name string, w ProjectCredentialWrite) (ProjectCredentialRecord, error) {
	id := uuid.Must(uuid.NewV7())
	var provider, cli *string
	if purpose == PurposeModel {
		provider = &slot
	} else if purpose == PurposeCLI {
		cli = &slot
	} else {
		return ProjectCredentialRecord{}, apperr.Invalid("purpose", "用途无效")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_credential_accounts(id,project_id,purpose,provider,cli_name,name)VALUES($1,$2,$3,$4,$5,$6)`, id, project, purpose, provider, cli, name); err != nil {
		return ProjectCredentialRecord{}, err
	}
	a := cm.Account{ID: id, ProjectID: project, Purpose: purpose, Provider: provider, CLI: cli}
	if _, err := insertProjectCredentialVersion(ctx, tx, a, w); err != nil {
		return ProjectCredentialRecord{}, err
	}
	return projectAccountTx(ctx, tx, project, id, false)
}
func (s *Store) CreateProjectCredential(ctx context.Context, project uuid.UUID, purpose, slot, name string, w ProjectCredentialWrite) (ProjectCredentialRecord, error) {
	var out ProjectCredentialRecord
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		var e error
		out, e = insertProjectAccountTx(ctx, tx, project, purpose, slot, name, w)
		if e != nil {
			return e
		}
		return bumpCredentialProject(ctx, tx, project)
	})
	return out, err
}
func (s *Store) MutateProjectCredential(ctx context.Context, project, id uuid.UUID, expected int64, name *string, w *ProjectCredentialWrite, remove bool) (ProjectCredentialRecord, error) {
	var out ProjectCredentialRecord
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, true); e != nil {
			return e
		}
		r, e := projectAccountTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		if e = credentialCAS("account", id, expected, r.View.Revision); e != nil {
			return e
		}
		if r.View.DeletedAt != nil {
			return projectAccountDeleted(project, id)
		}
		if w != nil || remove {
			code := "target_changed"
			if e = terminateVerificationsTx(ctx, tx, project, &id, &code); e != nil {
				return e
			}
		}
		if name != nil {
			if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET name=$2 WHERE id=$1`, id, *name); e != nil {
				return e
			}
		}
		if w != nil {
			if _, e = insertProjectCredentialVersion(ctx, tx, r.View, *w); e != nil {
				return e
			}
		}
		if remove {
			if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET deleted_at=clock_timestamp() WHERE id=$1`, id); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE project_model_auth_operations SET status='cancelled',authorization_url=NULL,error_code=NULL,completed_at=clock_timestamp() WHERE target_account_id=$1 AND status IN('pending','awaiting_authorization')`, id); e != nil {
				return e
			}
		}
		if remove {
			if _, e = tx.Exec(ctx, `UPDATE project_cli_auth_operations SET status='cancelled',authorization_url=NULL,user_code=NULL,error_code='auth_cancelled',completed_at=clock_timestamp() WHERE target_account_id=$1 AND status IN('pending','awaiting_authorization')`, id); e != nil {
				return e
			}
			if e = settleDeletedAccountTx(ctx, tx, project, id); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, id); e != nil {
			return e
		}
		if e = bumpCredentialProject(ctx, tx, project); e != nil {
			return e
		}
		out, e = projectAccountTx(ctx, tx, project, id, false)
		return e
	})
	if err == nil && (w != nil || remove) && s.VerificationInvalidated != nil {
		s.VerificationInvalidated(project, &id)
	}
	return out, err
}
func lockProjectCredentialAgent(ctx context.Context, tx pgx.Tx, agent uuid.UUID, write bool) (Agent, int64, error) {
	var project *uuid.UUID
	if e := tx.QueryRow(ctx, `SELECT project_id FROM agents WHERE id=$1 AND deleted_at IS NULL`, agent).Scan(&project); e != nil {
		return Agent{}, 0, notFoundIfNoRows(e, "Agent")
	}
	if project == nil {
		return Agent{}, 0, apperr.Invalid("agentId", "平台Agent使用原私有凭证接口")
	}
	rev, e := lockCredentialProject(ctx, tx, *project, write)
	if e != nil {
		return Agent{}, 0, e
	}
	mode := "FOR SHARE"
	if write {
		mode = "FOR UPDATE"
	}
	a, e := scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id=$1 AND deleted_at IS NULL `+mode, agent))
	return a, rev, notFoundIfNoRows(e, "Agent")
}
func resolveProjectCredentialTx(ctx context.Context, tx pgx.Tx, a Agent, purpose, slot string) (cm.Selection, *ProjectCredentialRecord, error) {
	out := cm.Selection{Purpose: purpose, Mode: "automatic", Status: "not_configured", Candidates: []cm.Account{}}
	if purpose == PurposeModel {
		out.Provider = &slot
	} else {
		out.CLI = &slot
	}
	var selected *uuid.UUID
	var mode string
	err := tx.QueryRow(ctx, `SELECT account_id,mode FROM agent_credential_selections WHERE agent_id=$1 AND purpose=$2 AND slot_key=$3`, a.ID, purpose, slot).Scan(&selected, &mode)
	present := err == nil
	if err != nil && err != pgx.ErrNoRows {
		return out, nil, err
	}
	records, err := projectAccountsTx(ctx, tx, *a.ProjectID, purpose, slot)
	if err != nil {
		return out, nil, err
	}
	for _, r := range records {
		out.Candidates = append(out.Candidates, r.View)
	}
	if present && mode == "explicit" {
		out.Mode = "explicit"
		out.SelectedAccountID = selected
		if selected == nil {
			return out, nil, errors.New("明确选择缺少账号")
		}
		r, e := projectAccountTx(ctx, tx, *a.ProjectID, *selected, false)
		if e != nil {
			return out, nil, e
		}
		if r.View.DeletedAt != nil {
			out.Status = "account_deleted"
			return out, nil, nil
		}
		if r.View.Purpose != purpose || (purpose == PurposeModel && (r.View.Provider == nil || *r.View.Provider != slot)) || (purpose == PurposeCLI && (r.View.CLI == nil || *r.View.CLI != slot)) {
			out.Status = "scope_mismatch"
			return out, nil, nil
		}
		return finishProjectResolution(out, r)
	}
	if !present && purpose == PurposeModel {
		var cleared bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_model_legacy_cleared WHERE agent_id=$1)`, a.ID).Scan(&cleared); err != nil {
			return out, nil, err
		}
		if cleared {
			out.Mode = "legacy_cleared"
			return out, nil, nil
		}
	}
	if len(records) > 1 {
		out.Status = "selection_required"
		return out, nil, nil
	}
	if len(records) == 0 {
		return out, nil, nil
	}
	return finishProjectResolution(out, records[0])
}
func finishProjectResolution(out cm.Selection, r ProjectCredentialRecord) (cm.Selection, *ProjectCredentialRecord, error) {
	out.ResolvedAccountID = &r.View.ID
	out.ResolvedVersionID = r.View.CurrentVersionID
	out.Status = "resolved"
	if r.View.Status == "unavailable" || r.View.Status == "requires_login" {
		out.Status = r.View.Status
	}
	return out, &r, nil
}
func selectionResolutionError(a Agent, v cm.Selection) error {
	code, status := apperr.CodeCredentialSelectionRequired, http.StatusConflict
	switch v.Status {
	case "resolved", "not_configured":
		return nil
	case "account_deleted":
		code = apperr.CodeCredentialAccountDeleted
	case "scope_mismatch":
		code = apperr.CodeCredentialScopeMismatch
		status = http.StatusUnprocessableEntity
	case "unavailable", "requires_login":
		return apperr.Invalid("credentials", "项目账号不可用，请检查账号状态")
	}
	ids := []uuid.UUID{}
	for _, c := range v.Candidates {
		ids = append(ids, c.ID)
	}
	result := &apperr.Error{Status: status, Code: code, Message: "项目账号取用需要核对，不会自动切换", Details: cm.ResolutionDetails{AgentID: a.ID, ProjectID: *a.ProjectID, Purpose: v.Purpose, Provider: v.Provider, CLI: v.CLI, AccountID: v.SelectedAccountID, CandidateAccountIDs: ids}}
	if code == apperr.CodeCredentialScopeMismatch {
		result.Fields = []apperr.FieldError{apperr.Field("accountId", "账号用途与提供方/CLI不匹配")}
	}
	return result
}
func selectionsTx(ctx context.Context, tx pgx.Tx, a Agent, rev int64) (cm.Selections, error) {
	out := cm.Selections{AgentID: a.ID, ProjectID: *a.ProjectID, ConfigVersion: a.ConfigVersion, CredentialsRevision: rev, Selections: []cm.Selection{}}
	rows, e := tx.Query(ctx, `SELECT purpose,slot_key FROM (SELECT 'model'::text purpose,$2::text slot_key UNION SELECT purpose,slot_key FROM agent_credential_selections WHERE agent_id=$1 AND purpose='model' UNION SELECT 'cli',cli_name FROM agent_clis WHERE agent_id=$1) slots ORDER BY purpose,slot_key`, a.ID, a.ModelProvider)
	if e != nil {
		return out, e
	}
	type slot struct{ purpose, key string }
	slots, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (slot, error) { var s slot; e := r.Scan(&s.purpose, &s.key); return s, e })
	if e != nil {
		return out, e
	}
	for _, s := range slots {
		view, _, e := resolveProjectCredentialTx(ctx, tx, a, s.purpose, s.key)
		if e != nil {
			return out, e
		}
		out.Selections = append(out.Selections, view)
	}
	return out, nil
}
func (s *Store) GetCredentialSelections(ctx context.Context, agent uuid.UUID) (cm.Selections, error) {
	var out cm.Selections
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, rev, e := lockProjectCredentialAgent(ctx, tx, agent, false)
		if e != nil {
			return e
		}
		out, e = selectionsTx(ctx, tx, a, rev)
		return e
	})
	return out, err
}
func (s *Store) PutCredentialSelection(ctx context.Context, agent uuid.UUID, in cm.SelectionPut) (cm.Selections, error) {
	var out cm.Selections
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, rev, e := lockProjectCredentialAgent(ctx, tx, agent, true)
		if e != nil {
			return e
		}
		if e = credentialCAS("agent", a.ID, in.ExpectedConfigVersion, a.ConfigVersion); e != nil {
			return e
		}
		var slot string
		if in.Purpose == PurposeModel && in.Provider != nil && in.CLI == nil {
			slot = *in.Provider
		} else if in.Purpose == PurposeCLI && in.CLI != nil && in.Provider == nil {
			slot = *in.CLI
			var allowed bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_clis WHERE agent_id=$1 AND cli_name=$2)`, a.ID, slot).Scan(&allowed); e != nil {
				return e
			}
			if !allowed {
				return apperr.Invalid("cli", "Agent未允许该CLI")
			}
		} else {
			return apperr.Invalid("purpose", "用途定位字段不匹配")
		}
		if in.Purpose == PurposeCLI && isDBCLI(slot) {
			present, err := dbSetExistsTx(ctx, tx, a.ID, slot)
			if err != nil {
				return err
			}
			if present {
				return &apperr.Error{Status: 409, Code: apperr.CodeCredentialSelectionModeConflict, Message: "该CLI使用数据库连接集合，请先删除集合恢复单项模式", Details: cm.CredentialSelectionModeConflictDetails{AgentID: a.ID, CLI: slot, Mode: "explicit_set", ConfigVersion: a.ConfigVersion}}
			}
		}
		if in.AccountID != nil {
			r, e := projectAccountTx(ctx, tx, *a.ProjectID, *in.AccountID, true)
			if e != nil {
				return e
			}
			if r.View.DeletedAt != nil {
				return projectAccountDeleted(*a.ProjectID, *in.AccountID)
			}
			if r.View.Purpose != in.Purpose || (r.View.Provider != nil && *r.View.Provider != slot) || (r.View.CLI != nil && *r.View.CLI != slot) {
				v := cm.Selection{Purpose: in.Purpose, Provider: in.Provider, CLI: in.CLI, Status: "scope_mismatch", SelectedAccountID: in.AccountID}
				return selectionResolutionError(a, v)
			}
		}
		mode := "explicit"
		var cleared bool
		if in.AccountID == nil && in.Purpose == PurposeModel {
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_model_legacy_cleared WHERE agent_id=$1)`, a.ID).Scan(&cleared); e != nil {
				return e
			}
		}
		if in.AccountID == nil && !cleared {
			_, e = tx.Exec(ctx, `DELETE FROM agent_credential_selections WHERE agent_id=$1 AND purpose=$2 AND slot_key=$3`, a.ID, in.Purpose, slot)
		} else {
			if in.AccountID == nil {
				mode = "automatic_override"
			}
			_, e = tx.Exec(ctx, `INSERT INTO agent_credential_selections(agent_id,project_id,purpose,slot_key,account_id,mode)VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(agent_id,purpose,slot_key)DO UPDATE SET account_id=excluded.account_id,mode=excluded.mode`, a.ID, *a.ProjectID, in.Purpose, slot, in.AccountID, mode)
		}
		if e != nil {
			return e
		}
		if e = bumpConfig(ctx, tx, a.ID); e != nil {
			return e
		}
		a.ConfigVersion++
		out, e = selectionsTx(ctx, tx, a, rev)
		return e
	})
	return out, err
}
func (s *Store) ProjectCredentialImpact(ctx context.Context, project uuid.UUID, purpose, slot string, account *uuid.UUID) (cm.Impact, error) {
	out := cm.Impact{ProjectID: project, AccountID: account, Agents: []cm.ImpactAgent{}}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		rev, e := lockCredentialProject(ctx, tx, project, false)
		if e != nil {
			return e
		}
		out.CredentialsRevision = rev
		if account != nil {
			r, e := projectAccountTx(ctx, tx, project, *account, false)
			if e != nil {
				return e
			}
			if r.View.Purpose != purpose || (r.View.Provider != nil && *r.View.Provider != slot) || (r.View.CLI != nil && *r.View.CLI != slot) {
				return apperr.Invalid("accountId", "账号用途定位不匹配")
			}
		}
		rows, e := tx.Query(ctx, `SELECT `+agentColumns+` FROM agents WHERE project_id=$1 AND deleted_at IS NULL ORDER BY id FOR SHARE`, project)
		if e != nil {
			return e
		}
		agents, e := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Agent, error) { return scanAgent(r) })
		if e != nil {
			return e
		}
		for _, a := range agents {
			if purpose == PurposeModel && a.ModelProvider != slot {
				continue
			}
			if purpose == PurposeCLI {
				var allowed bool
				if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_clis WHERE agent_id=$1 AND cli_name=$2)`, a.ID, slot).Scan(&allowed); e != nil {
					return e
				}
				if !allowed {
					continue
				}
			}
			if purpose == PurposeCLI && isDBCLI(slot) {
				set, err := dbConnectionsTx(ctx, tx, a, slot, rev)
				if err != nil {
					return err
				}
				if set.Mode == "explicit_set" {
					for _, item := range set.Connections {
						if account == nil || item.AccountID == *account {
							id := item.AccountID
							out.Agents = append(out.Agents, cm.ImpactAgent{AgentID: a.ID, Name: a.Name, Mode: "connection_set", SelectedAccountID: &id, ResolvedAccountID: &id, Status: item.Status})
						}
					}
					continue
				}
			}
			v, _, e := resolveProjectCredentialTx(ctx, tx, a, purpose, slot)
			if e != nil {
				return e
			}
			include := account == nil && v.Mode == "automatic"
			if account != nil {
				include = v.SelectedAccountID != nil && *v.SelectedAccountID == *account || v.Mode == "automatic" && v.ResolvedAccountID != nil && *v.ResolvedAccountID == *account
			}
			if include {
				out.Agents = append(out.Agents, cm.ImpactAgent{AgentID: a.ID, Name: a.Name, Mode: v.Mode, SelectedAccountID: v.SelectedAccountID, ResolvedAccountID: v.ResolvedAccountID, Status: v.Status})
			}
		}
		return nil
	})
	return out, err
}

// captureProjectCredentialsTx is shared by normal and task capture, with the
// caller already holding the project and Agent locks. Never opens a Store tx.
func captureProjectCredentialsTx(ctx context.Context, tx pgx.Tx, a Agent, clis []string) (*modelcredential.Binding, []CredentialRow, []cm.Selection, error) {
	all := []cm.Selection{}
	out := []CredentialRow{}
	v, r, e := resolveProjectCredentialTx(ctx, tx, a, PurposeModel, a.ModelProvider)
	if e != nil {
		return nil, nil, nil, e
	}
	all = append(all, v)
	if e = selectionResolutionError(a, v); e != nil {
		return nil, nil, nil, e
	}
	var binding *modelcredential.Binding
	if r != nil && r.Model != nil {
		binding = &modelcredential.Binding{VersionID: r.Model.ID, AgentID: a.ID, Provider: a.ModelProvider, OwnerKind: "project", ProjectID: a.ProjectID, AccountID: &r.View.ID}
	}
	for _, cli := range clis {
		if isDBCLI(cli) {
			set, err := dbConnectionsTx(ctx, tx, a, cli, 0)
			if err != nil {
				return nil, nil, nil, err
			}
			if set.Mode == "explicit_set" {
				all = append(all, cm.Selection{Purpose: PurposeCLI, CLI: &cli, Mode: "explicit_set", Status: set.Status})
				for _, item := range set.Connections {
					selected := cm.Selection{Purpose: PurposeCLI, CLI: &cli, Mode: "explicit_set", SelectedAccountID: &item.AccountID, ResolvedAccountID: &item.AccountID, ResolvedVersionID: item.VersionID, Status: item.Status}
					if err = selectionResolutionError(a, selected); err != nil {
						return nil, nil, nil, err
					}
					rec, err := projectAccountTx(ctx, tx, *a.ProjectID, item.AccountID, false)
					if err != nil {
						return nil, nil, nil, err
					}
					if rec.CLI == nil {
						return nil, nil, nil, apperr.Invalid("credentials", "数据库凭证不可用")
					}
					row := *rec.CLI
					row.AgentID = a.ID
					copy := item
					row.DBConnection = &copy
					out = append(out, row)
					all = append(all, selected)
				}
				continue
			}
		}

		v, r, e := resolveProjectCredentialTx(ctx, tx, a, PurposeCLI, cli)
		if e != nil {
			return nil, nil, nil, e
		}
		all = append(all, v)
		if e = selectionResolutionError(a, v); e != nil {
			return nil, nil, nil, e
		}
		if r != nil && r.CLI != nil {
			row := *r.CLI
			row.AgentID = a.ID
			if cli == "lark-cli" && r.View.AuthMethod != nil && *r.View.AuthMethod == "oauth" {
				i := r.View.LarkIdentity
				if i == nil || !i.Valid() {
					return nil, nil, nil, apperr.Invalid("credentials", "Lark用户身份不可用")
				}
				row.LarkBinding = &clicredential.LarkBinding{ProjectID: *a.ProjectID, AccountID: r.View.ID, VersionID: row.ID, AgentID: a.ID, Brand: i.Brand, AppID: i.AppID, OpenID: i.OpenID, RuntimeVariantVersion: clicredential.LarkUserVariant}
			}
			if cli == "gcloud" && r.View.AuthMethod != nil && *r.View.AuthMethod == "oauth" {
				row.GoogleBinding = &clicredential.Binding{VersionID: row.ID, AgentID: a.ID, ProjectID: *a.ProjectID, AccountID: r.View.ID, CLI: "gcloud"}
			}
			if isDBCLI(cli) {
				row.DBConnection = &cm.DBConnection{AccountID: r.View.ID, Name: r.View.Name, VersionID: r.View.CurrentVersionID, Status: v.Status}
			}
			out = append(out, row)
		}
	}
	return binding, out, all, nil
}

// Only effective identities, never project-wide counters or token generation.
func projectCredentialIdentity(clis []string, selections []cm.Selection, rows []CredentialRow) string {
	var b strings.Builder
	for _, cli := range clis {
		fmt.Fprintf(&b, "cli:%s;", cli)
	}
	for _, s := range selections {
		fmt.Fprintf(&b, "%s:%s:%v:%v;", s.Purpose, s.Mode, s.ResolvedAccountID, s.ResolvedVersionID)
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "format:%s:%s:%s;", r.CLIName, r.TemplateSource, r.TemplateVersion)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
func (s *Store) ProjectCredentialRevisionTx(ctx context.Context, tx pgx.Tx, project uuid.UUID) (int64, error) {
	var rev int64
	e := tx.QueryRow(ctx, `SELECT credentials_revision FROM projects WHERE id=$1 AND deleted_at IS NULL`, project).Scan(&rev)
	return rev, notFoundIfNoRows(e, "项目")
}

func lockAgentProjectTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, e := tx.Exec(ctx, `SELECT 1 FROM projects WHERE id=(SELECT project_id FROM agents WHERE id=$1) FOR UPDATE`, id)
	return e
}

func (s *Store) GetProjectCLIVersion(ctx context.Context, account, id uuid.UUID) (CredentialRow, string, error) {
	var c CredentialRow
	var source string
	c.Purpose = PurposeCLI
	e := s.Pool.QueryRow(ctx, `SELECT id,cli_name,template_source,template_version,secret,field_names,env_names,file_paths,updated_at FROM project_cli_versions WHERE id=$1 AND account_id=$2`, id, account).Scan(&c.ID, &c.CLIName, &source, &c.TemplateVersion, &c.Secret, &c.FieldNames, &c.EnvNames, &c.FilePaths, &c.UpdatedAt)
	c.TemplateSource = source
	return c, source, notFoundIfNoRows(e, "CLI凭证版本")
}

func (s *Store) RequirePrivateAgentCredentials(ctx context.Context, id uuid.UUID) error {
	a, e := s.GetAgent(ctx, id)
	if e != nil {
		return e
	}
	if a.ProjectID != nil {
		return projectCredentialRequired(a)
	}
	return nil
}
