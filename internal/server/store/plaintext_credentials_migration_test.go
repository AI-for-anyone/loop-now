package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// Fixtures here write the 025 column shapes directly. The one-time conversion
// program is the only production writer of the plaintext marker forms.
const plaintextMarker = "plaintext-v1"

type plaintextFixture struct {
	projectMigrationEnv
	agent, platform                         uuid.UUID
	agentCredential, modelVersion           uuid.UUID
	cliAccount, cliVersion, legacyRow       uuid.UUID
	pluginInstance                          uuid.UUID
	plugin, pluginAccount, liveVersion      uuid.UUID
	purgedVersion                           uuid.UUID
	runtime                                 uuid.UUID
	snapshot                                []byte
	originalSHA                             string
	agentPlain, modelPlain, cliPlain, plain []byte
}

func plaintextFixture025(t *testing.T) *plaintextFixture {
	t.Helper()
	e := projectMigrationFixture(t)
	if err := e.s.MigrateUpTo(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	return &plaintextFixture{projectMigrationEnv: e, agent: e.agent(t, "转换Agent", "openai")}
}

func (f *plaintextFixture) bytea(t *testing.T) {
	t.Helper()
	f.agentCredential = f.legacy(t, f.agent, "model", map[string]string{"OPENAI_API_KEY": "plain-agent"})
	if err := f.s.Pool.QueryRow(context.Background(), `SELECT ciphertext FROM agent_credentials WHERE id=$1`, f.agentCredential).Scan(&f.agentPlain); err != nil {
		t.Fatal(err)
	}
	f.modelPlain = []byte(`{"type":"api_key","key":"plain-model"}`)
	platform, err := f.s.CreateAgent(context.Background(), store.PlatformScope(), "平台Agent", "openai", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	f.platform = platform.ID
	f.modelVersion = uuid.New()
	insertCredentialRow(t, f.s, "model_auth_versions", []string{"id", "agent_id", "provider", "auth_method"}, []any{f.modelVersion, f.platform, "openai", "api_key"}, f.modelPlain)
	f.cliPlain = []byte(`{"env":{"GH_TOKEN":"plain-gh"},"files":[]}`)
	f.cliAccount, f.cliVersion = rawProjectCredential(t, f.projectMigrationEnv, "cli", "gh", "明文gh", "static", f.cliPlain)
	f.plain = []byte(`{"env":{"OPENAI_API_KEY":"plain-legacy"},"files":[]}`)
	f.legacyRow = uuid.New()
	insertCredentialRow(t, f.s, "project_credential_legacy_rows", []string{"id", "project_id", "source_agent_id", "field_names", "env_names", "file_paths", "updated_at"}, []any{f.legacyRow, f.project, f.agent, []string{}, []string{"OPENAI_API_KEY"}, []string{}, time.Now()}, f.plain)
}

func (f *plaintextFixture) plugins(t *testing.T, accountCipher, secrets string) {
	t.Helper()
	legacy := uuid.New()
	mustExec(t, f.s, `INSERT INTO plugins(id,name,source,status)VALUES($1,'legacy','{}','installed')`, legacy)
	f.pluginInstance = uuid.New()
	mustExec(t, f.s, `INSERT INTO plugin_instances(id,plugin_id,project_id)VALUES($1,$2,$3)`, f.pluginInstance, legacy, f.project)
	f.plugin = uuid.New()
	mustExec(t, f.s, `INSERT INTO platform_plugins(id,name,display_name,source,version,manifest)VALUES($1,'fixture','Fixture','{}','1.0.0','{}')`, f.plugin)
	f.pluginAccount, f.liveVersion, f.purgedVersion = uuid.New(), uuid.New(), uuid.New()
	mustExec(t, f.s, `INSERT INTO platform_plugin_accounts(id,project_id,plugin_id,kind,name,revision,version_id)VALUES($1,$2,$3,'token','账号',2,$4)`, f.pluginAccount, f.project, f.plugin, f.liveVersion)
	mustExec(t, f.s, `INSERT INTO platform_plugin_account_versions(id,account_id,project_id,plugin_id,kind,name,values,secret_fields,cipher)VALUES($1,$2,$3,$4,'token','账号','{"host":"h"}','["token"]',$5),($6,$2,$3,$4,'token','账号','{}','[]','{}')`, f.liveVersion, f.pluginAccount, f.project, f.plugin, accountCipher, f.purgedVersion)
	mustExec(t, f.s, `INSERT INTO project_platform_plugins(project_id,plugin_id,enabled,config_version,secrets)VALUES($1,$2,true,2,$3)`, f.project, f.plugin, secrets)
	mustExec(t, f.s, `INSERT INTO platform_plugin_config_versions(project_id,plugin_id,version,secrets)VALUES($1,$2,1,'{}'),($1,$2,2,$3)`, f.project, f.plugin, secrets)
}

// task writes one task instance with the given snapshot. A non-empty
// originalSHA is recorded in the conversion hand-off table.
func (f *plaintextFixture) task(t *testing.T, snapshot []byte, originalSHA string, handoff bool) {
	t.Helper()
	task := uuid.New()
	f.runtime, f.snapshot, f.originalSHA = uuid.New(), snapshot, originalSHA
	mustExec(t, f.s, `INSERT INTO task_records(id,project_id,status,document,version)VALUES($1,$2,'running','{"view":{"task":{"status":"running","version":1}}}',1)`, task, f.project)
	mustExec(t, f.s, `INSERT INTO task_instances(id,task_id,agent_id,agent_name,snapshot)VALUES($1,$2,$3,'fixture',$4)`, f.runtime, task, f.agent, snapshot)
	if handoff {
		mustExec(t, f.s, `CREATE TABLE IF NOT EXISTS plaintext_credential_snapshot_shas(runtime_id uuid PRIMARY KEY,original_sha text NOT NULL)`)
		if originalSHA != "" {
			mustExec(t, f.s, `INSERT INTO plaintext_credential_snapshot_shas(runtime_id,original_sha)VALUES($1,$2)`, f.runtime, originalSHA)
		}
	}
}

func plaintextSnapshot(t *testing.T, version int, agent uuid.UUID) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schemaVersion": version, "sourceVersion": 4, "snapshotId": uuid.New(), "scope": map[string]any{"agentId": agent, "taskId": uuid.New()}, "configuration": map[string]any{"agentId": agent}, "credentials": map[string]any{"model": map[string]string{"OPENAI_API_KEY": "plain-snapshot"}, "cli": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func sha(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func columnsOf(t *testing.T, s *store.Store, table string) map[string]bool {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[c] = true
	}
	return out
}

