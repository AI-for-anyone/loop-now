package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
	"strings"
	"sync"
	"testing"
)

// Uses only a self-owned schema; never runs pgtest's stale-schema sweep.
func dbConnectionFixture(t *testing.T) projectMigrationEnv { return dbConnectionFixtureAt(t, 0) }

// dbConnectionFixtureAt stops at ceiling (0: latest); 026 cannot be rolled back.
func dbConnectionFixtureAt(t *testing.T, ceiling int64) projectMigrationEnv {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("TEST connection unavailable")
	}
	schema := "db_connections_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	st, err := store.Open(ctx, url, store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateFixture(ctx, st, ceiling); err != nil {
		t.Fatal(err)
	}
	defaults, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	return projectMigrationEnv{st, templates, defaults.Project.ID}
}
func TestDBConnectionSetsCASModesAndAtomicity(t *testing.T) {
	e := dbConnectionFixture(t)
	ctx := context.Background()
	a := e.agent(t, "多连接", "openai")
	if err := e.s.AddAgentCLI(ctx, a, "mysql"); err != nil {
		t.Fatal(err)
	}
	makeAccount := func(name string) cm.Account {
		plain, _ := json.Marshal(clitemplate.Payload{Env: map[string]string{}, Files: []clitemplate.PayloadFile{{Path: "db.cnf", Content: []byte("synthetic")}}})
		r, err := e.s.CreateProjectCredential(ctx, e.project, "cli", "mysql", name, store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: "1"}, Secret: plain})
		if err != nil {
			t.Fatal(err)
		}
		return r.View
	}
	one, two := makeAccount("A"), makeAccount("B")
	state, err := e.s.GetDBConnections(ctx, a, "mysql")
	if err != nil || state.Mode != "legacy_single" || state.Status != "selection_required" {
		t.Fatal("legacy multiple", state, err)
	}
	cli := "mysql"
	legacy, err := e.s.PutCredentialSelection(ctx, a, cm.SelectionPut{Purpose: "cli", CLI: &cli, AccountID: &one.ID, ExpectedConfigVersion: state.ConfigVersion})
	if err != nil {
		t.Fatal(err)
	}
	expected := legacy.ConfigVersion
	state, err = e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: []uuid.UUID{two.ID, one.ID}, ExpectedConfigVersion: expected})
	if err != nil || state.Mode != "explicit_set" || len(state.Connections) != 2 || state.Status != "resolved" {
		t.Fatal("set", state, err)
	}
	if _, err = e.s.PutCredentialSelection(ctx, a, cm.SelectionPut{Purpose: "cli", CLI: &cli, AccountID: nil, ExpectedConfigVersion: state.ConfigVersion}); !apperr.Is(err, apperr.CodeCredentialSelectionModeConflict) {
		t.Fatal("single changed active set", err)
	}
	old, err := e.s.GetCredentialSelections(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range old.Selections {
		if v.CLI != nil && *v.CLI == cli && v.SelectedAccountID != nil && *v.SelectedAccountID == one.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("legacy selection lost")
	}
	for _, ids := range [][]uuid.UUID{{one.ID, one.ID}, {uuid.Nil}, {uuid.New()}} {
		if _, err = e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: ids, ExpectedConfigVersion: state.ConfigVersion}); err == nil {
			t.Fatal("invalid set accepted")
		}
		after, err := e.s.GetDBConnections(ctx, a, cli)
		if err != nil || after.ConfigVersion != state.ConfigVersion || len(after.Connections) != 2 {
			t.Fatal("failed transaction changed set", err)
		}
	}
	if _, err = e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: []uuid.UUID{}, ExpectedConfigVersion: expected}); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("stale CAS", err)
	}
	// Same CAS, two different full replacements: one commit, no union/partial set.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []uuid.UUID{one.ID, two.ID} {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			_, e := e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: []uuid.UUID{id}, ExpectedConfigVersion: state.ConfigVersion})
			errs <- e
		}(id)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if apperr.Is(err, apperr.CodePreconditionFailed) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS admitted multiple commits")
	}
	state, err = e.s.GetDBConnections(ctx, a, cli)
	if err != nil || len(state.Connections) != 1 {
		t.Fatal(err)
	}
	empty, err := e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: []uuid.UUID{}, ExpectedConfigVersion: state.ConfigVersion})
	if err != nil || empty.Mode != "explicit_set" || len(empty.Connections) != 0 || empty.Status != "not_configured" {
		t.Fatal("empty fallback", empty, err)
	}
	restored, err := e.s.DeleteDBConnections(ctx, a, cli, empty.ConfigVersion)
	if err != nil || restored.Mode != "legacy_single" || len(restored.Connections) != 1 || restored.Connections[0].AccountID != one.ID {
		t.Fatal("restore legacy", restored, err)
	}
	same, err := e.s.DeleteDBConnections(ctx, a, cli, restored.ConfigVersion)
	if err != nil || same.ConfigVersion != restored.ConfigVersion {
		t.Fatal("absent DELETE not idempotent", err)
	}
	// Real Down refuses a nonempty mode marker and leaves the version applied.
	if _, err = e.s.PutDBConnections(ctx, a, cli, cm.DBConnectionsPut{AccountIDs: []uuid.UUID{}, ExpectedConfigVersion: same.ConfigVersion}); err != nil {
		t.Fatal(err)
	}
	if err = e.s.MigrateDownTo(ctx, 12); err == nil {
		t.Fatal("lossy Down allowed")
	}
}

