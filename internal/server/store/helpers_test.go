package store_test

// 多个测试文件共用的辅助函数。原来散在已删除的 PostgreSQL 迁移测试里，改到 SQLite 时集中到这里。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/testutil/sqlitetest"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustExec(t *testing.T, s *store.Store, sql string, args ...any) {
	t.Helper()
	if _, err := s.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

type projectMigrationEnv struct {
	s         *store.Store
	templates *clitemplate.Catalog
	project   uuid.UUID
}

// projectMigrationFixture 返回基线上的默认项目与模板目录（名字沿用 PostgreSQL 时期：那时先停在 011 再升级）。
func projectMigrationFixture(t *testing.T) projectMigrationEnv {
	t.Helper()
	s := sqlitetest.NewStore(t)
	dc, err := s.EnsureDefaults(context.Background(), "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return projectMigrationEnv{s, templates, dc.Project.ID}
}

func (e projectMigrationEnv) agent(t *testing.T, name, provider string) uuid.UUID {
	t.Helper()
	a, err := e.s.CreateAgent(context.Background(), store.ProjectScope(e.project), name, provider, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

// insertCredentialRow 按当前结构写一行明文凭证（secret 列）。
func insertCredentialRow(t *testing.T, s *store.Store, table string, columns []string, values []any, plain []byte) {
	t.Helper()
	columns = append(columns, "secret")
	values = append(values, plain)
	marks := make([]string, len(values))
	for i := range marks {
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	mustExec(t, s, `INSERT INTO `+table+`(`+strings.Join(columns, ",")+`)VALUES(`+strings.Join(marks, ",")+`)`, values...)
}

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
	mustExec(t, e.s, `INSERT INTO task_records(id,project_id,status,document,version)VALUES($1,$2,'running',$3,1)`, task, e.project, json.RawMessage(document))
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
	mustExec(t, e.s, `INSERT INTO task_records(id,project_id,status,document,version,ended_at)VALUES($1,$2,$3,$4,1,$5)`, task, e.project, status, json.RawMessage(document), endedAt)
	mustExec(t, e.s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot,snapshot_sha,prepared,retired)VALUES($1,$2,$3,'fixture',$4,$6,true,$5)`, runtime, task, agent, raw, retired, sha256Hex(raw))
	return runtime, task, raw
}

// sha256Hex 是快照内容的 SHA-256 十六进制串（task_instances.snapshot_sha）。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