func ledger(t *testing.T, s *store.Store) int {
	t.Helper()
	var v int
	if err := s.Pool.QueryRow(context.Background(), `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPlaintextCredentialsMigrationEmptySchema(t *testing.T) {
	ctx := context.Background()
	// 031 删除了这里检查的插件表，所以停在 026。
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: pgtest.NewSchema(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.MigrateUpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	if ledger(t, s) < 26 {
		t.Fatal("fresh schema did not reach 026")
	}
	sealed := []string{"alg", "key_id", "wrapped_dek", "nonce", "ciphertext"}
	for _, table := range []string{"agent_credentials", "model_auth_versions", "project_cli_versions", "project_credential_legacy_rows"} {
		cols := columnsOf(t, s, table)
		if !cols["secret"] {
			t.Fatal("secret column missing", table)
		}
		for _, c := range sealed {
			if cols[c] {
				t.Fatal("sealed column kept", table, c)
			}
		}
	}
	cols := columnsOf(t, s, "plugin_instances")
	for _, c := range append(sealed, "secret") {
		if cols[c] {
			t.Fatal("plugin_instances kept credential column", c)
		}
	}
	var constraint bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='plugin_instances_sealed_all_or_none' AND connamespace=current_schema()::regnamespace)`).Scan(&constraint); err != nil || constraint {
		t.Fatal("plugin_instances sealed constraint kept", err)
	}
	cols = columnsOf(t, s, "platform_plugin_account_versions")
	if !cols["secret"] || cols["cipher"] {
		t.Fatal("plugin account version column not renamed")
	}
	var nullable, shaNullable string
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='platform_plugin_account_versions' AND column_name='secret'),(SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='task_instances' AND column_name='snapshot_sha')`).Scan(&nullable, &shaNullable); err != nil || nullable != "YES" || shaNullable != "NO" {
		t.Fatal("nullability wrong", nullable, shaNullable, err)
	}
	var handoff bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('plaintext_credential_snapshot_shas') IS NOT NULL`).Scan(&handoff); err != nil || handoff {
		t.Fatal("hand-off table left behind", err)
	}
	// Without credential data 026 rolls back to the 025 layout and re-applies.
	if err := s.MigrateDownTo(ctx, 25); err != nil {
		t.Fatal("empty 026 Down", err)
	}
	if ledger(t, s) != 25 {
		t.Fatal("empty 026 Down did not reach 025")
	}
	for _, table := range []string{"agent_credentials", "model_auth_versions", "project_cli_versions", "project_credential_legacy_rows", "plugin_instances"} {
		cols := columnsOf(t, s, table)
		for _, c := range sealed {
			if !cols[c] {
				t.Fatal("025 column not restored", table, c)
			}
		}
		if cols["secret"] {
			t.Fatal("plaintext column left after Down", table)
		}
	}
	cols = columnsOf(t, s, "platform_plugin_account_versions")
	if cols["secret"] || !cols["cipher"] || columnsOf(t, s, "task_instances")["snapshot_sha"] {
		t.Fatal("025 plugin/task layout not restored")
	}
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='platform_plugin_account_versions' AND column_name='cipher'),EXISTS(SELECT 1 FROM pg_constraint WHERE conname='plugin_instances_sealed_all_or_none' AND connamespace=current_schema()::regnamespace)`).Scan(&nullable, &constraint); err != nil || nullable != "NO" || !constraint {
		t.Fatal("025 constraints not restored", nullable, constraint, err)
	}
	if err := s.MigrateUpTo(ctx, 26); err != nil || ledger(t, s) != 26 {
		t.Fatal("026 re-apply after empty Down", err)
	}
}

func TestPlaintextCredentialsMigrationConvertsMarkedData(t *testing.T) {
	f := plaintextFixture025(t)
	ctx := context.Background()
	f.bytea(t)
	f.plugins(t, `{"format":"plaintext-v1","values":{"host":"h","token":"plain-plugin-account"}}`, `{"token":"plain-plugin-config"}`)
	snapshot := plaintextSnapshot(t, 8, f.agent)
	original := sha([]byte("original sealed snapshot bytes"))
	f.task(t, snapshot, original, true)
	if err := f.s.MigrateUpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		table string
		id    uuid.UUID
		want  []byte
	}{{"agent_credentials", f.agentCredential, f.agentPlain}, {"model_auth_versions", f.modelVersion, f.modelPlain}, {"project_cli_versions", f.cliVersion, f.cliPlain}, {"project_credential_legacy_rows", f.legacyRow, f.plain}} {
		var got []byte
		if err := f.s.Pool.QueryRow(ctx, `SELECT secret FROM `+c.table+` WHERE id=$1`, c.id).Scan(&got); err != nil || !bytes.Equal(got, c.want) {
			t.Fatal("plaintext not kept verbatim", c.table, err)
		}
	}
	var live, purged *string
	if err := f.s.Pool.QueryRow(ctx, `SELECT (SELECT secret::text FROM platform_plugin_account_versions WHERE id=$1),(SELECT secret::text FROM platform_plugin_account_versions WHERE id=$2)`, f.liveVersion, f.purgedVersion).Scan(&live, &purged); err != nil || live == nil || *live != `{"host": "h", "token": "plain-plugin-account"}` || purged != nil {
		t.Fatal("plugin account values not unwrapped or purge not NULL", live, purged, err)
	}
	var current, history string
	if err := f.s.Pool.QueryRow(ctx, `SELECT (SELECT secrets::text FROM project_platform_plugins WHERE plugin_id=$1),(SELECT secrets::text FROM platform_plugin_config_versions WHERE plugin_id=$1 AND version=2)`, f.plugin).Scan(&current, &history); err != nil || current != `{"token": "plain-plugin-config"}` || history != current {
		t.Fatal("plugin config secrets changed", current, history, err)
	}
	var kept bool
	if err := f.s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_instances WHERE id=$1)`, f.pluginInstance).Scan(&kept); err != nil || !kept {
		t.Fatal("legacy plugin instance row lost", err)
	}
	var stored []byte
	var frozen string
	if err := f.s.Pool.QueryRow(ctx, `SELECT snapshot,snapshot_sha FROM task_instances WHERE id=$1`, f.runtime).Scan(&stored, &frozen); err != nil || !bytes.Equal(stored, snapshot) || frozen != original {
		t.Fatal("snapshot or frozen identity wrong", err)
	}
	if frozen == sha(snapshot) {
		t.Fatal("frozen identity recomputed from converted bytes")
	}
	var handoff bool
	if err := f.s.Pool.QueryRow(ctx, `SELECT to_regclass('plaintext_credential_snapshot_shas') IS NOT NULL`).Scan(&handoff); err != nil || handoff {
		t.Fatal("hand-off table left behind", err)
	}
	// Current Store code reads the converted rows.
	row, _, err := f.s.GetProjectCLIVersion(ctx, f.cliAccount, f.cliVersion)
	if err != nil || !bytes.Equal(row.Secret, f.cliPlain) {
		t.Fatal("store read of converted CLI credential", err)
	}
	inst, err := f.s.TaskInstance(ctx, f.runtime)
	if err != nil || !bytes.Equal(inst.Snapshot, snapshot) {
		t.Fatal("store read of converted snapshot", err)
	}
	// Plaintext credential data cannot be rolled back to the encrypted layout.
	if err = f.s.MigrateDownTo(ctx, 25); err == nil || !strings.Contains(err.Error(), "plaintext_credentials_down_refused") {
		t.Fatal("026 Down with credential data was not refused", err)
	}
	if ledger(t, f.s) != 26 || !columnsOf(t, f.s, "agent_credentials")["secret"] {
		t.Fatal("refused 026 Down changed the schema")
	}
}

