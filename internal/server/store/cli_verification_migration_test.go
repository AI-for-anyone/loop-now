package store_test

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"strings"
	"testing"
	"time"
)

func verificationSeed(t *testing.T, e projectMigrationEnv, project uuid.UUID, cli, method string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var raw []byte
	var label *string
	if method == "oauth" {
		c := clicredential.Google{Type: "authorized_user", Account: "fixture@example.invalid", ClientID: "fixture", ClientSecret: "synthetic", RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"fixture"}}
		var err error
		raw, err = c.Encode()
		if err != nil {
			t.Fatal(err)
		}
		label = &c.Account
	} else {
		raw = []byte(`{"env":{"GH_TOKEN":"synthetic"},"files":[]}`)
	}
	defer clear(raw)
	r, err := e.s.CreateProjectCredential(ctx, project, "cli", cli, "fixture", store.ProjectCredentialWrite{AuthMethod: method, AccountLabel: label, TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: raw})
	if err != nil {
		t.Fatal(err)
	}
	return r.View.ID, *r.View.CurrentVersionID
}

// rawGoogleOAuthSeed writes a gcloud OAuth account in the column shape of
// schemas 014–025 (conversion marker form), without current Store code.
func rawGoogleOAuthSeed(t *testing.T, e projectMigrationEnv) (uuid.UUID, uuid.UUID, []byte) {
	t.Helper()
	c := clicredential.Google{Type: "authorized_user", Account: "fixture@example.invalid", ClientID: "fixture", ClientSecret: "synthetic", RefreshToken: "synthetic-refresh", AccessToken: "synthetic-access", ExpiresAt: time.Now().Add(time.Hour).UnixMilli(), Scopes: []string{"fixture"}}
	raw, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	account, version := uuid.New(), uuid.New()
	mustExec(t, e.s, `INSERT INTO project_credential_accounts(id,project_id,purpose,cli_name,name)VALUES($1,$2,'cli','gcloud','fixture')`, account, e.project)
	insertCredentialRow(t, e.s, "project_cli_versions", []string{"id", "account_id", "cli_name", "template_source", "template_version", "auth_method", "account_label", "field_names", "env_names", "file_paths"}, []any{version, account, "gcloud", "builtin", "1", "oauth", c.Account, []string{}, []string{}, []string{}}, raw)
	mustExec(t, e.s, `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, account, version)
	mustExec(t, e.s, `INSERT INTO google_cli_grants(version_id,account_id)VALUES($1,$2)`, version, account)
	return account, version, raw
}
func verificationInsert(t *testing.T, e projectMigrationEnv, project, account, version, id uuid.UUID, cli, method string) error {
	t.Helper()
	_, err := e.s.Pool.Exec(context.Background(), `INSERT INTO cli_credential_verifications(id,project_id,account_id,version_id,live_version_id,cli_name,auth_method,request_hash,initiator_kind,template_source,credential_format,template_fingerprint,adapter_revision,probe_kind,expires_at)VALUES($1,$2,$3,$4,$4,$5,$6,$7,'console','builtin','1',$7,'fixture','github_user',clock_timestamp()+interval '1 minute')`, id, project, account, version, cli, method, strings.Repeat("a", 64))
	return err
}
func TestCLIVerificationMigrationEmptyAndExistingLease(t *testing.T) {
	ctx := context.Background()
	e := projectMigrationFixture(t)
	if err := e.s.MigrateUpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateUpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateDownTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	agent := e.agent(t, "lease-owner", "anthropic")
	_, version, _ := rawGoogleOAuthSeed(t, e)
	lease := uuid.New()
	// Seed the exact 015 lease shape using its original columns. Current
	// Store code legitimately requires schema016 and must not be called on015.
	tag, err := e.s.Pool.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_agent_id=$3,lease_until=$4 WHERE version_id=$1 AND generation=1 AND refresh_state='ready'`, version, lease, agent, time.Now().Add(time.Minute))
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("old 015 Agent lease fixture", err)
	}
	var hashBefore string
	if err = e.s.Pool.QueryRow(ctx, `SELECT encode(sha256(ciphertext),'hex') FROM project_cli_versions WHERE id=$1`, version).Scan(&hashBefore); err != nil {
		t.Fatal(err)
	}
	if err = e.s.MigrateUpTo(ctx, 16); err != nil {
		t.Fatal(err)
	}
	var generation int64
	var leaseID, leaseAgent *uuid.UUID
	if err = e.s.Pool.QueryRow(ctx, `SELECT generation,lease_id,lease_agent_id FROM google_cli_grants WHERE version_id=$1`, version).Scan(&generation, &leaseID, &leaseAgent); err != nil || generation != 1 || leaseID == nil || *leaseID != lease || leaseAgent == nil || *leaseAgent != agent {
		t.Fatal("016 changed existing lease", err)
	}
	var hashAfter string
	if err = e.s.Pool.QueryRow(ctx, `SELECT encode(sha256(ciphertext),'hex') FROM project_cli_versions WHERE id=$1`, version).Scan(&hashAfter); err != nil || hashAfter != hashBefore {
		t.Fatal("016 changed original credential", err)
	}
}
func TestCLIVerificationMigrationOwnerConstraints(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	account, version := verificationSeed(t, e, e.project, "gcloud", "oauth")
	op := uuid.New()
	if err := verificationInsert(t, e, e.project, account, version, op, "gcloud", "oauth"); err != nil {
		t.Fatal(err)
	}
	if err := verificationInsert(t, e, e.project, account, version, uuid.New(), "gcloud", "oauth"); err == nil {
		t.Fatal("duplicate active operation accepted")
	}
	var org uuid.UUID
	if err := e.s.Pool.QueryRow(ctx, `SELECT organization_id FROM projects WHERE id=$1`, e.project).Scan(&org); err != nil {
		t.Fatal(err)
	}
	other, err := e.s.CreateProject(ctx, org, "other-scope")
	if err != nil {
		t.Fatal(err)
	}
	for name, attempt := range map[string]func() error{
		"cross-project": func() error {
			return verificationInsert(t, e, other.ID, account, version, uuid.New(), "gcloud", "oauth")
		},
		"wrong-version": func() error {
			return verificationInsert(t, e, e.project, account, uuid.New(), uuid.New(), "gcloud", "oauth")
		},
		"wrong-cli": func() error { return verificationInsert(t, e, e.project, account, version, uuid.New(), "gh", "oauth") },
		"wrong-auth": func() error {
			return verificationInsert(t, e, e.project, account, version, uuid.New(), "gcloud", "static")
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := attempt(); err == nil {
				t.Fatal("invalid native reference accepted")
			}
		})
	}
	agent := e.agent(t, "owner-xor", "anthropic")
	lease := uuid.New()
	if _, err = e.s.Pool.Exec(ctx, `UPDATE google_cli_grants SET refresh_state='refreshing',lease_id=$2,lease_until=clock_timestamp()+interval '1 minute',lease_verification_id=$3 WHERE version_id=$1`, version, lease, op); err != nil {
		t.Fatal("valid management lease rejected", err)
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE google_cli_grants SET lease_agent_id=$2 WHERE version_id=$1`, version, agent); err == nil {
		t.Fatal("two lease owners accepted")
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE google_cli_grants SET lease_verification_id=NULL WHERE version_id=$1`, version); err == nil {
		t.Fatal("no lease owner accepted")
	}
	if _, err = e.s.Pool.Exec(ctx, `UPDATE google_cli_grants SET lease_verification_id=$2 WHERE version_id=$1`, version, uuid.New()); err == nil {
		t.Fatal("fake operation lease accepted")
	}
	// 027 has no data here and rolls back; the refusal under test is 026's.
	if err = e.s.MigrateDownTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	var before int
	if err = e.s.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = e.s.MigrateDownTo(ctx, 15); err == nil {
		t.Fatal("history was destructively downgraded")
	}
	var current int
	if err = e.s.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&current); err != nil || current != before {
		t.Fatal("failed Down changed version ledger", err)
	}
}
