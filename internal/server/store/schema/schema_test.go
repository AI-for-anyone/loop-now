package schema_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/server/store/schema"
	"loop-now/internal/testutil/sqlitetest"

	"modernc.org/sqlite"
)

// events.db holds only these four prunable log tables, plus the plugin event capture table for lifecycle_events.
var eventsTables = []string{"audit_log", "lifecycle_events", "plugin_host_log", "plugin_lifecycle_changes", "run_tool_events"}

// Tables that are not part of the business schema.
func ignoredTable(name string) bool {
	return name == "goose_db_version" || name == "sqlite_sequence" || strings.HasPrefix(name, "sqlite_")
}

type tableInfo struct {
	schema string
	strict bool
	sql    string
}

func tables(t *testing.T, d *db.DB) map[string]tableInfo {
	t.Helper()
	ctx := context.Background()
	out := map[string]tableInfo{}
	for _, s := range []string{"main", db.EventsSchema} {
		rows, err := d.Query(ctx, fmt.Sprintf(`SELECT l.name, l.strict, m.sql FROM pragma_table_list l JOIN %s.sqlite_schema m ON m.name = l.name AND m.type = 'table' WHERE l.schema = ? AND l.type = 'table'`, s), s)
		if err != nil {
			t.Fatal(err)
		}
		var name, ddl string
		var strict bool
		if _, err := db.ForEachRow(rows, []any{&name, &strict, &ddl}, func() error {
			if !ignoredTable(name) {
				out[name] = tableInfo{schema: s, strict: strict, sql: ddl}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestBaselineShape(t *testing.T) {
	d := sqlitetest.NewDB(t)
	ctx := context.Background()
	ts := tables(t, d)
	var inEvents []string
	for name, ti := range ts {
		if !ti.strict {
			t.Errorf("%s is not a STRICT table", name)
		}
		if ti.schema == db.EventsSchema {
			inEvents = append(inEvents, name)
		}
	}
	slices.Sort(inEvents)
	if !slices.Equal(inEvents, eventsTables) {
		t.Errorf("events.db tables = %v, want %v", inEvents, eventsTables)
	}
	// The baseline has 90 tables (the 87 tables from PG plus sequences and the two plugin event capture tables),
	// migration 2 adds the 3 tables for local mode, migration 13 the local ACP commands, and migration 15 who writes
	// with AI, migration 16 background tasks, migration 18 permission settings and requests.
	if len(ts) != 98 {
		t.Errorf("%d tables in total, want 98", len(ts))
	}
	for _, name := range eventsTables {
		var n int
		if err := d.QueryRow(ctx, `SELECT count(*) FROM pragma_foreign_key_list(?, ?)`, name, db.EventsSchema).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s in events.db should have no foreign keys: %d %v", name, n, err)
		}
	}
	for _, s := range []string{"main", db.EventsSchema} {
		var res string
		if err := d.QueryRow(ctx, "PRAGMA "+s+".integrity_check").Scan(&res); err != nil || res != "ok" {
			t.Errorf("%s integrity_check: %q %v", s, res, err)
		}
		rows, err := d.Query(ctx, "PRAGMA "+s+".foreign_key_check")
		if err != nil {
			t.Fatalf("%s foreign_key_check: %v", s, err)
		}
		if rows.Next() {
			t.Errorf("%s foreign_key_check should be empty", s)
		}
		rows.Close()
	}
	var dirs, git, cli, logins bool
	if err := d.QueryRow(ctx, `SELECT directories, git, cli, logins FROM local_mode_settings`).Scan(&dirs, &git, &cli, &logins); err != nil || !dirs || !git || !cli || logins {
		t.Errorf("local mode switches: directories, git and CLI should default to on, logins to off: %v %v %v %v %v", dirs, git, cli, logins, err)
	}
	var seqs string
	if err := d.QueryRow(ctx, `SELECT group_concat(name || '=' || value, ',') FROM (SELECT * FROM sequences ORDER BY name)`).Scan(&seqs); err != nil ||
		seqs != "conversation_messages.seq=0,runs.queue_seq=0,task_turns.queue_seq=0" {
		t.Errorf("sequences: %q %v", seqs, err)
	}
	// Migrating again does nothing.
	before, err := schema.Version(ctx, d)
	if err != nil || before < schema.Baseline {
		t.Fatalf("version = %d %v, want at least %d", before, err, schema.Baseline)
	}
	if err := schema.Migrate(ctx, d, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if v, err := schema.Version(ctx, d); err != nil || v != latestVersion {
		t.Fatalf("version = %d %v, want %d", v, err, latestVersion)
	}
}

// latestVersion is the version of the last migration; update it when adding a migration.
const latestVersion = 18

// TestMigrateToBaseline: when importing an old database, first migrate only to the baseline (without the tables added
// after the baseline), then to the latest.
func TestMigrateToBaseline(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	log := slog.New(slog.DiscardHandler)
	if err := schema.MigrateTo(ctx, d, log, schema.Baseline); err != nil {
		t.Fatal(err)
	}
	if v, err := schema.Version(ctx, d); err != nil || v != schema.Baseline {
		t.Fatalf("version = %d %v, want %d", v, err, schema.Baseline)
	}
	if ts := tables(t, d); len(ts) != 90 {
		t.Fatalf("the baseline has %d tables, want 90", len(ts))
	}
	// Migration 2 rebuilds agent_folder_git_pulls (two new failure codes): existing records are kept as they are.
	st := store.New(d)
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := oldAgent(ctx, d, dc.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	pull := func(path string) uuid.UUID {
		t.Helper()
		// Written directly: CreateFolderGitPull reads the Agent with today's columns.
		id := uuid.Must(uuid.NewV7())
		if _, err := d.Exec(ctx, `INSERT INTO agent_folder_git_pulls (id, agent_id, action_id, url, branch, path, status) VALUES ($1, $2, $3, 'https://example.com/r.git', 'main', $4, 'queued')`,
			id, a.ID, uuid.New(), path); err != nil {
			t.Fatal(err)
		}
		if ok, err := st.StartFolderGitPull(ctx, id); err != nil || !ok {
			t.Fatal(err)
		}
		return id
	}
	old, queued := pull("old"), pull("queued")
	if err := st.FinishFolderGitPull(ctx, old, "clone_failed", "original failure"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishFolderGitPull(ctx, queued, "auth_failed", "x"); err == nil {
		t.Fatal("the baseline should not accept auth_failed")
	}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	if v, err := schema.Version(ctx, d); err != nil || v != latestVersion {
		t.Fatalf("version = %d %v, want %d", v, err, latestVersion)
	}
	if got, err := st.GetFolderGitPull(ctx, a.ID, old); err != nil || got.Status != "failed" || *got.ErrorCode != "clone_failed" || *got.ErrorMessage != "original failure" {
		t.Fatalf("old record after the rebuild: %+v %v", got, err)
	}
	if got, err := st.GetFolderGitPull(ctx, a.ID, queued); err != nil || got.Status != "running" {
		t.Fatalf("in-progress record after the rebuild: %+v %v", got, err)
	}
	for _, code := range []string{"auth_failed", "host_key_unverified"} {
		if err := st.FinishFolderGitPull(ctx, pull(code), code, "x"); err != nil {
			t.Fatalf("%s should be accepted after the migration: %v", code, err)
		}
	}
	// The unique index for in-progress pulls is still there: the same path cannot have two pulls in progress.
	if _, _, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: uuid.New(), URL: "https://example.com/r.git", Branch: "main", Path: "queued"}); err == nil {
		t.Fatal("the unique index should reject a second in-progress pull of the same path")
	}
	var fk string
	if err := d.QueryRow(ctx, `SELECT count(*) FROM pragma_foreign_key_list('agent_folder_git_pulls')`).Scan(&fk); err != nil || fk != "1" {
		t.Fatalf("the foreign key should be kept: %q %v", fk, err)
	}
}

// The 13 regex CHECKs were replaced by GLOB; here the part of each constraint converted from a regex is extracted and
// compared one by one with the same regex (Go and PG give these patterns the same semantics). Some constraints have
// other conditions as well (e.g. the name must not be console); only the regex part is compared.
var regexChecks = map[string]struct{ column, pattern string }{
	"agent_clis_cli_name_check":                               {"cli_name", `^[a-z0-9][a-z0-9._-]{0,63}$`},
	"blobs_sha256_check":                                      {"sha256", `^[0-9a-f]{64}$`},
	"cli_credential_verifications_request_hash_check":         {"request_hash", `^[a-f0-9]{64}$`},
	"cli_credential_verifications_template_fingerprint_check": {"template_fingerprint", `^[a-f0-9]{64}$`},
	"external_identities_name_check":                          {"name", `^[a-z0-9][a-z0-9._-]{0,63}$`},
	"external_tokens_display_prefix_check":                    {"display_prefix", `^lne_[0-9a-f]{8}$`},
	"github_cli_identities_user_id_check":                     {"user_id", `^[1-9][0-9]*$`},
	"lifecycle_events_type_check":                             {"type", `^[a-z_]+(\.[a-z_]+)+$`},
	"plugin_events_id_check":                                  {"id", `^[0-9A-HJKMNP-TV-Z]{26}$`},
	"plugins_id_check":                                        {"id", `^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`},
	"task_instances_snapshot_sha":                             {"snapshot_sha", `^[0-9a-f]{64}$`},
	"task_lark_binding_members_base_snapshot_sha_check":       {"base_snapshot_sha", `^[0-9a-f]{64}$`},
	"project_cli_auth_github_view":                            {"user_code", `^[A-Z0-9]{4}-[A-Z0-9]{4}$`},
}

// regexFragment extracts, from the constraint named name in a CREATE TABLE statement, the part converted from a regex:
// the parenthesized group starting with "(length(column)" or "(column GLOB".
func regexFragment(ddl, name, column string) (string, bool) {
	i := strings.Index(ddl, "CONSTRAINT "+name+" CHECK ")
	if i < 0 {
		return "", false
	}
	s := ddl[i:]
	if end := strings.Index(s, "\n"); end >= 0 {
		s = s[:end]
	}
	j := strings.Index(s, "(length("+column+")")
	if j < 0 {
		j = strings.Index(s, "("+column+" GLOB")
	}
	if j < 0 {
		return "", false
	}
	return balanced(s[j:])
}

// balanced returns the prefix that starts with an opening parenthesis and ends at its matching closing parenthesis.
func balanced(s string) (string, bool) {
	depth := 0
	for j, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:j+1], true
			}
		}
	}
	return "", false
}

func TestRegexChecksMatchPostgres(t *testing.T) {
	d := sqlitetest.NewDB(t)
	ctx := context.Background()
	ts := tables(t, d)
	var all []string
	for _, ti := range ts {
		all = append(all, ti.sql)
	}
	ddl := strings.Join(all, "\n")
	if n := strings.Count(ddl, " GLOB "); n == 0 {
		t.Fatal("the baseline has no GLOB checks")
	}
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._- é\n")
	for name, c := range regexChecks {
		expr, ok := regexFragment(ddl, name, c.column)
		if !ok {
			// A name mismatch means the constraint was changed on the PG side: fix this table first.
			t.Errorf("cannot find the part of constraint %s converted from a regex in the baseline", name)
			continue
		}
		re := regexp.MustCompile(c.pattern)
		cases := []string{"", "a", "-", ".", "a.b", "a..b", ".a", "a.", "lne_0123abcd", "lne_0123ABCD", "ABCD-1234", "abcd-1234",
			"1", "0", "10", "01", strings.Repeat("0", 64), strings.Repeat("F", 64), strings.Repeat("a", 64), strings.Repeat("a", 65),
			"a-b", "-ab", "ab-", "run.ended", "0123456789ABCDEFGHJKMNPQRS", "0123456789ABCDEFGHJKMNPQRI"}
		for range 2000 {
			n := r.IntN(70)
			b := make([]rune, n)
			for i := range b {
				b[i] = alphabet[r.IntN(len(alphabet))]
			}
			cases = append(cases, string(b))
		}
		q := fmt.Sprintf("SELECT %s FROM (SELECT ?1 AS %s)", expr, c.column)
		for _, s := range cases {
			var got bool
			if err := d.QueryRow(ctx, q, s).Scan(&got); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if want := re.MatchString(s); got != want {
				t.Errorf("%s(%q) = %v, PG regex %s gives %v", name, s, got, c.pattern, want)
			}
		}
	}
}

// rawConn opens a separate connection to the migrated data.db with foreign keys off and CHECKs ignored, so data can be
// made by filling only the NOT NULL columns.
// rawDriver is the driver used only by rawConn: the tables' time defaults call now(), which it provides here as the
// time of the call.
var rawDriver = sync.OnceValue(func() string {
	drv := &sqlite.Driver{}
	if err := drv.RegisterFunction("now", &sqlite.FunctionImpl{NArgs: 0, Scalar: func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return time.Now().UnixMicro(), nil
	}}); err != nil {
		panic(err)
	}
	sql.Register("sqlite-schema-raw", drv)
	return "sqlite-schema-raw"
})

