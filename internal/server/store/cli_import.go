package store

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
)

func importConflict(code string) error {
	return apperr.Conflict(code, "本机导入操作冲突，请读取原操作或重新扫描")
}
func importCLI(v string) bool {
	return v == "gh" || v == "lark-cli" || v == "gcloud" || v == "psql" || v == "mysql"
}
func importCode(v string) string {
	switch v {
	case "source_changed", "source_unreadable", "missing_field", "unsupported", "template_mismatch", "supplement_override", "invalid_credential", "consumer_failed", "precondition_failed", "source_conflict", "provenance_key_changed", "cancelled", "timeout", "worker_restarted", "project_deleted", "internal_error":
		return v
	}
	return "internal_error"
}
func importProjectLock(ctx context.Context, tx pgx.Tx, project uuid.UUID) (int64, bool, error) {
	var rev int64
	var deleted *time.Time
	err := tx.QueryRow(ctx, `SELECT credentials_revision,deleted_at FROM projects WHERE id=$1 FOR UPDATE`, project).Scan(&rev, &deleted)
	return rev, deleted != nil, notFoundIfNoRows(err, "项目")
}
func importOperationTx(ctx context.Context, tx pgx.Tx, project, id uuid.UUID, lock bool) (CLIImportOperation, error) {
	var o CLIImportOperation
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT id,project_id,scan_id,status,phase,created_at,deadline_at,completed_at,error_code,committed_credentials_revision FROM cli_credential_import_operations WHERE id=$1 AND project_id=$2`+suffix, id, project).Scan(&o.ID, &o.ProjectID, &o.ScanID, &o.Status, &o.Phase, &o.CreatedAt, &o.DeadlineAt, &o.CompletedAt, &o.ErrorCode, &o.CredentialsRevision)
	if err != nil {
		return o, notFoundIfNoRows(err, "导入操作")
	}
	rows, err := tx.Query(ctx, `SELECT candidate_id,cli_name,label,status,account_id,version_id,error_code FROM cli_credential_import_items WHERE operation_id=$1 ORDER BY ordinal`, id)
	if err != nil {
		return o, err
	}
	o.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CLIImportItem, error) {
		var i CLIImportItem
		e := r.Scan(&i.CandidateID, &i.CLI, &i.Label, &i.Status, &i.AccountID, &i.VersionID, &i.ErrorCode)
		return i, e
	})
	if o.Items == nil {
		o.Items = []CLIImportItem{}
	}
	return o, err
}

// FindCLIImport restores before consulting ephemeral scans. No source or secret access.
func (s *Store) FindCLIImport(ctx context.Context, project, id uuid.UUID, fingerprint []byte) (CLIImportOperation, error) {
	var out CLIImportOperation
	if project == uuid.Nil || id == uuid.Nil {
		return out, apperr.Invalid("operationId", "UUID不能为零")
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, e := lockCredentialProject(ctx, tx, project, false); e != nil {
			return e
		}
		o, e := importOperationTx(ctx, tx, project, id, false)
		if e != nil {
			return e
		}
		if fingerprint != nil {
			var hash []byte
			if e = tx.QueryRow(ctx, `SELECT request_fingerprint FROM cli_credential_import_operations WHERE id=$1`, id).Scan(&hash); e != nil {
				return e
			}
			if !bytes.Equal(hash, fingerprint) {
				return importConflict("import_operation_conflict")
			}
		}
		out = o
		return nil
	})
	return out, err
}
func (s *Store) GetCLIImport(ctx context.Context, project, id uuid.UUID) (CLIImportOperation, error) {
	return s.FindCLIImport(ctx, project, id, nil)
}

func (s *Store) BeginCLIImport(ctx context.Context, in CLIImportBegin) (CLIImportOperation, bool, error) {
	var out CLIImportOperation
	created := false
	if in.ID == uuid.Nil || in.ProjectID == uuid.Nil || in.ScanID == uuid.Nil || in.InstanceID == uuid.Nil || len(in.RequestFingerprint) != 32 || in.ExpectedRevision < 1 || len(in.ProvenanceKeyID) == 0 || len(in.ProvenanceKeyID) > 200 || len(in.Items) < 1 || len(in.Items) > 20 {
		return out, false, apperr.Invalid("operationId", "导入输入无效")
	}
	seen := map[uuid.UUID]bool{}
	for _, v := range in.Items {
		if v.CandidateID == uuid.Nil || seen[v.CandidateID] || !importCLI(v.CLI) || !utf8.ValidString(v.Label) || utf8.RuneCountInString(v.Label) > 200 || strings.IndexFunc(v.Label, unicode.IsControl) >= 0 {
			return out, false, apperr.Invalid("candidateIds", "导入条目无效")
		}
		seen[v.CandidateID] = true
	}
	err := s.cliImportTx(ctx, func(tx pgx.Tx) error {
		rev, deleted, e := importProjectLock(ctx, tx, in.ProjectID)
		if e != nil {
			return e
		}
		if deleted {
			return apperr.NotFound("项目")
		}
		old, e := importOperationTx(ctx, tx, in.ProjectID, in.ID, true)
		if e == nil {
			var hash []byte
			if e = tx.QueryRow(ctx, `SELECT request_fingerprint FROM cli_credential_import_operations WHERE id=$1`, in.ID).Scan(&hash); e != nil {
				return e
			}
			if !bytes.Equal(hash, in.RequestFingerprint) {
				return importConflict("import_operation_conflict")
			}
			out = old
			return nil
		}
		if !IsNotFound(e) {
			return e
		}
		if e = credentialCAS("project", in.ProjectID, in.ExpectedRevision, rev); e != nil {
			return e
		}
		// Receipts under another provenance key stay as history; they never match
		// the current key, so the same source is imported again as a new account.
		var active bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cli_credential_import_operations WHERE project_id=$1 AND status IN('pending','running'))`, in.ProjectID).Scan(&active); e != nil {
			return e
		}
		if active {
			return importConflict("import_in_progress")
		}
		if _, e = tx.Exec(ctx, `INSERT INTO cli_credential_import_operations(id,project_id,scan_id,instance_id,request_fingerprint,expected_credentials_revision,provenance_key_id,deadline_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, in.ID, in.ProjectID, in.ScanID, in.InstanceID, in.RequestFingerprint, in.ExpectedRevision, in.ProvenanceKeyID, in.Deadline); e != nil {
			if isUniqueViolation(e, "cli_credential_import_operations_pkey") {
				return apperr.NotFound("导入操作")
			}
			if isUniqueViolation(e, "") {
				return importConflict("import_operation_conflict")
			}
			return e
		}
		for n, v := range in.Items {
			if _, e = tx.Exec(ctx, `INSERT INTO cli_credential_import_items(operation_id,candidate_id,ordinal,cli_name,label)VALUES($1,$2,$3,$4,$5)`, in.ID, v.CandidateID, n, v.CLI, v.Label); e != nil {
				return e
			}
		}
		out, e = importOperationTx(ctx, tx, in.ProjectID, in.ID, false)
		created = e == nil
		return e
	})
	return out, created && err == nil, err
}

func (s *Store) CLIImportOriginSeen(ctx context.Context, project uuid.UUID, key string, origin []byte) (bool, error) {
	var seen bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cli_credential_import_receipts WHERE project_id=$1 AND provenance_key_id=$2 AND origin_mac=$3)`, project, key, origin).Scan(&seen)
	return seen, err
}
func (s *Store) SetCLIImportPhase(ctx context.Context, project, id, instance uuid.UUID, phase string) error {
	if phase != "extracting" && phase != "validating" && phase != "committing" {
		return apperr.Invalid("phase", "阶段无效")
	}
	return s.cliImportTx(ctx, func(tx pgx.Tx) error {
		_, deleted, e := importProjectLock(ctx, tx, project)
		if e != nil {
			return e
		}
		if deleted {
			return apperr.NotFound("项目")
		}
		tag, e := tx.Exec(ctx, `UPDATE cli_credential_import_operations SET status='running',phase=$4 WHERE project_id=$1 AND id=$2 AND instance_id=$3 AND status IN('pending','running') AND deadline_at>clock_timestamp()`, project, id, instance, phase)
		if e == nil && tag.RowsAffected() != 1 {
			return importConflict("import_operation_conflict")
		}
		return e
	})
}