func TestPlaintextCredentialsMigrationRefusesEncryptedLeftovers(t *testing.T) {
	sealedAccount := `{"Alg":"A256GCM-envelope-v1","KeyID":"local-0123456789abcdef","WrappedDEK":"AQ==","Nonce":"Ag==","Ciphertext":"Aw=="}`
	sealedField := `{"token":` + sealedAccount + `}`
	for _, c := range []struct {
		name, want string
		seed       func(t *testing.T, f *plaintextFixture)
	}{
		{"agent_credentials", "agent_credentials=1", func(t *testing.T, f *plaintextFixture) {
			f.bytea(t)
			mustExec(t, f.s, `UPDATE agent_credentials SET alg='A256GCM-envelope-v1'`)
		}},
		{"model_auth_versions", "model_auth_versions=1", func(t *testing.T, f *plaintextFixture) {
			f.bytea(t)
			mustExec(t, f.s, `UPDATE model_auth_versions SET alg='A256GCM-envelope-v1'`)
		}},
		{"project_cli_versions", "project_cli_versions=1", func(t *testing.T, f *plaintextFixture) {
			f.bytea(t)
			mustExec(t, f.s, `UPDATE project_cli_versions SET alg='A256GCM-envelope-v1'`)
		}},
		{"project_credential_legacy_rows", "project_credential_legacy_rows=1", func(t *testing.T, f *plaintextFixture) {
			f.bytea(t)
			mustExec(t, f.s, `UPDATE project_credential_legacy_rows SET alg='A256GCM-envelope-v1'`)
		}},
		{"plugin_instances", "plugin_instances=1", func(t *testing.T, f *plaintextFixture) {
			f.plugins(t, `{}`, `{}`)
			mustExec(t, f.s, `UPDATE plugin_instances SET alg='A256GCM-envelope-v1',key_id='k',wrapped_dek='\x01',nonce='\x02',ciphertext='\x03'`)
		}},
		{"platform_plugin_account_versions", "platform_plugin_account_versions=1", func(t *testing.T, f *plaintextFixture) {
			f.plugins(t, sealedAccount, `{}`)
		}},
		{"project_platform_plugins", "project_platform_plugins=1", func(t *testing.T, f *plaintextFixture) {
			f.plugins(t, `{}`, sealedField)
			mustExec(t, f.s, `UPDATE platform_plugin_config_versions SET secrets='{}'`)
		}},
		{"platform_plugin_config_versions", "platform_plugin_config_versions=1", func(t *testing.T, f *plaintextFixture) {
			f.plugins(t, `{}`, `{}`)
			mustExec(t, f.s, `UPDATE platform_plugin_config_versions SET secrets=$1 WHERE version=2`, sealedField)
		}},
		{"task_instances_sealed", "task_instances=1", func(t *testing.T, f *plaintextFixture) {
			raw := []byte(`{"schemaVersion":7,"credentials":` + sealedAccount + `}`)
			f.task(t, raw, sha(raw), true)
		}},
		{"task_instances_without_handoff", "task_instances.original_snapshot_sha=missing", func(t *testing.T, f *plaintextFixture) {
			f.task(t, plaintextSnapshot(t, 8, f.agent), "", false)
		}},
		{"task_instances_missing_handoff_row", "task_instances.original_snapshot_sha=1", func(t *testing.T, f *plaintextFixture) {
			f.task(t, plaintextSnapshot(t, 8, f.agent), "", true)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := plaintextFixture025(t)
			ctx := context.Background()
			c.seed(t, f)
			err := f.s.MigrateUpTo(ctx, 26)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "转换程序") {
				t.Fatal("leftover encrypted record was not refused with its location", err)
			}
			if strings.Contains(err.Error(), "plain-") {
				t.Fatal("refusal leaked a credential")
			}
			if ledger(t, f.s) != 25 {
				t.Fatal("refused 026 advanced the ledger")
			}
			for table, column := range map[string]string{"agent_credentials": "alg", "model_auth_versions": "ciphertext", "plugin_instances": "wrapped_dek", "platform_plugin_account_versions": "cipher"} {
				if !columnsOf(t, f.s, table)[column] {
					t.Fatal("refused 026 left partial DDL", table, column)
				}
			}
			if columnsOf(t, f.s, "task_instances")["snapshot_sha"] {
				t.Fatal("refused 026 kept snapshot_sha")
			}
		})
	}
}
