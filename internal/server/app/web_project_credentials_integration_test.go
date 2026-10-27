package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/credentialmodel"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/testenv"
)

// Main granted Web this one new test file. Existing helpers and production
// App/HTTP/store source remain owned by server and are reused read-only.
func TestWebProjectCredentialsCRUDIntegration(t *testing.T) {
	webProjectCredentialsIntegration(t, "crud")
}

func TestWebProjectCredentialsDeleteIntegration(t *testing.T) {
	webProjectCredentialsIntegration(t, "delete")
}

func TestWebProjectCredentialsFocusIntegration(t *testing.T) {
	webProjectCredentialsIntegration(t, "focus")
}

func webProjectCredentialsIntegration(t *testing.T, slice string) {
	if os.Getenv("LOOP_NOW_WEB_PROJECT_CREDENTIALS_INTEGRATION") != "1" {
		t.Skip("explicit isolated project credential CRUD browser integration only")
	}
	for _, key := range []string{"LOOP_NOW_TEST_DATABASE_URL", "LOOP_NOW_TEST_REDIS_URL"} {
		if os.Getenv(key) == "" {
			t.Fatal("explicit TEST database and Redis endpoints required")
		}
	}
	db, err := url.Parse(os.Getenv("LOOP_NOW_TEST_DATABASE_URL"))
	if err != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("only explicit test database permitted")
	}
	rdb, err := redis.ParseURL(os.Getenv("LOOP_NOW_TEST_REDIS_URL"))
	if err != nil || rdb.DB != 10 {
		t.Fatal("explicit TEST Redis DB10 required")
	}
	root := testenv.RepoRoot()
	deleteOnly := slice != "crud"
	evidence := filepath.Join(root, "web/test-results/add-project-credentials/app-"+slice)
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	var w *world
	var address string
	audit := map[string]any{"mode": "actual React -> route.fetch -> temporary app.Build -> isolated TEST PostgreSQL/Redis; CRUD only; no OAuth/model requests"}
	// Runs last: existing world cleanup removes schema, prefix and work root;
	// App shutdown and Node's self-owned frontend cleanup must already finish.
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		audit["appAddress"] = address
		conn, err := pgx.Connect(ctx, pgtest.URL(t))
		if err != nil {
			t.Error("cannot verify isolated schema cleanup")
			return
		}
		defer conn.Close(ctx)
		var exists bool
		err = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists)
		audit["schemaRemoved"] = err == nil && !exists
		if err != nil || exists {
			t.Error("isolated schema cleanup not verified")
		}
		rc := redis.NewClient(rdb)
		defer rc.Close()
		iter := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := iter.Next(ctx)
		audit["redisPrefixEmpty"] = !found && iter.Err() == nil
		if found || iter.Err() != nil {
			t.Error("isolated Redis prefix cleanup not verified")
		}
		_, err = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(err)
		if !os.IsNotExist(err) {
			t.Error("isolated work root cleanup not verified")
		}
		closed := true
		for _, addr := range []string{address, "127.0.0.1:4317"} {
			if addr == "" {
				continue
			}
			c, e := net.DialTimeout("tcp", addr, 200*time.Millisecond)
			if e == nil {
				closed = false
				c.Close()
				t.Error("self-owned temporary listener still open")
			}
		}
		audit["temporaryListenersClosed"] = closed
		data, _ := json.MarshalIndent(audit, "", "  ")
		if err := os.WriteFile(filepath.Join(evidence, "driver-results.json"), data, 0600); err != nil {
			t.Error("cannot persist sanitized driver audit")
		}
	})
	w = newWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4317"}
	s := w.start(nil)
	address = s.a.Addr()
	t.Cleanup(s.stop)
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	_, worker := s.createAgent("WebProjectCredentialWorker")
	private := decode[apitypes.Agent](t, s.must(201, "POST", "/platform/agents", map[string]any{"name": "WebPlatformPrivate", "model": worker.Model}))
	var mainAccount credentialmodel.Account
	if deleteOnly {
		mainAccount = decode[credentialmodel.Account](t, s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": worker.Model.Provider, "name": "单账号删除相邻回归", "credential": map[string]any{"type": "api_key", "key": "synthetic-main-delete-neighbor"}}))
		s.must(200, "PUT", "/agents/"+worker.ID.String()+"/credential-selections", map[string]any{"purpose": "model", "provider": worker.Model.Provider, "accountId": mainAccount.ID, "expectedConfigVersion": worker.ConfigVersion})
		s.must(200, "PUT", "/agents/"+private.ID.String()+"/model-credential", map[string]any{"provider": private.Model.Provider, "key": "synthetic-platform-delete-neighbor", "expectedConfigVersion": private.ConfigVersion})
	}
	removeProject := decode[apitypes.Project](t, s.must(201, "POST", "/projects", map[string]any{"name": "WebCredentialDeleteProject"}))
	removeAccount := decode[credentialmodel.Account](t, s.must(201, "POST", "/projects/"+removeProject.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": worker.Model.Provider, "name": "删除项目测试账号", "credential": map[string]any{"type": "api_key", "key": "synthetic-delete-project-key"}}))
	manifest := map[string]any{"slice": slice, "apiBase": s.url, "webOrigin": "http://127.0.0.1:4317", "projectId": dc.Project.ID, "projectName": dc.Project.Name, "agentId": worker.ID, "provider": worker.Model.Provider, "platformAgentId": private.ID, "deleteProjectId": removeProject.ID, "deleteProjectName": removeProject.Name, "mainAccountId": mainAccount.ID, "mainAccountName": mainAccount.Name}
	manifestPath := filepath.Join(t.TempDir(), "project-credentials-browser-manifest.json")
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, raw, 0600); err != nil {
		t.Fatal("cannot persist private test manifest")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/project-credentials-app.mjs"))
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		// Also bound cleanup when CommandContext must kill Node before JS finally.
		// Only the process group created by this driver is ever signalled.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_PROJECT_CREDENTIALS_MANIFEST=" + manifestPath}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("actual project credential browser failed: %v; sanitized browser-failure.json retained", err)
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "PASS ") {
			t.Log(line)
		}
	}
	var result struct {
		IDs map[string]uuid.UUID `json:"ids"`
	}
	raw, err = os.ReadFile(filepath.Join(evidence, "browser-results.json"))
	if err != nil || json.Unmarshal(raw, &result) != nil {
		t.Fatal("cannot read sanitized browser identities")
	}
	ctx := context.Background()
	counts := map[string]int{}
	readCount := func(name, query string, args ...any) {
		t.Helper()
		var count int
		if err := s.a.Store.Pool.QueryRow(ctx, query, args...).Scan(&count); err != nil {
			t.Fatalf("PG read-back %s failed", name)
		}
		counts[name] = count
	}
	if slice == "focus" {
		readCount("untouchedMainAccount", `SELECT count(*) FROM project_credential_accounts WHERE id=$1 AND deleted_at IS NULL`, mainAccount.ID)
		readCount("platformPrivateVersion", `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1 AND project_account_id IS NULL AND octet_length(secret)>0`, private.ID)
		readCount("runs", `SELECT count(*) FROM runs`)
		readCount("usageRecords", `SELECT count(*) FROM usage_records`)
		readCount("projectOAuthOperations", `SELECT count(*) FROM project_model_auth_operations`)
		readCount("privateOAuthOperations", `SELECT count(*) FROM model_auth_operations`)
		for name, expected := range map[string]int{"untouchedMainAccount": 1, "platformPrivateVersion": 1, "runs": 0, "usageRecords": 0, "projectOAuthOperations": 0, "privateOAuthOperations": 0} {
			if counts[name] != expected {
				t.Errorf("PG %s got %d want %d", name, counts[name], expected)
			}
		}
		audit["pgReadback"] = counts
		audit["browserCompleted"] = true
		return
	}
	if deleteOnly {
		readCount("accountDeletedTombstone", `SELECT count(*) FROM project_credential_accounts WHERE id=$1 AND deleted_at IS NOT NULL`, mainAccount.ID)
		readCount("selectedDeletedPointerRetained", `SELECT count(*) FROM agent_credential_selections WHERE agent_id=$1 AND account_id=$2 AND mode='explicit'`, worker.ID, mainAccount.ID)
		readCount("deletedAccountVersionsRetained", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1 AND octet_length(secret)>0`, mainAccount.ID)
		readCount("platformPrivateVersion", `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1 AND project_account_id IS NULL AND octet_length(secret)>0`, private.ID)
		readCount("deletedProjectSecrets", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1 AND octet_length(secret)>0`, removeAccount.ID)
		readCount("deletedProjectVersionRows", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, removeAccount.ID)
		readCount("deletedProjectTombstone", `SELECT count(*) FROM projects WHERE id=$1 AND deleted_at IS NOT NULL`, removeProject.ID)
		readCount("runs", `SELECT count(*) FROM runs`)
		readCount("usageRecords", `SELECT count(*) FROM usage_records`)
		readCount("projectOAuthOperations", `SELECT count(*) FROM project_model_auth_operations`)
		readCount("privateOAuthOperations", `SELECT count(*) FROM model_auth_operations`)
		for name, expected := range map[string]int{"accountDeletedTombstone": 1, "selectedDeletedPointerRetained": 1, "deletedAccountVersionsRetained": 1, "platformPrivateVersion": 1, "deletedProjectSecrets": 0, "deletedProjectVersionRows": 0, "deletedProjectTombstone": 1, "runs": 0, "usageRecords": 0, "projectOAuthOperations": 0, "privateOAuthOperations": 0} {
			if counts[name] != expected {
				t.Errorf("PG %s got %d want %d", name, counts[name], expected)
			}
		}
		audit["pgReadback"] = counts
		audit["browserCompleted"] = true
		return
	}
	readCount("liveProjectModelAccounts", `SELECT count(*) FROM project_credential_accounts WHERE project_id=$1 AND purpose='model' AND deleted_at IS NULL`, dc.Project.ID)
	readCount("deletedSelectedAccount", `SELECT count(*) FROM project_credential_accounts WHERE id=$1 AND deleted_at IS NOT NULL`, result.IDs["accountB"])
	readCount("selectedDeletedPointerRetained", `SELECT count(*) FROM agent_credential_selections WHERE agent_id=$1 AND account_id=$2 AND mode='explicit'`, worker.ID, result.IDs["accountB"])
	readCount("accountAVersions", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1 AND octet_length(secret)>0`, result.IDs["accountA"])
	readCount("deletedAccountVersionsRetained", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, result.IDs["accountB"])
	readCount("projectCLIVersion", `SELECT count(*) FROM project_cli_versions WHERE account_id=$1 AND octet_length(secret)>0`, result.IDs["cliAccount"])
	readCount("platformPrivateVersion", `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1 AND project_account_id IS NULL AND octet_length(secret)>0`, private.ID)
	readCount("deletedProjectSecrets", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1 AND octet_length(secret)>0`, removeAccount.ID)
	readCount("deletedProjectVersionRows", `SELECT count(*) FROM model_auth_versions WHERE project_account_id=$1`, removeAccount.ID)
	readCount("deletedProjectTombstone", `SELECT count(*) FROM projects WHERE id=$1 AND deleted_at IS NOT NULL`, removeProject.ID)
	readCount("runs", `SELECT count(*) FROM runs`)
	readCount("usageRecords", `SELECT count(*) FROM usage_records`)
	readCount("projectOAuthOperations", `SELECT count(*) FROM project_model_auth_operations`)
	readCount("privateOAuthOperations", `SELECT count(*) FROM model_auth_operations`)
	for name, expected := range map[string]int{"liveProjectModelAccounts": 1, "deletedSelectedAccount": 1, "selectedDeletedPointerRetained": 1, "accountAVersions": 2, "deletedAccountVersionsRetained": 1, "projectCLIVersion": 1, "platformPrivateVersion": 1, "deletedProjectSecrets": 0, "deletedProjectVersionRows": 0, "deletedProjectTombstone": 1, "runs": 0, "usageRecords": 0, "projectOAuthOperations": 0, "privateOAuthOperations": 0} {
		if counts[name] != expected {
			t.Errorf("PG %s got %d want %d", name, counts[name], expected)
		}
	}
	audit["pgReadback"] = counts
	audit["browserCompleted"] = true
}