func finishImportTx(ctx context.Context, tx pgx.Tx, o CLIImportOperation, status, code string, failed uuid.UUID) (CLIImportOperation, error) {
	if o.Terminal() {
		return o, nil
	}
	code = importCode(code)
	if status != "failed" && status != "cancelled" && status != "expired" {
		return o, apperr.Invalid("status", "终态无效")
	}
	if _, err := tx.Exec(ctx, `UPDATE cli_credential_import_items SET status=CASE WHEN candidate_id=$2 THEN 'failed' ELSE 'not_committed' END,error_code=CASE WHEN candidate_id=$2 THEN $3 ELSE NULL END WHERE operation_id=$1`, o.ID, failed, code); err != nil {
		return o, err
	}
	if _, err := tx.Exec(ctx, `UPDATE cli_credential_import_operations SET status=$2,phase=NULL,completed_at=clock_timestamp(),error_code=$3 WHERE id=$1 AND status IN('pending','running')`, o.ID, status, code); err != nil {
		return o, err
	}
	return importOperationTx(ctx, tx, o.ProjectID, o.ID, false)
}
func (s *Store) FinishCLIImport(ctx context.Context, project, id uuid.UUID, status, code string, failed uuid.UUID) (CLIImportOperation, error) {
	var out CLIImportOperation
	err := s.cliImportTx(ctx, func(tx pgx.Tx) error {
		_, deleted, e := importProjectLock(ctx, tx, project)
		if e != nil {
			return e
		}
		o, e := importOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		if deleted {
			status, code = "failed", "project_deleted"
		}
		out, e = finishImportTx(ctx, tx, o, status, code, failed)
		return e
	})
	return out, err
}
func (s *Store) CancelCLIImport(ctx context.Context, project, id uuid.UUID) (CLIImportOperation, error) {
	return s.FinishCLIImport(ctx, project, id, "cancelled", "cancelled", uuid.Nil)
}

