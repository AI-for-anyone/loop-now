package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// Self-owned PG18 only. Set the explicit pgtest URL before its first lookup so
// testenv never falls back to .env.local or an existing database. No test DDL.
func constraintsOwnPG(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/opt/homebrew/opt/postgresql@18/bin/initdb"); err != nil {
		t.Skip("required fixed offline dependency unavailable: /opt/homebrew/opt/postgresql@18/bin/initdb")
	}
	root, err := os.MkdirTemp("/private/tmp", "ln-constraints-")
	if err != nil {
		t.Fatal(err)
	}
	data, socket := filepath.Join(root, "data"), filepath.Join(root, "socket")
	if err = os.Mkdir(socket, 0700); err != nil {
		t.Fatal(err)
	}
	command := func(name string, args ...string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/opt/homebrew/opt/postgresql@18/bin/"+name, args...)
		cmd.Env = []string{"HOME=" + root, "PATH=/usr/bin:/bin", "LC_ALL=C"}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run()
	}
	if err = command("initdb", "-D", data, "-U", "fixture", "-A", "trust", "--no-locale", "-E", "UTF8"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG init failed", err)
	}
	if err = command("pg_ctl", "-D", data, "-l", filepath.Join(root, "pg.log"), "-o", "-F -h '' -k "+socket+" -p 55433", "-w", "-t", "10", "start"); err != nil {
		os.RemoveAll(root)
		t.Fatal("own PG start failed", err)
	}
	t.Cleanup(func() {
		if err := command("pg_ctl", "-D", data, "-m", "immediate", "-w", "-t", "10", "stop"); err != nil {
			t.Errorf("own PG stop failed: %v", err)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error("own PG cleanup failed")
		}
	})
	if err = command("createdb", "-h", socket, "-p", "55433", "-U", "fixture", "loop_now_test"); err != nil {
		t.Fatal("own test database creation failed", err)
	}
	t.Setenv(pgtest.EnvTestDatabaseURL, "postgres://fixture@/loop_now_test?host="+url.QueryEscape(socket)+"&port=55433&sslmode=disable")
}

type credentialConstraintCase struct {
	name, table, code, constraint, column, sql string
	args                                       []any
	deferred                                   bool
}

