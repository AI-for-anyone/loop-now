package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/testutil/testenv"
)

// latestMigration is the highest numbered SQL migration shipped in the repo.
func latestMigration(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(testenv.RepoRoot(), "internal/server/store/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	latest := 0
	for _, e := range entries {
		if n, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0]); err == nil && n > latest {
			latest = n
		}
	}
	return latest
}

// Actual temporary App with every shipped migration. Authentication uses only
// synthetic DI fixtures; no browser navigation, provider or model request.
func TestSchemaCompatibilityCLIInboxAndOfflineAuth(t *testing.T) {
	w := newWorld(t)
	fixture := &taskAuthOffline{}
	fixture.account.Store("schema-offline-account")
	s := w.startOfflineAuth(fixture)
	defer s.stop()
	ctx := context.Background()
	rows, err := s.a.Store.Pool.Query(ctx, `SELECT version_id FROM goose_db_version WHERE is_applied AND version_id>0 ORDER BY version_id`)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(versions) != latestMigration(t) {
		t.Fatal("not using complete real schema", versions, err)
	}
	for i, v := range versions {
		if v != int64(i+1) {
			t.Fatal("missing migration", versions)
		}
	}
	oldIdentity := s.must(201, "POST", "/external-identities", map[string]any{"name": "schema-old-ci"})
	s.must(422, "POST", "/external-identities", map[string]any{"name": "platform"})
	var count int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM external_identities WHERE name='platform'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("reserved identity persisted", err)
	}
	if !strings.Contains(string(s.must(200, "GET", "/external-identities", nil)), "schema-old-ci") || !strings.Contains(string(oldIdentity), "schema-old-ci") {
		t.Fatal("existing identity changed after rejected creation")
	}
	path, agent := s.createPlatformAgent("schema旧CLI")
	s.must(200, "PUT", path+"/clis/gh", nil)
	for _, token := range []string{"synthetic-cli-first", "synthetic-cli-replacement"} {
		response := s.must(200, "PUT", path+"/clis/gh/credential", map[string]any{"fields": map[string]string{"token": token}})
		if strings.Contains(string(response), token) {
			t.Fatal("credential plaintext projected")
		}
		var source string
		if err := s.a.Store.Pool.QueryRow(ctx, `SELECT template_source FROM agent_credentials WHERE agent_id=$1 AND purpose='cli' AND cli_name='gh'`, agent.ID).Scan(&source); err != nil || source != "builtin" {
			t.Fatal("CLI source missing after actual HTTP write", err)
		}
	}
	s.must(200, "PUT", path+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-key-schema"})
	authState := decode[modelauth.State](t, s.must(200, "GET", path+"/model-auth", nil))
	if authState.Credential == nil || authState.Credential.VersionID == nil {
		t.Fatal("new model auth key was not versioned")
	}
	s.must(204, "DELETE", path+"/clis/gh/credential", nil)
	s.must(204, "DELETE", path+"/clis/gh", nil) // do not invoke CLI installation during Inbox fixture.
	s.must(201, "POST", "/inbox/messages", map[string]any{"to": "schema旧CLI", "text": "schema-old-inbox-fixture"})
	s.waitIdle(path)
	var messages int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='console' AND to_kind='agent' AND to_agent_id=$1 AND text='schema-old-inbox-fixture'`, agent.ID).Scan(&messages); err != nil || messages != 1 {
		t.Fatal("old Inbox categories changed", err)
	}
	s.must(200, "GET", "/inbox/messages", nil)

	codex := decode[apitypes.Agent](t, s.must(201, "POST", "/platform/agents", map[string]any{"name": "schema模拟Codex", "model": map[string]string{"provider": "openai-codex", "modelId": "gpt-6.1-sol"}, "prompt": "offline"}))
	base := "/agents/" + codex.ID.String() + "/model-auth"
	op := decode[modelauth.Operation](t, s.must(200, "POST", base+"/operations", map[string]any{"provider": "openai-codex", "expectedConfigVersion": codex.ConfigVersion}))
	succeeded := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		view := decode[modelauth.State](t, s.must(200, "GET", base, nil))
		if view.Operation != nil && view.Operation.ID == op.ID && view.Operation.Status == "succeeded" {
			if view.Operation.AuthorizationURL != nil || view.Credential == nil || view.Credential.VersionID == nil {
				t.Fatal("bad terminal auth projection")
			}
			succeeded = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !succeeded {
		t.Fatal("offline OAuth did not finish")
	}
	s.must(204, "DELETE", path+"/model-credential", nil)
	if fixture.refreshes.Load() != 0 {
		t.Fatal("schema test attempted refresh")
	}
	var plugins int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM plugins`).Scan(&plugins); err != nil || plugins != 0 {
		t.Fatal("schema prep installed plugin behavior", err)
	}
}