// ExpireCLIImport is background reconciliation, never a GET side effect. The
// operation deadline is checked under the same locks as Finalize/Cancel.
func (s *Store) ExpireCLIImport(ctx context.Context, project, id uuid.UUID) (CLIImportOperation, error) {
	var out CLIImportOperation
	err := s.cliImportTx(ctx, func(tx pgx.Tx) error {
		_, deleted, e := importProjectLock(ctx, tx, project)
		if e != nil {
			return e
		}
		o, e := importOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		out = o
		if o.Terminal() {
			return nil
		}
		if deleted {
			out, e = finishImportTx(ctx, tx, o, "failed", "project_deleted", uuid.Nil)
			return e
		}
		var due bool
		if e = tx.QueryRow(ctx, `SELECT deadline_at<=clock_timestamp() FROM cli_credential_import_operations WHERE id=$1`, id).Scan(&due); e != nil {
			return e
		}
		if !due {
			return nil
		}
		out, e = finishImportTx(ctx, tx, o, "expired", "timeout", uuid.Nil)
		return e
	})
	return out, err
}

// FinalizeCLIImport owns the sole account/receipt/operation transaction. The
// recheck callback may only bounded-stat/digest frozen sources, never extract.
func (s *Store) FinalizeCLIImport(ctx context.Context, project, id, instance uuid.UUID, items []CLIImportPreparedItem, recheck func(context.Context) error) (CLIImportOperation, bool, error) {
	var out CLIImportOperation
	changed := false
	err := s.cliImportTx(ctx, func(tx pgx.Tx) error {
		rev, deleted, e := importProjectLock(ctx, tx, project)
		if e != nil {
			return e
		}
		o, e := importOperationTx(ctx, tx, project, id, true)
		if e != nil {
			return e
		}
		out = o
		if o.Terminal() {
			return nil
		}
		if deleted {
			out, e = finishImportTx(ctx, tx, o, "failed", "project_deleted", uuid.Nil)
			return e
		}
		var expected int64
		var owner uuid.UUID
		var key string
		var expired bool
		if e = tx.QueryRow(ctx, `SELECT expected_credentials_revision,instance_id,provenance_key_id,deadline_at<=clock_timestamp() FROM cli_credential_import_operations WHERE id=$1`, id).Scan(&expected, &owner, &key, &expired); e != nil {
			return e
		}
		if owner != instance {
			return importConflict("import_operation_conflict")
		}
		if expired {
			out, e = finishImportTx(ctx, tx, o, "expired", "timeout", uuid.Nil)
			return e
		}
		if e = credentialCAS("project", project, expected, rev); e != nil {
			return e
		}
		if len(items) != len(o.Items) || len(items) < 1 {
			return apperr.Invalid("candidateIds", "批次不完整")
		}
		selected := map[uuid.UUID]CLIImportItem{}
		for _, v := range o.Items {
			selected[v.CandidateID] = v
		}
		sorted := append([]CLIImportPreparedItem(nil), items...)
		sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i].OriginMAC, sorted[j].OriginMAC) < 0 })
		origins := map[string]bool{}
		ids := map[uuid.UUID]bool{}
		type result struct {
			candidate, account, version uuid.UUID
			status                      string
		}
		results := []result{}
		if recheck == nil {
			return errors.New("missing source recheck")
		}
		if e = recheck(ctx); e != nil {
			return e
		}
		for _, v := range sorted {
			original, ok := selected[v.CandidateID]
			if !ok || v.CLI != original.CLI || ids[v.CandidateID] || v.AccountID == uuid.Nil || v.VersionID == uuid.Nil || len(v.OriginMAC) != 32 || len(v.ContentMAC) != 32 || v.ProvenanceKeyID != key || v.TemplateSource != "builtin" || v.CredentialFormat == "" || !utf8.ValidString(v.Name) || utf8.RuneCountInString(v.Name) > 200 || strings.TrimSpace(v.Name) != v.Name || v.Name == "" || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 {
				return apperr.Invalid("candidateIds", "批次载荷无效")
			}
			if origins[string(v.OriginMAC)] {
				return importItemFailure(v.CandidateID, "source_conflict", importConflict("import_duplicate_source"))
			}
			origins[string(v.OriginMAC)] = true
			ids[v.CandidateID] = true
			if (v.CLI == "gcloud" && v.AuthMethod != "service_account") || (v.CLI != "gcloud" && v.AuthMethod != "static") {
				return apperr.Invalid("authMethod", "导入不接受动态认证")
			}
			var oldContent []byte
			var account, version uuid.UUID
			var cli, format string
			e = tx.QueryRow(ctx, `SELECT content_mac,account_id,version_id,cli_name,credential_format FROM cli_credential_import_receipts WHERE project_id=$1 AND provenance_key_id=$2 AND origin_mac=$3 FOR UPDATE`, project, key, v.OriginMAC).Scan(&oldContent, &account, &version, &cli, &format)
			if e == nil {
				if !bytes.Equal(oldContent, v.ContentMAC) || cli != v.CLI || format != v.CredentialFormat {
					return importItemFailure(v.CandidateID, "source_conflict", importConflict("import_source_conflict"))
				}
				a, err := projectAccountTx(ctx, tx, project, account, true)
				if err != nil {
					return importItemFailure(v.CandidateID, "source_conflict", importConflict("import_source_conflict"))
				}
				if a.View.DeletedAt != nil || a.View.Purpose != "cli" || a.View.CLI == nil || *a.View.CLI != v.CLI || a.View.CurrentVersionID == nil || *a.View.CurrentVersionID != version {
					return importItemFailure(v.CandidateID, "source_conflict", importConflict("import_source_conflict"))
				}
				results = append(results, result{v.CandidateID, account, version, "already_imported"})
				continue
			}
			if e != pgx.ErrNoRows {
				return e
			}
			if len(v.Secret) == 0 {
				return apperr.Invalid("credential", "缺少凭证内容")
			}
			if _, e = tx.Exec(ctx, `INSERT INTO project_credential_accounts(id,project_id,purpose,cli_name,name)VALUES($1,$2,'cli',$3,$4)`, v.AccountID, project, v.CLI, v.Name); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO project_cli_versions(id,account_id,cli_name,template_source,template_version,auth_method,secret,field_names,env_names,file_paths)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, v.VersionID, v.AccountID, v.CLI, v.TemplateSource, v.CredentialFormat, v.AuthMethod, v.Secret, nonNilStrings(v.Meta.FieldNames), nonNilStrings(v.Meta.EnvNames), nonNilStrings(v.Meta.FilePaths)); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, v.AccountID, v.VersionID); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `INSERT INTO cli_credential_import_receipts(project_id,provenance_key_id,origin_mac,content_mac,cli_name,credential_format,account_id,version_id,live_account_id,live_version_id)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$7,$8)`, project, key, v.OriginMAC, v.ContentMAC, v.CLI, v.CredentialFormat, v.AccountID, v.VersionID); e != nil {
				return e
			}
			changed = true
			results = append(results, result{v.CandidateID, v.AccountID, v.VersionID, "imported"})
		}
		if e = recheck(ctx); e != nil {
			return e
		}
		// Recheck deadline after lock waits and all writes; rollback any late batch.
		if e = tx.QueryRow(ctx, `SELECT deadline_at<=clock_timestamp() FROM cli_credential_import_operations WHERE id=$1`, id).Scan(&expired); e != nil {
			return e
		}
		if expired {
			return context.DeadlineExceeded
		}
		for _, r := range results {
			if _, e = tx.Exec(ctx, `UPDATE cli_credential_import_items SET status=$3,account_id=$4,version_id=$5,error_code=NULL WHERE operation_id=$1 AND candidate_id=$2`, id, r.candidate, r.status, r.account, r.version); e != nil {
				return e
			}
		}
		if changed {
			if e = bumpCredentialProject(ctx, tx, project); e != nil {
				return e
			}
			rev++
		}
		if _, e = tx.Exec(ctx, `UPDATE cli_credential_import_operations SET status='succeeded',phase=NULL,completed_at=clock_timestamp(),committed_credentials_revision=$2 WHERE id=$1`, id, rev); e != nil {
			return e
		}
		out, e = importOperationTx(ctx, tx, project, id, false)
		return e
	})
	return out, changed && err == nil, err
}