func TestProjectCredentialNativeConstraints(t *testing.T) {
	constraintsOwnPG(t)
	ctx := context.Background()
	st := pgtest.NewStore(t) // Executes the current real chain, including registered 012, on an empty schema.
	var applied bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM goose_db_version WHERE version_id=12 AND is_applied)`).Scan(&applied); err != nil || !applied {
		t.Fatal("fixture did not apply formal migration 012")
	}
	// Later append-only migrations do not erase 012 acceptance. All original
	// native constraint negative/positive cases below remain unchanged.
	var version int
	if err := st.Pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version < 12 {
		t.Fatal("fixture migration ledger invalid")
	}
	t.Logf("actual current migration chain through %d includes formal012", version)
	var pgVersion int
	if err := st.Pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&pgVersion); err != nil || pgVersion < 180000 || pgVersion >= 190000 {
		t.Fatal("fixture must use PostgreSQL18")
	}
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := st.EnsureDefaults(ctx, "constraint-org", "constraint-project")
	if err != nil {
		t.Fatal(err)
	}
	p1 := defaults.Project.ID
	other, err := st.CreateProject(ctx, defaults.Organization.ID, "constraint-other")
	if err != nil {
		t.Fatal(err)
	}
	p2 := other.ID
	agent, err := st.CreateAgent(ctx, store.ProjectScope(p1), "constraint-agent", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	foreignAgent, err := st.CreateAgent(ctx, store.ProjectScope(p2), "constraint-foreign", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	platform, err := st.CreateAgent(ctx, store.PlatformScope(), "constraint-platform", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	apiKey, err := modelcredential.APIKey("synthetic-constraint-key")
	if err != nil {
		t.Fatal(err)
	}
	modelPlain, err := apiKey.Encode()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(modelPlain)
	modelWrite := store.ProjectCredentialWrite{AuthMethod: "api_key", Secret: modelPlain}
	createModel := func(project uuid.UUID, provider, name string) store.ProjectCredentialRecord {
		t.Helper()
		r, err := st.CreateProjectCredential(ctx, project, store.PurposeModel, provider, name, modelWrite)
		if err != nil || r.Model == nil {
			t.Fatal("valid project model account rejected")
		}
		return r
	}
	model := createModel(p1, "openai", "model")
	modelOtherOwner := createModel(p1, "openai", "same-project-other-owner")
	modelOtherProvider := createModel(p1, "anthropic", "other-provider")
	modelForeign := createModel(p2, "openai", "foreign-model")
	gh, _ := templates.Get("gh")
	if gh == nil {
		t.Fatal("fixed gh template unavailable")
	}
	cliPayload, cliFields, err := gh.Render(map[string]string{"token": "synthetic-constraint-gh"})
	if err != nil {
		t.Fatal("valid CLI template fixture rejected")
	}
	cliPlain, err := json.Marshal(cliPayload)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(cliPlain)
	cliWrite := store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: gh.CredentialFormat(), FieldNames: cliFields, EnvNames: cliPayload.EnvNames(), FilePaths: cliPayload.FilePaths()}, Secret: cliPlain}
	createCLI := func(project uuid.UUID, name string) store.ProjectCredentialRecord {
		t.Helper()
		r, err := st.CreateProjectCredential(ctx, project, store.PurposeCLI, "gh", name, cliWrite)
		if err != nil || r.CLI == nil {
			t.Fatal("valid project CLI account rejected")
		}
		return r
	}
	cli := createCLI(p1, "cli")
	cliForeign := createCLI(p2, "foreign-cli")
	private, err := st.PutModelAuthKey(ctx, platform.ID, "openai", nil, modelWrite.Secret)
	if err != nil {
		t.Fatal("legal platform private version rejected")
	}
	// Prove fixtures hold real credentials rather than placeholders. Negative
	// cases update existing IDs/relations only and never replace credential columns.
	for _, v := range []*store.ModelAuthVersion{model.Model, modelOtherOwner.Model, modelOtherProvider.Model, modelForeign.Model, &private} {
		decoded, err := modelcredential.Decode(v.Secret)
		if err != nil || decoded.Kind() != "api_key" {
			t.Fatal("model fixture payload is not canonical")
		}
	}
	for _, v := range []*store.CredentialRow{cli.CLI, cliForeign.CLI} {
		if !bytes.Equal(v.Secret, cliPlain) {
			t.Fatal("CLI credential control failed")
		}
	}
	// Auto-generated names can be truncated by PostgreSQL. Identify the exact
	// formal constraint by table/type/definition, then require that native name.
	native := func(table, kind, definition string) string {
		t.Helper()
		var names []string
		err := st.Pool.QueryRow(ctx, `SELECT array_agg(conname::text) FROM pg_constraint WHERE conrelid=$1::regclass AND contype=$2::"char" AND pg_get_constraintdef(oid) LIKE $3`, table, kind, definition+"%").Scan(&names)
		if err != nil || len(names) != 1 {
			t.Fatalf("formal native constraint missing/ambiguous: %s %s", table, definition)
		}
		return names[0]
	}
	accountCheck := native("project_credential_accounts", "c", "CHECK ((((purpose = 'model'::text)")
	selectionCheck := native("agent_credential_selections", "c", "CHECK ((((mode = 'explicit'::text)")
	selectionOwnerFK := native("agent_credential_selections", "f", "FOREIGN KEY (agent_id, project_id)")
	selectionAccountFK := native("agent_credential_selections", "f", "FOREIGN KEY (account_id, project_id, purpose, slot_key)")
	cliOwnerFK := native("project_cli_versions", "f", "FOREIGN KEY (account_id, purpose, cli_name)")
	cases := []credentialConstraintCase{
		{name: "owner_both", table: "model_auth_versions", code: "23514", constraint: "model_auth_owner", sql: `UPDATE model_auth_versions SET agent_id=$2 WHERE id=$1`, args: []any{model.Model.ID, platform.ID}},
		{name: "owner_neither", table: "model_auth_versions", code: "23514", constraint: "model_auth_owner", sql: `UPDATE model_auth_versions SET project_account_id=NULL WHERE id=$1`, args: []any{model.Model.ID}},
		{name: "owner_missing_account", table: "model_auth_versions", code: "23503", constraint: "model_auth_project_owner", sql: `UPDATE model_auth_versions SET project_account_id=$2 WHERE id=$1`, args: []any{model.Model.ID, uuid.New()}},
		{name: "owner_zero_account", table: "model_auth_versions", code: "23503", constraint: "model_auth_project_owner", sql: `UPDATE model_auth_versions SET project_account_id=$2 WHERE id=$1`, args: []any{model.Model.ID, uuid.Nil}},
		{name: "model_version_cli_owner", table: "model_auth_versions", code: "23503", constraint: "model_auth_project_owner", sql: `UPDATE model_auth_versions SET project_account_id=$2 WHERE id=$1`, args: []any{model.Model.ID, cli.View.ID}},
		{name: "model_version_wrong_provider", table: "model_auth_versions", code: "23503", constraint: "model_auth_project_owner", sql: `UPDATE model_auth_versions SET provider='anthropic' WHERE id=$1`, args: []any{model.Model.ID}},
		{name: "model_version_wrong_purpose", table: "model_auth_versions", code: "23514", constraint: "model_auth_versions_project_purpose_check", sql: `UPDATE model_auth_versions SET project_purpose='cli' WHERE id=$1`, args: []any{model.Model.ID}},
		{name: "account_invalid_purpose", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET purpose='other' WHERE id=$1`, args: []any{model.View.ID}},
		{name: "model_account_null_provider", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET provider=NULL WHERE id=$1`, args: []any{model.View.ID}},
		{name: "model_account_cli_field", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET cli_name='gh' WHERE id=$1`, args: []any{model.View.ID}},
		{name: "cli_account_null_cli", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET cli_name=NULL WHERE id=$1`, args: []any{cli.View.ID}},
		{name: "cli_account_provider_field", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET provider='openai' WHERE id=$1`, args: []any{cli.View.ID}},
		{name: "model_account_cli_current", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, cli.CLI.ID}},
		{name: "cli_account_model_current", table: "project_credential_accounts", code: "23514", constraint: accountCheck, sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{cli.View.ID, model.Model.ID}},
		{name: "cli_version_model_owner", table: "project_cli_versions", code: "23503", constraint: cliOwnerFK, sql: `UPDATE project_cli_versions SET account_id=$2 WHERE id=$1`, args: []any{cli.CLI.ID, model.View.ID}},
		{name: "cli_version_wrong_cli", table: "project_cli_versions", code: "23503", constraint: cliOwnerFK, sql: `UPDATE project_cli_versions SET cli_name='other-cli' WHERE id=$1`, args: []any{cli.CLI.ID}},
		{name: "cli_version_wrong_purpose", table: "project_cli_versions", code: "23514", constraint: "project_cli_versions_purpose_check", sql: `UPDATE project_cli_versions SET purpose='model' WHERE id=$1`, args: []any{cli.CLI.ID}},
		{name: "current_model_cross_project", table: "project_credential_accounts", code: "23503", constraint: "account_current_model_owner", sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, modelForeign.Model.ID}, deferred: true},
		{name: "current_model_same_project_wrong_owner", table: "project_credential_accounts", code: "23503", constraint: "account_current_model_owner", sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, modelOtherOwner.Model.ID}, deferred: true},
		{name: "current_model_wrong_provider_owner", table: "project_credential_accounts", code: "23503", constraint: "account_current_model_owner", sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, modelOtherProvider.Model.ID}, deferred: true},
		{name: "current_model_private_owner", table: "project_credential_accounts", code: "23503", constraint: "account_current_model_owner", sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, private.ID}, deferred: true},
		{name: "current_model_missing", table: "project_credential_accounts", code: "23503", constraint: "account_current_model_owner", sql: `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, args: []any{model.View.ID, uuid.New()}, deferred: true},
		{name: "current_cli_cross_project", table: "project_credential_accounts", code: "23503", constraint: "account_current_cli_owner", sql: `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, args: []any{cli.View.ID, cliForeign.CLI.ID}, deferred: true},
		{name: "current_cli_missing", table: "project_credential_accounts", code: "23503", constraint: "account_current_cli_owner", sql: `UPDATE project_credential_accounts SET current_cli_version_id=$2 WHERE id=$1`, args: []any{cli.View.ID, uuid.New()}, deferred: true},
	}
	selectSQL := `INSERT INTO agent_credential_selections(agent_id,project_id,purpose,slot_key,account_id,mode)VALUES($1,$2,$3,$4,$5,$6)`
	addSelection := func(name, code, constraint, column string, agentID, projectID uuid.UUID, purpose, slot any, account any, mode any) {
		cases = append(cases, credentialConstraintCase{name: name, table: "agent_credential_selections", code: code, constraint: constraint, column: column, sql: selectSQL, args: []any{agentID, projectID, purpose, slot, account, mode}})
	}
	addSelection("selection_cross_project_account", "23503", selectionAccountFK, "", agent.ID, p1, "model", "openai", modelForeign.View.ID, "explicit")
	addSelection("selection_wrong_agent_project", "23503", selectionOwnerFK, "", foreignAgent.ID, p1, "model", "openai", model.View.ID, "explicit")
	addSelection("selection_platform_claims_project", "23503", selectionOwnerFK, "", platform.ID, p1, "model", "openai", model.View.ID, "explicit")
	addSelection("selection_wrong_provider", "23503", selectionAccountFK, "", agent.ID, p1, "model", "anthropic", model.View.ID, "explicit")
	addSelection("selection_wrong_cli", "23503", selectionAccountFK, "", agent.ID, p1, "cli", "other-cli", cli.View.ID, "explicit")
	addSelection("selection_wrong_purpose", "23503", selectionAccountFK, "", agent.ID, p1, "model", "gh", cli.View.ID, "explicit")
	addSelection("selection_zero_account", "23503", selectionAccountFK, "", agent.ID, p1, "model", "openai", uuid.Nil, "explicit")
	addSelection("selection_explicit_null", "23514", selectionCheck, "", agent.ID, p1, "model", "openai", nil, "explicit")
	addSelection("selection_override_nonnull", "23514", selectionCheck, "", agent.ID, p1, "model", "openai", model.View.ID, "automatic_override")
	addSelection("selection_cli_override", "23514", selectionCheck, "", agent.ID, p1, "cli", "gh", nil, "automatic_override")
	addSelection("selection_unknown_mode", "23514", selectionCheck, "", agent.ID, p1, "model", "openai", model.View.ID, "other")
	addSelection("selection_null_mode", "23502", "", "mode", agent.ID, p1, "model", "openai", model.View.ID, nil)
	addSelection("selection_null_slot", "23502", "", "slot_key", agent.ID, p1, "model", nil, model.View.ID, "explicit")
	addSelection("selection_null_purpose", "23502", "", "purpose", agent.ID, p1, nil, "openai", model.View.ID, "explicit")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := st.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, c.sql, c.args...)
			if c.deferred && err != nil {
				t.Fatal("expected deferred FK must allow statement before commit")
			}
			if err == nil {
				err = tx.Commit(ctx)
			}
			var native *pgconn.PgError
			if !errors.As(err, &native) || native.Code != c.code || native.TableName != c.table || native.ConstraintName != c.constraint || (c.column != "" && native.ColumnName != c.column) {
				gotCode, gotConstraint := "none", "none"
				if native != nil {
					gotCode = native.Code
					gotConstraint = native.ConstraintName
				}
				t.Fatalf("native DB rejection mismatch: want %s/%s got %s/%s (raw error withheld)", c.code, c.constraint, gotCode, gotConstraint)
			}
			t.Logf("native %s table=%s constraint=%s column=%s deferred=%t", native.Code, native.TableName, native.ConstraintName, native.ColumnName, c.deferred)
		})
	}
	t.Run("valid_selections_and_duplicate_native_rejection", func(t *testing.T) {
		for _, args := range [][]any{{agent.ID, p1, "model", "openai", model.View.ID, "explicit"}, {agent.ID, p1, "cli", "gh", cli.View.ID, "explicit"}, {agent.ID, p1, "model", "anthropic", nil, "automatic_override"}} {
			if _, err = st.Pool.Exec(ctx, selectSQL, args...); err != nil {
				t.Fatal("legal explicit/automatic_override selection rejected")
			}
		}
		_, err = st.Pool.Exec(ctx, selectSQL, agent.ID, p1, "model", "openai", modelOtherOwner.View.ID, "explicit")
		var native *pgconn.PgError
		if !errors.As(err, &native) || native.Code != "23505" || native.ConstraintName != "agent_credential_selections_pkey" {
			t.Fatal("duplicate selection was not rejected by native primary key")
		}
		t.Log("native 23505 agent_credential_selections_pkey; distinct slots and model automatic_override NULL accepted")
	})
	t.Run("valid_platform_private_and_original_credential_unchanged", func(t *testing.T) {
		pointer, _, err := st.CurrentModelAuthVersion(ctx, platform.ID)
		if err != nil || pointer.ID != private.ID || pointer.AgentID != platform.ID || pointer.ProjectAccountID != nil {
			t.Fatal("platform private owner/pointer changed")
		}
		for _, account := range []store.ProjectCredentialRecord{model, modelOtherOwner, modelOtherProvider, modelForeign, cli, cliForeign} {
			actual, err := st.GetProjectCredential(ctx, account.View.ProjectID, account.View.ID)
			if err != nil || actual.View.CurrentVersionID == nil || *actual.View.CurrentVersionID != *account.View.CurrentVersionID {
				t.Fatal("rejected transaction modified valid current pointer")
			}
			if actual.Model != nil {
				if actual.Model.Generation != account.Model.Generation || actual.Model.RefreshState != account.Model.RefreshState {
					t.Fatal("constraint failures changed original generation/refresh state")
				}
				if !bytes.Equal(actual.Model.Secret, modelPlain) {
					t.Fatal("constraint failures modified valid credential")
				}
			} else if !bytes.Equal(actual.CLI.Secret, cliPlain) {
				t.Fatal("constraint failures modified valid credential")
			}
		}
		var count int
		st.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions WHERE id=$1 AND agent_id=$2 AND project_account_id IS NULL`, private.ID, platform.ID).Scan(&count)
		if count != 1 {
			t.Fatal("legal platform row missing")
		}
		t.Log("project model/CLI, private platform, original credentials/current identities preserved")
	})
}