func rawConn(t *testing.T, d *db.DB) *sql.DB {
	t.Helper()
	raw, err := sql.Open(rawDriver(), "file:"+filepath.Join(d.Dir(), db.DataFile)+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(0)&_pragma=ignore_check_constraints(1)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	return raw
}

// insertDummy inserts one row into a table: NOT NULL columns without defaults, other than those in overrides, are
// filled with placeholder values by type.
func insertDummy(t *testing.T, raw *sql.DB, table string, overrides map[string]any) {
	t.Helper()
	rows, err := raw.Query(`SELECT name, type, "notnull", dflt_value IS NOT NULL, hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	var cols []string
	var args []any
	for rows.Next() {
		var name, typ string
		var notnull, hasDefault bool
		var hidden int
		if err := rows.Scan(&name, &typ, &notnull, &hasDefault, &hidden); err != nil {
			t.Fatal(err)
		}
		v, ok := overrides[name]
		if !ok {
			if hidden != 0 || !notnull || hasDefault {
				continue
			}
			v = map[string]any{"TEXT": "x", "INTEGER": 1, "BLOB": []byte{0}}[typ]
		}
		cols = append(cols, `"`+name+`"`)
		args = append(args, v)
	}
	rows.Close()
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", "))
	if _, err := raw.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestGuardTriggersAndEmulations(t *testing.T) {
	d := sqlitetest.NewDB(t)
	raw := rawConn(t, d)

	// Feishu (Lark) credential bindings are append-only.
	insertDummy(t, raw, "task_lark_credential_bindings", map[string]any{"task_id": "t1", "revision": 1})
	insertDummy(t, raw, "task_lark_binding_members", map[string]any{"task_id": "t1", "revision": 1})
	for _, q := range []string{
		`UPDATE task_lark_credential_bindings SET revision = 2`,
		`DELETE FROM task_lark_credential_bindings`,
		`UPDATE task_lark_binding_members SET revision = 2`,
		`DELETE FROM task_lark_binding_members`,
	} {
		if _, err := raw.Exec(q); !db.IsRaise(err, "task_lark_binding_is_append_only") {
			t.Errorf("%s should be rejected, got %v", q, err)
		}
	}

	// Column-level SET NULL: deleting a version only sets live_version_id to NULL, so it can be deleted with foreign
	// keys on.
	insertDummy(t, raw, "project_cli_versions", map[string]any{"id": "v1", "account_id": "a1", "cli_name": "gh", "auth_method": "oauth"})
	insertDummy(t, raw, "cli_credential_verifications", map[string]any{"id": "c1", "account_id": "a1", "version_id": "v0", "live_version_id": "v1", "cli_name": "gh", "auth_method": "oauth"})
	insertDummy(t, raw, "cli_credential_import_receipts", map[string]any{"project_id": "p1", "provenance_key_id": "k", "origin_mac": []byte{1}, "account_id": "a1", "version_id": "v0", "live_version_id": "v1"})
	if _, err := raw.Exec(`PRAGMA foreign_keys = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM project_cli_versions WHERE id = 'v1'`); err != nil {
		t.Fatalf("deleting a referenced version should have the trigger clear the references first: %v", err)
	}
	var live1, live2 sql.NullString
	var account string
	if err := raw.QueryRow(`SELECT v.live_version_id, v.account_id, r.live_version_id FROM cli_credential_verifications v, cli_credential_import_receipts r`).Scan(&live1, &account, &live2); err != nil {
		t.Fatal(err)
	}
	if live1.Valid || live2.Valid || account != "a1" {
		t.Errorf("only live_version_id should be cleared: %v %q %v", live1, account, live2)
	}
	if _, err := raw.Exec(`PRAGMA foreign_keys = 0`); err != nil {
		t.Fatal(err)
	}

	// UNIQUE NULLS NOT DISTINCT: two rows with a NULL project_id count as duplicates.
	insertDummy(t, raw, "plugin_state", map[string]any{"plugin_id": "p", "project_id": nil, "key": "k"})
	_, err := raw.Exec(`INSERT INTO plugin_state (plugin_id, project_id, key, value) VALUES ('p', NULL, 'k', 'x')`)
	if !db.IsUniqueIndexViolation(err, "plugin_state_plugin_id_project_id_key_key") {
		t.Errorf("duplicate keys with a NULL project should conflict: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO plugin_state (plugin_id, project_id, key, value) VALUES ('p', 'proj', 'k', 'x')
		ON CONFLICT (plugin_id, coalesce(project_id, ''), key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Errorf("the expression conflict target should work: %v", err)
	}

	// stop_requested defaults to 0.
	for _, table := range []string{"runs", "task_turns"} {
		var dflt string
		if err := raw.QueryRow(`SELECT dflt_value FROM pragma_table_info(?) WHERE name = 'stop_requested'`, table).Scan(&dflt); err != nil || dflt != "0" {
			t.Errorf("%s.stop_requested default: %q %v", table, dflt, err)
		}
	}
}

// When v9 rebuilds runs it must keep main's Pi command columns and the conversation messages that reference them.
func TestConsoleCompactMigrationPreservesPiCommands(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := schema.MigrateTo(ctx, d, log, 8); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	defaults, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := oldAgent(ctx, d, defaults.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Version 8 data is written directly in SQL: the current store reads and writes the latest columns (e.g.
	// runs.compaction), so it cannot be used on an old database.
	runID, userID, replyID := uuid.New(), uuid.New(), uuid.New()
	notices := []byte(`{"notices":[{"message":"kept","level":"info"}],"truncated":false}`)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin, queued_at, started_at, ended_at, pi_command, pi_command_handled, pi_command_notices)
			VALUES ($1, $2, $3, 1, 1, 1, 'completed', 'console', now(), now(), now(), 'review', 1, $4)`, []any{runID, agent.ID, userID, string(notices)}},
		{`INSERT INTO conversation_messages (id, agent_id, seq, role, text, run_id, session_epoch) VALUES ($1, $2, 1, 'user', '/pi:review', $3, 1)`, []any{userID, agent.ID, runID}},
		{`INSERT INTO conversation_messages (id, agent_id, seq, role, text, run_id, session_epoch) VALUES ($1, $2, 2, 'assistant', 'reply', $3, 1)`, []any{replyID, agent.ID, runID}},
	} {
		if _, err := d.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	user, reply, run := store.Message{ID: userID}, store.Message{ID: replyID}, store.Run{ID: runID}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, run.ID)
	if err != nil || got.PiCommand == nil || *got.PiCommand != "review" || !got.PiCommandHandled || string(got.PiCommandNotices) != string(notices) || got.UserMessageID != user.ID {
		t.Fatalf("the Pi command details should be kept: %+v %v", got, err)
	}
	for _, id := range []uuid.UUID{user.ID, reply.ID} {
		msg, err := st.GetMessage(ctx, id)
		if err != nil || msg.RunID != run.ID {
			t.Fatalf("conversation messages should be kept: %+v %v", msg, err)
		}
	}
	if err := st.RecordSessionFile(ctx, agent.ID, "/tmp/session.jsonl"); err != nil {
		t.Fatal(err)
	}
	compact, err := st.InsertConsoleCompactRun(ctx, agent.ID, uuid.New(), uuid.New(), "keep the decisions")
	if err != nil || compact.UserMessageID != uuid.Nil || compact.Origin != "console_compact" || store.ParseCompaction(compact.Compaction).Instructions != "keep the decisions" {
		t.Fatalf("compaction run: %+v %v", compact, err)
	}
	if _, err := d.Exec(ctx, `UPDATE runs SET compaction = '{}' WHERE id = $1`, runID); err == nil {
		t.Fatal("a run that is not a manual compaction must not have compaction")
	}
	rows, err := d.Query(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("there should be no foreign key errors after the migration")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// v11 backfills numbers for existing tasks per project (by id when creation times are equal), and adds the numbers to
// the stored task objects (task.updated events, responses of idempotent actions); afterwards a number is always
// present, positive, unique within the project, and cannot be changed.
func TestTaskNumberMigration(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := schema.MigrateTo(ctx, d, log, 10); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	defaults, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, defaults.Organization.ID, "Another")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.CreateProject(ctx, defaults.Organization.ID, "No tasks")
	if err != nil {
		t.Fatal(err)
	}
	// Version 10 data is written directly in SQL. Two tasks in the default project have the same creation time and are
	// ordered by id; the insert order deliberately differs from the number order.
	a, b, c := uuid.MustParse("00000000-0000-7000-8000-00000000000a"), uuid.MustParse("00000000-0000-7000-8000-00000000000b"), uuid.MustParse("00000000-0000-7000-8000-00000000000c")
	x := uuid.New()
	tasks := []struct {
		id      uuid.UUID
		project uuid.UUID
		created int64
	}{{c, defaults.Project.ID, 3000}, {b, defaults.Project.ID, 2000}, {a, defaults.Project.ID, 2000}, {x, other.ID, 1000}}
	for _, task := range tasks {
		doc := fmt.Sprintf(`{"view":{"task":{"id":%q,"status":"running","version":1}}}`, task.id)
		if _, err := d.Exec(ctx, `INSERT INTO task_records(id,project_id,status,document,version,created_at,updated_at) VALUES($1,$2,'running',$3,1,$4,$4)`, task.id, task.project, doc, task.created); err != nil {
			t.Fatal(err)
		}
	}
	hash := make([]byte, 32)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO task_events(task_id,project_id,type,data) VALUES($1,$2,'task.updated',$3)`, []any{c, defaults.Project.ID, fmt.Sprintf(`{"task":{"id":%q,"title":"t"}}`, c)}},
		{`INSERT INTO task_events(task_id,project_id,type,data) VALUES($1,$2,'node.updated','{"node":{"id":"n"}}')`, []any{c, defaults.Project.ID}},
		{`INSERT INTO task_actions(actor_key,action_id,operation,task_id,body_hash,response) VALUES('m','create','createTask',$1,$2,$3)`, []any{b, hash, fmt.Sprintf(`{"task":{"id":%q},"nodes":[]}`, b)}},
		{`INSERT INTO task_actions(actor_key,action_id,operation,task_id,body_hash,response) VALUES('m','message','message',$1,$2,'{"messageId":"m1"}')`, []any{b, hash}},
	} {
		if _, err := d.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}

	want := map[uuid.UUID]int64{a: 1, b: 2, c: 3, x: 1}
	for id, n := range want {
		var column, inDoc int64
		if err := d.QueryRow(ctx, `SELECT number, document->'$.view.task'->>'number' FROM task_records WHERE id=$1`, id).Scan(&column, &inDoc); err != nil || column != n || inDoc != n {
			t.Errorf("task %s number = %d (document %d), want %d: %v", id, column, inDoc, n, err)
		}
	}
	for project, n := range map[uuid.UUID]int64{defaults.Project.ID: 3, other.ID: 1, empty.ID: 0} {
		var last int64
		if err := d.QueryRow(ctx, `SELECT last_task_number FROM projects WHERE id=$1`, project).Scan(&last); err != nil || last != n {
			t.Errorf("project %s last_task_number = %d, want %d: %v", project, last, n, err)
		}
	}
	var event, node, created, message string
	if err := d.QueryRow(ctx, `SELECT data FROM task_events WHERE type='task.updated'`).Scan(&event); err != nil || event != fmt.Sprintf(`{"task":{"id":%q,"title":"t","number":3}}`, c) {
		t.Errorf("task.updated events should get the number: %s %v", event, err)
	}
	if err := d.QueryRow(ctx, `SELECT data FROM task_events WHERE type='node.updated'`).Scan(&node); err != nil || node != `{"node":{"id":"n"}}` {
		t.Errorf("other events should not change: %s %v", node, err)
	}
	if err := d.QueryRow(ctx, `SELECT response FROM task_actions WHERE action_id='create'`).Scan(&created); err != nil || created != fmt.Sprintf(`{"task":{"id":%q,"number":2},"nodes":[]}`, b) {
		t.Errorf("recorded task details should get the number: %s %v", created, err)
	}
	if err := d.QueryRow(ctx, `SELECT response FROM task_actions WHERE action_id='message'`).Scan(&message); err != nil || message != `{"messageId":"m1"}` {
		t.Errorf("responses without a task should not change: %s %v", message, err)
	}

	insert := func(number string) error {
		id := uuid.New()
		_, err := d.Exec(ctx, `INSERT INTO task_records(id,project_id,status,document,version) VALUES($1,$2,'running',$3,1)`, id, defaults.Project.ID,
			fmt.Sprintf(`{"view":{"task":{"id":%q,"status":"running","version":1%s}}}`, id, number))
		return err
	}
	if err := insert(""); err == nil || !strings.Contains(err.Error(), "NOT NULL constraint failed: task_records.number") {
		t.Errorf("a task without a number should be rejected: %v", err)
	}
	if err := insert(`,"number":0`); err == nil || !strings.Contains(err.Error(), "CHECK constraint failed: number > 0") {
		t.Errorf("number 0 should be rejected: %v", err)
	}
	if err := insert(`,"number":2`); !db.IsUniqueViolation(err, "task_records", "project_id", "number") {
		t.Errorf("a duplicate number in the same project should be rejected: %v", err)
	}
	if err := insert(`,"number":4`); err != nil {
		t.Errorf("a new number should be writable: %v", err)
	}
	if _, err := d.Exec(ctx, `UPDATE task_records SET document=json_set(document,'$.view.task.number',9) WHERE id=$1`, a); !db.IsRaise(err, "task_number_is_immutable") {
		t.Errorf("the number must not change: %v", err)
	}
	if _, err := d.Exec(ctx, `UPDATE task_records SET document=json_set(document,'$.view.task.title','renamed') WHERE id=$1`, a); err != nil {
		t.Errorf("changing other parts of the document should still work: %v", err)
	}
}

// v12 adds the pending handoff of a change of runtime (agent_sessions.handoff) and the run's selection and carried
// handoff (runs.selection, runs.context_handoff): existing sessions and runs read them as empty, and only JSON objects
// are accepted.
func TestContextHandoffMigration(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := schema.MigrateTo(ctx, d, log, 11); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	defaults, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := oldAgent(ctx, d, defaults.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Version 11 data is written directly in SQL: the current store reads the new columns.
	runID := uuid.New()
	if _, err := d.Exec(ctx, `UPDATE agent_sessions SET session_file = '/s.jsonl', state = 'active' WHERE agent_id = $1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin, queued_at, started_at, ended_at)
		VALUES ($1, $2, $3, 1, 1, 1, 'completed', 'console', now(), now(), now())`, runID, agent.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	sess, err := st.GetAgentSession(ctx, agent.ID)
	if err != nil || sess.State != "active" || sess.SessionFile != "/s.jsonl" || sess.Handoff != nil {
		t.Fatalf("an existing session has no pending handoff: %+v %v", sess, err)
	}
	run, err := st.GetRun(ctx, runID)
	if err != nil || run.SelectionInfo() != nil || run.HandoffInfo() != nil {
		t.Fatalf("an existing run has no selection or handoff: %+v %v", run, err)
	}
	for _, q := range []string{
		`UPDATE agent_sessions SET handoff = '[1]' WHERE agent_id = $1`,
		`UPDATE runs SET selection = '"pi"' WHERE agent_id = $1`,
		`UPDATE runs SET context_handoff = '1' WHERE agent_id = $1`,
	} {
		if _, err := d.Exec(ctx, q, agent.ID); err == nil {
			t.Fatalf("a value that is not a JSON object must be rejected: %s", q)
		}
	}
}

// v14 rebuilds run_tool_events in events.db so a tool can be closed as no_result: every row written before stays as it
// was, in order, and states outside the four are still refused.
func TestToolNoResultMigration(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := schema.MigrateTo(ctx, d, log, 13); err != nil {
		t.Fatal(err)
	}
	insert := func(run uuid.UUID, seq int, state string) error {
		_, err := d.Exec(ctx, `INSERT INTO ev.run_tool_events (run_id, seq, tool_call_id, tool_name, state, at) VALUES ($1, $2, $3, 'execute', $4, $5)`,
			run, seq, fmt.Sprintf("call-%d", seq), state, int64(1700000000000+seq))
		return err
	}
	runs := []uuid.UUID{uuid.New(), uuid.New()}
	for i, state := range []string{"started", "succeeded", "started", "failed", "started"} {
		if err := insert(runs[i%2], i+1, state); err != nil {
			t.Fatal(err)
		}
	}
	if err := insert(runs[0], 99, "no_result"); err == nil {
		t.Fatal("version 13 refuses no_result")
	}
	dump := func() string {
		rows, err := d.Query(ctx, `SELECT run_id || ':' || seq || ':' || tool_call_id || ':' || tool_name || ':' || state || ':' || at FROM ev.run_tool_events ORDER BY run_id, seq`)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		var row string
		if _, err := db.ForEachRow(rows, []any{&row}, func() error { out = append(out, row); return nil }); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	before := dump()
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	if after := dump(); after != before || strings.Count(after, "\n") != 4 {
		t.Fatalf("rows changed:\n%s\nwant:\n%s", after, before)
	}
	if err := insert(runs[0], 6, "no_result"); err != nil {
		t.Fatalf("no_result is accepted: %v", err)
	}
	if err := insert(runs[0], 7, "lost"); err == nil {
		t.Fatal("an unknown state is refused")
	}
	if ti, ok := tables(t, d)["run_tool_events"]; !ok || ti.schema != db.EventsSchema || !ti.strict {
		t.Fatalf("run_tool_events stays a STRICT table in events.db: %+v", ti)
	}
}

// v16 lets a run come from the runtime's own follow-up after background work ended (origin background, end reason
// merged_into_reply) and keeps background tasks in a table of their own, removed with the run or task turn that started
// them; existing runs and their messages are kept.
func TestBackgroundTasksMigration(t *testing.T) {
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	d, err := db.Open(ctx, db.Options{Dir: t.TempDir(), Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := schema.MigrateTo(ctx, d, log, 15); err != nil {
		t.Fatal(err)
	}
	st := store.New(d)
	defaults, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := oldAgent(ctx, d, defaults.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	runID, userID := uuid.New(), uuid.New()
	selection := `{"runtime":"claude-code","provider":"claude-code","modelId":"m","thinkingLevel":null}`
	if _, err := d.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin, queued_at, started_at, ended_at, end_reason, selection)
		VALUES ($1, $2, $3, 1, 1, 1, 'interrupted', 'console', now(), now(), now(), 'user_stopped', $4)`, runID, agent.ID, userID, selection); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO conversation_messages (id, agent_id, seq, role, text, run_id, session_epoch) VALUES ($1, $2, 1, 'user', 'hi', $3, 1)`, userID, agent.ID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin) VALUES ($1, $2, $3, 1, 0, 2, 'queued', 'background')`, uuid.New(), agent.ID, uuid.New()); err == nil {
		t.Fatal("version 15 refuses origin background")
	}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetRun(ctx, runID)
	if err != nil || got.Status != "interrupted" || got.EndReason != "user_stopped" || got.UserMessageID != userID || got.Selection == nil {
		t.Fatalf("the run should be kept: %+v %v", got, err)
	}
	follow := uuid.New()
	if _, err := d.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status, origin, end_reason, ended_at) VALUES ($1, $2, $3, 1, 0, 2, 'interrupted', 'background', 'merged_into_reply', now())`, follow, agent.ID, uuid.New()); err != nil {
		t.Fatalf("origin background and end reason merged_into_reply are accepted: %v", err)
	}
	insertTask := func(id uuid.UUID, run any, status string, ended any) error {
		_, err := d.Exec(ctx, `INSERT INTO background_tasks (id, agent_id, run_id, native_id, runtime, kind, label, started_at, status, ended_at, follow_up_run_id)
			VALUES ($1, $2, $3, 'b1', 'claude-code', 'command', 'sleep 20', now(), $4, $5, $6)`, id, agent.ID, run, status, ended, follow)
		return err
	}
	task := uuid.New()
	if err := insertTask(task, runID, "completed", int64(1700000000000)); err != nil {
		t.Fatal(err)
	}
	if err := insertTask(uuid.New(), nil, "running", nil); err == nil {
		t.Fatal("a background task belongs to a run or a task turn")
	}
	if err := insertTask(uuid.New(), runID, "running", int64(1700000000000)); err == nil {
		t.Fatal("a running task has no end")
	}
	if err := insertTask(uuid.New(), runID, "lost", int64(1700000000000)); err == nil {
		t.Fatal("an unknown status is refused")
	}
	if _, err := d.Exec(ctx, `DELETE FROM runs WHERE id = $1`, follow); err != nil {
		t.Fatal(err)
	}
	var followed *string
	if err := d.QueryRow(ctx, `SELECT follow_up_run_id FROM background_tasks WHERE id = $1`, task).Scan(&followed); err != nil || followed != nil {
		t.Fatalf("deleting the follow-up run clears the link: %v %v", followed, err)
	}
	if _, err := d.Exec(ctx, `DELETE FROM conversation_messages WHERE run_id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `DELETE FROM runs WHERE id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT count(*) FROM background_tasks`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a run's background tasks go with it: %d %v", n, err)
	}
	rows, err := d.Query(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("there should be no foreign key errors after the migration")
	}
}

// oldAgent creates an Agent and its session row the way any schema version takes them (today's store reads columns an
// older schema lacks).
func oldAgent(ctx context.Context, d *db.DB, projectID uuid.UUID) (struct{ ID uuid.UUID }, error) {
	a := struct{ ID uuid.UUID }{ID: uuid.Must(uuid.NewV7())}
	if _, err := d.Exec(ctx, `INSERT INTO agents (id, project_id, name, model_provider, model_id, prompt) VALUES ($1, $2, 'a', 'anthropic', 'm', '')`, a.ID, projectID); err != nil {
		return a, err
	}
	_, err := d.Exec(ctx, `INSERT INTO agent_sessions (agent_id) VALUES ($1)`, a.ID)
	return a, err
}
