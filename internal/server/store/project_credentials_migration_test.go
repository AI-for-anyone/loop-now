package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/testutil/pgtest"
)

type projectMigrationEnv struct {
	s         *store.Store
	templates *clitemplate.Catalog
	project   uuid.UUID
}

// projectMigrationFixture stops at schema 011 so callers can choose the next ceiling.
func projectMigrationFixture(t *testing.T) projectMigrationEnv {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: pgtest.NewSchema(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err = s.MigrateUpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	dc, err := s.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return projectMigrationEnv{s, templates, dc.Project.ID}
}

// agent uses current Store code once its columns exist (024+); earlier schemas
// get the minimal original row.
func (e projectMigrationEnv) agent(t *testing.T, name, provider string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var current bool
	if err := e.s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='agents' AND column_name='thinking_level')`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if !current {
		id := uuid.New()
		mustExec(t, e.s, `INSERT INTO agents(id,project_id,name,model_provider,model_id)VALUES($1,$2,$3,$4,'fixture')`, id, e.project, name, provider)
		return id
	}
	a, err := e.s.CreateAgent(ctx, store.ProjectScope(e.project), name, provider, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// migrateFixture migrates to the latest schema, or only through ceiling for
// historical Down/Up round trips: 026 cannot be rolled back.
func migrateFixture(ctx context.Context, s *store.Store, ceiling int64) error {
	if ceiling > 0 {
		return s.MigrateUpTo(ctx, ceiling)
	}
	return s.Migrate(ctx, quietLogger())
}

// insertCredentialRow writes a plaintext credential row in the shape of the
// current schema: the 026 secret column, or the one-time conversion marker
// (alg plaintext-v1, plaintext in ciphertext) before 026.
func insertCredentialRow(t *testing.T, s *store.Store, table string, columns []string, values []any, plain []byte) {
	t.Helper()
	var hasSecret bool
	if err := s.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name='secret')`, table).Scan(&hasSecret); err != nil {
		t.Fatal(err)
	}
	if hasSecret {
		columns = append(columns, "secret")
		values = append(values, plain)
	} else {
		columns = append(columns, "alg", "key_id", "wrapped_dek", "nonce", "ciphertext")
		values = append(values, "plaintext-v1", "", []byte{}, []byte{}, plain)
	}
	marks := make([]string, len(values))
	for i := range marks {
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	mustExec(t, s, `INSERT INTO `+table+`(`+strings.Join(columns, ",")+`)VALUES(`+strings.Join(marks, ",")+`)`, values...)
}
func (e projectMigrationEnv) version(t *testing.T, agent uuid.UUID, provider string, generation int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	credential, err := modelcredential.APIKey("fixture-private-value")
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := credential.Encode()
	insertCredentialRow(t, e.s, "model_auth_versions", []string{"id", "agent_id", "provider", "auth_method", "generation"}, []any{id, agent, provider, "api_key", generation}, plain)
	return id
}
func (e projectMigrationEnv) legacy(t *testing.T, agent uuid.UUID, purpose string, env map[string]string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	plain, _ := json.Marshal(clitemplate.Payload{Env: env, Files: []clitemplate.PayloadFile{}})
	var cli, source, format *string
	names := []string{}
	if purpose == "cli" {
		v, src, f := "gh", "builtin", "1"
		cli, source, format = &v, &src, &f
		names = []string{"token"}
		mustExec(t, e.s, `INSERT INTO agent_clis(agent_id,cli_name)VALUES($1,'gh') ON CONFLICT DO NOTHING`, agent)
	}
	insertCredentialRow(t, e.s, "agent_credentials", []string{"id", "agent_id", "purpose", "cli_name", "template_source", "template_version", "field_names", "env_names", "file_paths"}, []any{id, agent, purpose, cli, source, format, names, clitemplate.Payload{Env: env}.EnvNames(), []string{}}, plain)
	return id
}
func (e projectMigrationEnv) legacyTask(t *testing.T, agent uuid.UUID) {
	t.Helper()
	task := uuid.New()
	document, _ := json.Marshal(map[string]any{"view": map[string]any{"task": map[string]any{"status": "running", "version": 1}}})
	mustExec(t, e.s, `INSERT INTO task_records(id,project_id,status,document,version)VALUES($1,$2,'running',$3,1)`, task, e.project, document)
	mustExec(t, e.s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot)VALUES($1,$2,$3,'fixture',$4)`, uuid.New(), task, agent, []byte(`{"schemaVersion":3}`))
}

// oldTask inserts a converted (schema 8, sourceVersion) task snapshot whose
// credentials are plaintext, with its frozen snapshot identity.
func (e projectMigrationEnv) oldTask(t *testing.T, agent uuid.UUID, version int, ended, retired bool, binding *modelcredential.Binding, env map[string]string) (uuid.UUID, uuid.UUID, []byte) {
	t.Helper()
	runtime, task, snapshotID := uuid.New(), uuid.New(), uuid.New()
	cfg := taskruntime.Configuration{AgentID: agent, ConfigVersion: 1, Provider: "openai", ModelID: "fixture", Prompt: "", CLIs: []string{}, Skills: []taskruntime.Skill{}}
	if version >= 3 {
		cfg.Prompts = &prompts.Layers{Format: prompts.FormatV1, ProjectID: &e.project, ProjectVersion: 1, AgentVersion: 1}
	}
	raw, err := json.Marshal(map[string]any{"schemaVersion": 8, "sourceVersion": version, "snapshotId": snapshotID, "scope": map[string]any{"agentId": agent, "taskId": task}, "configuration": cfg, "credentials": map[string]any{"modelAuth": binding, "model": env, "cli": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	status := "running"
	var endedAt *time.Time
	if ended {
		status = "completed"
		now := time.Now().UTC()
		endedAt = &now
	}
	document, _ := json.Marshal(map[string]any{"view": map[string]any{"task": map[string]any{"status": status, "version": 1}}})
	mustExec(t, e.s, `INSERT INTO task_records(id,project_id,status,document,version,ended_at)VALUES($1,$2,$3,$4,1,$5)`, task, e.project, status, document, endedAt)
	mustExec(t, e.s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared,retired)VALUES($1,$2,$3,'fixture',$4,encode(sha256($4),'hex'),true,$5)`, runtime, task, agent, raw, retired)
	return runtime, task, raw
}
func assertProjectMigrationRolledBack(t *testing.T, e projectMigrationEnv) {
	t.Helper()
	var exists bool
	if err := e.s.Pool.QueryRow(context.Background(), `SELECT to_regclass('project_credential_accounts') IS NOT NULL OR EXISTS(SELECT 1 FROM goose_db_version WHERE version_id=12 AND is_applied)`).Scan(&exists); err != nil || exists {
		t.Fatal("012 refusal did not roll back DDL/version", err)
	}
}

