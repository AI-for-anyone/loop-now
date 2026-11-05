package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/db"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"

	"github.com/google/uuid"
)

type credentialConstraintCase struct {
	name, table, code, constraint, column, sql string
	args                                       []any
	deferred                                   bool
}

func TestProjectCredentialNativeConstraints(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t) // 基线即全部结构（包括原来 PostgreSQL 的 012 凭证约束）。
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
	// 约束名沿用 PostgreSQL 的（包括自动生成的）；按表与定义开头在基线的建表语句里找到唯一的那个。
	native := func(table, definition string) string {
		t.Helper()
		var ddl string
		if err := st.Pool.QueryRow(ctx, `SELECT sql FROM sqlite_schema WHERE type='table' AND name=$1`, table).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, line := range strings.Split(ddl, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 2 && fields[0] == "CONSTRAINT" && strings.HasPrefix(strings.Join(fields[2:], " "), definition) {
				names = append(names, fields[1])
			}
		}
		if len(names) != 1 {
			t.Fatalf("formal native constraint missing/ambiguous: %s %s", table, definition)
		}
		return names[0]
	}
	accountCheck := native("project_credential_accounts", "CHECK ((((purpose = 'model')")
	selectionCheck := native("agent_credential_selections", "CHECK ((((mode = 'explicit')")
	selectionOwnerFK := native("agent_credential_selections", "FOREIGN KEY (agent_id, project_id)")
	selectionAccountFK := native("agent_credential_selections", "FOREIGN KEY (account_id, project_id, purpose, slot_key)")
	cliOwnerFK := native("project_cli_versions", "FOREIGN KEY (account_id, purpose, cli_name)")
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
			statementOK := false
			err := st.Pool.InTx(ctx, func(tx *db.Tx) error {
				_, err := tx.Exec(ctx, c.sql, c.args...)
				statementOK = err == nil
				return err
			})
			if c.deferred && !statementOK {
				t.Fatal("expected deferred FK must allow statement before commit")
			}
			if !nativeRejection(err, c) {
				t.Fatalf("native DB rejection mismatch: want %s/%s got code %d (raw error withheld)", c.code, c.constraint, db.Code(err))
			}
			t.Logf("native %s table=%s constraint=%s column=%s deferred=%t", c.code, c.table, c.constraint, c.column, c.deferred)
		})
	}
	t.Run("valid_selections_and_duplicate_native_rejection", func(t *testing.T) {
		for _, args := range [][]any{{agent.ID, p1, "model", "openai", model.View.ID, "explicit"}, {agent.ID, p1, "cli", "gh", cli.View.ID, "explicit"}, {agent.ID, p1, "model", "anthropic", nil, "automatic_override"}} {
			if _, err = st.Pool.Exec(ctx, selectSQL, args...); err != nil {
				t.Fatal("legal explicit/automatic_override selection rejected")
			}
		}
		_, err = st.Pool.Exec(ctx, selectSQL, agent.ID, p1, "model", "openai", modelOtherOwner.View.ID, "explicit")
		if db.Code(err) != db.CodeConstraintPK || !db.IsUniqueViolation(err, "agent_credential_selections", "agent_id", "purpose", "slot_key") {
			t.Fatal("duplicate selection was not rejected by native primary key")
		}
		t.Log("native primary key agent_credential_selections_pkey; distinct slots and model automatic_override NULL accepted")
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

// nativeRejection 报告 err 是否为用例期望的那种约束失败。SQLite 的 CHECK 报约束名、NOT NULL 报表与列；
// 外键失败不报约束名，只看结果码（约束名已由基线与 PostgreSQL 的对照测试核对）。
func nativeRejection(err error, c credentialConstraintCase) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch c.code {
	case "23514":
		return db.Code(err) == db.CodeConstraintCheck && strings.Contains(msg, "CHECK constraint failed: "+c.constraint)
	case "23503":
		return db.Code(err) == db.CodeConstraintFK
	case "23502":
		return db.Code(err) == db.CodeConstraintNotNull && strings.Contains(msg, "NOT NULL constraint failed: "+c.table+"."+c.column)
	}
	return false
}