func (s *Store) RecoverCLIImports(ctx context.Context, instance uuid.UUID) error {
	if instance == uuid.Nil {
		return apperr.Invalid("instanceId", "UUID不能为零")
	}
	rows, err := s.Pool.Query(ctx, `SELECT project_id,id FROM cli_credential_import_operations WHERE instance_id<>$1 AND status IN('pending','running') ORDER BY project_id,id`, instance)
	if err != nil {
		return err
	}
	type pair struct{ p, id uuid.UUID }
	pairs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) { var p pair; e := r.Scan(&p.p, &p.id); return p, e })
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if _, err = s.FinishCLIImport(ctx, p.p, p.id, "failed", "worker_restarted", uuid.Nil); err != nil {
			return err
		}
	}
	return nil
}

// ExpireDueCLIImports retries durable expiry after a temporary database outage.
// It only touches this service instance's elapsed operations and never sources.
func (s *Store) ExpireDueCLIImports(ctx context.Context, instance uuid.UUID) error {
	if instance == uuid.Nil {
		return apperr.Invalid("instanceId", "UUID不能为零")
	}
	rows, err := s.Pool.Query(ctx, `SELECT project_id,id FROM cli_credential_import_operations WHERE instance_id=$1 AND status IN('pending','running') AND deadline_at<=clock_timestamp() ORDER BY project_id,id`, instance)
	if err != nil {
		return err
	}
	type pair struct{ project, id uuid.UUID }
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) { var p pair; e := r.Scan(&p.project, &p.id); return p, e })
	if err != nil {
		return err
	}
	for _, p := range items {
		if _, err = s.ExpireCLIImport(ctx, p.project, p.id); err != nil {
			return err
		}
	}
	return nil
}

// TerminateProjectCLIImportsTx is configured by trusted App wiring after migration17.
// The caller already holds the project row lock. It neither opens a transaction
// nor swallows undefined-table errors; target16 fixtures simply must not wire it.
func (s *Store) TerminateProjectCLIImportsTx(ctx context.Context, tx pgx.Tx, project uuid.UUID) error {
	if project == uuid.Nil {
		return apperr.Invalid("projectId", "UUID不能为零")
	}
	rows, err := tx.Query(ctx, `SELECT id FROM cli_credential_import_operations WHERE project_id=$1 AND status IN('pending','running') ORDER BY id FOR UPDATE`, project)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE cli_credential_import_items SET status='not_committed',account_id=NULL,version_id=NULL,error_code=NULL WHERE operation_id=ANY($1)`, ids); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE cli_credential_import_operations SET status='cancelled',phase=NULL,completed_at=clock_timestamp(),error_code='project_deleted' WHERE id=ANY($1) AND status IN('pending','running')`, ids)
	return err
}