// Pre-012 credentials and snapshots were sealed by the removed master key.
// 012 refuses them and names the supported upgrade path instead of reading them.
func TestProjectCredentialsMigrationRefusesLegacySecrets(t *testing.T) {
	for _, kind := range []string{"agent_credentials", "model_auth_versions", "task_instances"} {
		t.Run(kind, func(t *testing.T) {
			e := projectMigrationFixture(t)
			ctx := context.Background()
			a := e.agent(t, "原账号", "openai")
			switch kind {
			case "agent_credentials":
				e.legacy(t, a, "model", map[string]string{"OPENAI_API_KEY": "fixture-private-value"})
			case "model_auth_versions":
				v := e.version(t, a, "openai", 3)
				mustExec(t, e.s, `INSERT INTO agent_model_auth(agent_id,version_id)VALUES($1,$2)`, a, v)
			case "task_instances":
				e.legacyTask(t, a)
			}
			err := e.s.Migrate(ctx, quietLogger())
			if err == nil || !strings.Contains(err.Error(), "运行一次性明文凭证转换程序") || !strings.Contains(err.Error(), "025") {
				t.Fatal("legacy secret data was not refused with the upgrade path", err)
			}
			if strings.Contains(err.Error(), "fixture-private-value") {
				t.Fatal("secret leaked")
			}
			assertProjectMigrationRolledBack(t, e)
		})
	}
}

// Deleted projects were never migrated by 012; their rows stay agent owned.
func TestProjectCredentialsMigrationIgnoresDeletedProjectRows(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	deleted := uuid.New()
	mustExec(t, e.s, `INSERT INTO projects(id,organization_id,name)SELECT $1,organization_id,'已删项目' FROM projects WHERE id=$2`, deleted, e.project)
	e.project = deleted
	a := e.agent(t, "已删项目", "openai")
	e.legacy(t, a, "model", map[string]string{"OPENAI_API_KEY": "fixture"})
	e.version(t, a, "openai", 1)
	mustExec(t, e.s, `UPDATE projects SET deleted_at=now() WHERE id=$1`, deleted)
	if err := e.s.MigrateUpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	var owned int
	if err := e.s.Pool.QueryRow(ctx, `SELECT count(*) FROM model_auth_versions WHERE agent_id=$1 AND project_account_id IS NULL`, a).Scan(&owned); err != nil || owned != 1 {
		t.Fatal("deleted project version moved", owned, err)
	}
}

func TestProjectCredentialsMigrationMovesNonSecretState(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	cleared := e.agent(t, "已清除", "openai")
	mustExec(t, e.s, `INSERT INTO agent_model_auth(agent_id,version_id)VALUES($1,NULL)`, cleared)
	pending := e.agent(t, "登录中", "openai")
	op := uuid.New()
	mustExec(t, e.s, `INSERT INTO model_auth_operations(id,agent_id,provider,config_version,status,authorization_url,expires_at)VALUES($1,$2,'openai-codex',1,'awaiting_authorization','https://auth.invalid/x',$3)`, op, pending, time.Now().Add(time.Hour))
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
	var barriers, pointers int
	if err := e.s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_model_legacy_cleared WHERE agent_id=$1),(SELECT count(*) FROM agent_model_auth WHERE agent_id=$1)`, cleared).Scan(&barriers, &pointers); err != nil || barriers != 1 || pointers != 0 {
		t.Fatal("NULL pointer barrier not moved", barriers, pointers, err)
	}
	var status, code string
	var url *string
	if err := e.s.Pool.QueryRow(ctx, `SELECT status,error_code,authorization_url FROM model_auth_operations WHERE id=$1`, op).Scan(&status, &code, &url); err != nil || status != "cancelled" || code != "server_restarted" || url != nil {
		t.Fatal("pending legacy login not cancelled", status, code, err)
	}
}

func TestProjectCredentialsMigrationEmptyDownUp(t *testing.T) {
	e := projectMigrationFixture(t)
	ctx := context.Background()
	if err := e.s.MigrateUpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if err := e.s.MigrateDownTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Migrate(ctx, quietLogger()); err != nil {
		t.Fatal(err)
	}
}