func TestDBConnectionCaptureVersionsAndFailureIsolation(t *testing.T) {
	e := dbConnectionFixture(t)
	ctx := context.Background()
	a := e.agent(t, "冻结连接", "openai")
	if err := e.s.AddAgentCLI(ctx, a, "mysql"); err != nil {
		t.Fatal(err)
	}
	tmpl, _ := e.templates.Get("mysql")
	makeWrite := func(password string) store.ProjectCredentialWrite {
		payload, fields, err := tmpl.Render(map[string]string{"host": "offline.example", "port": "3306", "user": "synthetic", "password": password, "database": "db", "sslMode": "DISABLED"})
		if err != nil {
			t.Fatal(err)
		}
		plain, _ := json.Marshal(payload)
		return store.ProjectCredentialWrite{TemplateSource: "builtin", Meta: store.CredentialMeta{TemplateVersion: tmpl.CredentialFormat(), FieldNames: fields, EnvNames: payload.EnvNames(), FilePaths: payload.FilePaths()}, Secret: plain}
	}
	one, err := e.s.CreateProjectCredential(ctx, e.project, "cli", "mysql", "A", makeWrite("synthetic-old"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := e.s.CreateProjectCredential(ctx, e.project, "cli", "mysql", "B", makeWrite("synthetic-B"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.s.GetDBConnections(ctx, a, "mysql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.PutDBConnections(ctx, a, "mysql", cm.DBConnectionsPut{AccountIDs: []uuid.UUID{one.View.ID, two.View.ID}, ExpectedConfigVersion: state.ConfigVersion}); err != nil {
		t.Fatal(err)
	}
	old, err := e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || len(old.CredentialRows) != 2 {
		t.Fatal("group capture", err)
	}
	captured := map[uuid.UUID]uuid.UUID{}
	for _, row := range old.CredentialRows {
		if row.DBConnection == nil || row.DBConnection.VersionID == nil {
			t.Fatal("capture identity missing")
		}
		captured[row.DBConnection.AccountID] = *row.DBConnection.VersionID
	}
	rename := "renamed"
	named, err := e.s.MutateProjectCredential(ctx, e.project, one.View.ID, 1, &rename, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || renamed.CredentialIdentity != old.CredentialIdentity {
		t.Fatal("name changed fingerprint", err)
	}
	write := makeWrite("synthetic-new")
	replacement, err := e.s.MutateProjectCredential(ctx, e.project, one.View.ID, named.View.Revision, nil, &write, false)
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.s.CaptureExecutionIdentity(ctx, a)
	if err != nil || next.CredentialIdentity == old.CredentialIdentity {
		t.Fatal("replacement not captured", err)
	}
	if captured[one.View.ID] != *one.View.CurrentVersionID || captured[one.View.ID] == *replacement.View.CurrentVersionID {
		t.Fatal("old capture rewritten")
	}
	for _, row := range old.CredentialRows {
		if len(row.Secret) == 0 {
			t.Fatal("old captured credential discarded")
		}
	}
	if _, err = e.s.MutateProjectCredential(ctx, e.project, two.View.ID, 1, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.CaptureExecutionIdentity(ctx, a); !apperr.Is(err, apperr.CodeCredentialAccountDeleted) {
		t.Fatal("deleted member partially captured", err)
	}
	state, err = e.s.GetDBConnections(ctx, a, "mysql")
	if err != nil || len(state.AccountIDs) != 2 || state.Status != "account_deleted" {
		t.Fatal("deleted binding hidden", err)
	}
	if err = e.s.RemoveAgentCLI(ctx, a, "mysql"); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_db_connection_sets WHERE agent_id=$1`, a).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("permission removal retained set", err)
	}
}

func TestDBConnectionMigrationEmptyDownUpPreservesOldVersions(t *testing.T) {
	// 026 refuses Down, so the 013 round trip runs on a 025 schema seeded in its
	// own column shape (the conversion marker form).
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.MigrateUpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	a := e.agent(t, "旧账号保留", "openai")
	account, version := uuid.New(), uuid.New()
	mustExec(t, e.s, `INSERT INTO project_credential_accounts(id,project_id,purpose,provider,name)VALUES($1,$2,'model','openai','旧账号')`, account, e.project)
	credential, err := modelcredential.APIKey("synthetic-original")
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := credential.Encode()
	insertCredentialRow(t, e.s, "model_auth_versions", []string{"id", "project_account_id", "provider", "auth_method"}, []any{version, account, "openai", "api_key"}, plain)
	mustExec(t, e.s, `UPDATE project_credential_accounts SET current_model_version_id=$2 WHERE id=$1`, account, version)
	mustExec(t, e.s, `INSERT INTO agent_credential_selections(agent_id,project_id,purpose,slot_key,account_id,mode)VALUES($1,$2,'model','openai',$3,'explicit')`, a, e.project, account)
	if err = e.s.MigrateDownTo(ctx, 12); err != nil {
		t.Fatal("empty 013 Down", err)
	}
	if err = e.s.MigrateUpTo(ctx, 13); err != nil {
		t.Fatal("013 Up again", err)
	}
	var current, selected uuid.UUID
	var stored []byte
	if err = e.s.Pool.QueryRow(ctx, `SELECT a.current_model_version_id,s.account_id,v.ciphertext FROM project_credential_accounts a JOIN agent_credential_selections s ON s.account_id=a.id AND s.agent_id=$2 JOIN model_auth_versions v ON v.id=a.current_model_version_id WHERE a.id=$1`, account, a).Scan(&current, &selected, &stored); err != nil || current != version || selected != account || !bytes.Equal(stored, plain) {
		t.Fatal("old binding changed", err)
	}
	var markers int
	if err = e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_db_connection_sets`).Scan(&markers); err != nil || markers != 0 {
		t.Fatal("Up invented connections", err)
	}
}
