package store_test

import (
	"context"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

// rawProjectCredential writes a current project account in the column shape of
// schemas 014–025 (conversion marker form), without current Store code.
func rawProjectCredential(t *testing.T, e projectMigrationEnv, purpose, slot, name, method string, plain []byte) (uuid.UUID, uuid.UUID) {
	t.Helper()
	account, version := uuid.New(), uuid.New()
	if purpose == "model" {
		mustExec(t, e.s, `INSERT INTO project_credential_accounts(id,project_id,purpose,provider,name)VALUES($1,$2,'model',$3,$4)`, account, e.project, slot, name)
		insertCredentialRow(t, e.s, "model_auth_versions", []string{"id", "project_account_id", "provider", "auth_method"}, []any{version, account, slot, method}, plain)
		mustExec(t, e.s, `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, account, version)
		return account, version
	}
	mustExec(t, e.s, `INSERT INTO project_credential_accounts(id,project_id,purpose,cli_name,name)VALUES($1,$2,'cli',$3,$4)`, account, e.project, slot, name)
	insertCredentialRow(t, e.s, "project_cli_versions", []string{"id", "account_id", "cli_name", "template_source", "template_version", "auth_method", "field_names", "env_names", "file_paths"}, []any{version, account, slot, "builtin", "1", method, []string{}, []string{}, []string{}}, plain)
	mustExec(t, e.s, `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, account, version)
	return account, version
}

func TestLark020MigrationOldCredentialLedgerAndDownProtection(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.MigrateUpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	larkAccount, larkVersion := rawProjectCredential(t, e, "cli", "lark-cli", "原静态应用", "static", []byte(`{"env":{},"files":[]}`))
	for _, old := range []struct{ purpose, slot, method string }{{"cli", "gh", "static"}, {"cli", "gcloud", "service_account"}, {"model", "anthropic", "api_key"}} {
		rawProjectCredential(t, e, old.purpose, old.slot, "legacy-"+old.slot, old.method, []byte(`{"synthetic":"legacy-original"}`))
	}
	var err error
	const allCipherSQL = `SELECT string_agg(id::text||':'||encode(sha256(ciphertext),'hex'),',' ORDER BY id) FROM (SELECT id,ciphertext FROM project_cli_versions UNION ALL SELECT id,ciphertext FROM model_auth_versions) v`
	var allBefore string
	if err = e.s.Pool.QueryRow(ctx, allCipherSQL).Scan(&allBefore); err != nil {
		t.Fatal(err)
	}
	var cipherBefore, ledgerBefore string
	if err = e.s.Pool.QueryRow(ctx, `SELECT encode(sha256(ciphertext),'hex') FROM project_cli_versions WHERE id=$1`, larkVersion).Scan(&cipherBefore); err != nil {
		t.Fatal(err)
	}
	ledgerSQL := `SELECT md5(string_agg(row_to_json(v)::text,',' ORDER BY id)) FROM goose_db_version v WHERE version_id BETWEEN 0 AND 19`
	if err = e.s.Pool.QueryRow(ctx, ledgerSQL).Scan(&ledgerBefore); err != nil {
		t.Fatal(err)
	}
	if err = e.s.MigrateUpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var cipherAfter, ledgerAfter string
	if err = e.s.Pool.QueryRow(ctx, `SELECT encode(sha256(ciphertext),'hex') FROM project_cli_versions WHERE id=$1`, larkVersion).Scan(&cipherAfter); err != nil {
		t.Fatal(err)
	}
	if err = e.s.Pool.QueryRow(ctx, ledgerSQL).Scan(&ledgerAfter); err != nil {
		t.Fatal(err)
	}
	if cipherBefore != cipherAfter || ledgerBefore != ledgerAfter {
		t.Fatal("020 changed old credential or ledger rows")
	}
	var allAfter string
	if err = e.s.Pool.QueryRow(ctx, allCipherSQL).Scan(&allAfter); err != nil || allAfter != allBefore {
		t.Fatal("020 changed legacy bot, GitHub, Google or model credentials")
	}
	if err = e.s.MigrateDownTo(ctx, 19); err != nil {
		t.Fatal("empty 020 Down failed", err)
	}
	if err = e.s.MigrateUpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	// New Lark login audit data, written in the 020 shape.
	mustExec(t, e.s, `INSERT INTO project_cli_auth_operations(id,project_id,cli_name,auth_type,request_hash,source_account_id,source_version_id,source_revision,expires_at)VALUES($1,$2,'lark-cli','lark_user',$3,$4,$5,1,$6)`, uuid.New(), e.project, strings.Repeat("a", 64), larkAccount, larkVersion, time.Now().Add(time.Minute))
	if err = e.s.MigrateDownTo(ctx, 19); err == nil {
		t.Fatal("020 destructive Down accepted new audit data")
	}
	var current int
	if err = e.s.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&current); err != nil || current != 20 {
		t.Fatal("refused Down changed current version")
	}
}

// Force a failure after 020 has altered the version CHECK and created its
// identity table. Goose must roll the complete migration and ledger back.
func TestLark020PartialDDLFailureRollsBackConstraintsAndLedger(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.MigrateUpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	var before string
	const ledger = `SELECT md5(string_agg(row_to_json(v)::text,',' ORDER BY id)) FROM goose_db_version v`
	if err := e.s.Pool.QueryRow(ctx, ledger).Scan(&before); err != nil {
		t.Fatal(err)
	}
	var original string
	const constraintQuery = `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='project_cli_versions'::regclass AND conname='cli_auth_kind'`
	if err := e.s.Pool.QueryRow(ctx, constraintQuery).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Pool.Exec(ctx, `CREATE TABLE lark_cli_grants (sentinel integer)`); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateUpTo(ctx, 20); err == nil {
		t.Fatal("forced late DDL collision was ignored")
	}
	var after, check string
	var identities *string
	if err := e.s.Pool.QueryRow(ctx, ledger).Scan(&after); err != nil || after != before {
		t.Fatal("failed DDL changed migration ledger")
	}
	if err := e.s.Pool.QueryRow(ctx, `SELECT to_regclass(current_schema()||'.lark_cli_identities')::text`).Scan(&identities); err != nil || identities != nil {
		t.Fatal("failed migration retained partial table")
	}
	if err := e.s.Pool.QueryRow(ctx, constraintQuery).Scan(&check); err != nil || original != check {
		t.Fatal("failed migration changed original constraint")
	}
	// A legacy static Lark version must still be accepted, and the exact
	// original CHECK must remain in effect rather than a half-applied 020 CHECK.
	if _, err := e.s.Pool.Exec(ctx, `DROP TABLE lark_cli_grants`); err != nil {
		t.Fatal(err)
	}
	rawProjectCredential(t, e, "cli", "lark-cli", "rollback static", "static", []byte(`{"env":{},"files":[]}`))
	if err := e.s.MigrateUpTo(ctx, 20); err != nil {
		t.Fatal("clean retry after rollback failed")
	}
}
