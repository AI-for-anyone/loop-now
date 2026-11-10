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

// events.db 里只放这四张可清理的日志表，加上 lifecycle_events 的插件事件捕获表。
var eventsTables = []string{"audit_log", "lifecycle_events", "plugin_host_log", "plugin_lifecycle_changes", "run_tool_events"}

// 不属于业务结构的表。
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
			t.Errorf("%s 不是 STRICT 表", name)
		}
		if ti.schema == db.EventsSchema {
			inEvents = append(inEvents, name)
		}
	}
	slices.Sort(inEvents)
	if !slices.Equal(inEvents, eventsTables) {
		t.Errorf("events.db 的表 = %v，应为 %v", inEvents, eventsTables)
	}
	// 基线 90 张（PG 的 87 张加 sequences 与两张插件事件捕获表），迁移 2 加本机模式的 3 张。
	if len(ts) != 93 {
		t.Errorf("业务表共 %d 张，应为 93", len(ts))
	}
	for _, name := range eventsTables {
		var n int
		if err := d.QueryRow(ctx, `SELECT count(*) FROM pragma_foreign_key_list(?, ?)`, name, db.EventsSchema).Scan(&n); err != nil || n != 0 {
			t.Errorf("events.db 的 %s 不应有外键：%d %v", name, n, err)
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
			t.Errorf("%s foreign_key_check 应为空", s)
		}
		rows.Close()
	}
	var dirs, git, cli, logins bool
	if err := d.QueryRow(ctx, `SELECT directories, git, cli, logins FROM local_mode_settings`).Scan(&dirs, &git, &cli, &logins); err != nil || !dirs || !git || !cli || logins {
		t.Errorf("本机模式开关：目录、git、CLI 应默认开启，登录状态默认关闭：%v %v %v %v %v", dirs, git, cli, logins, err)
	}
	var seqs string
	if err := d.QueryRow(ctx, `SELECT group_concat(name || '=' || value, ',') FROM (SELECT * FROM sequences ORDER BY name)`).Scan(&seqs); err != nil ||
		seqs != "conversation_messages.seq=0,runs.queue_seq=0,task_turns.queue_seq=0" {
		t.Errorf("sequences: %q %v", seqs, err)
	}
	// 再迁移一次什么都不做。
	if err := schema.Migrate(ctx, d, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if v, err := schema.Version(ctx, d); err != nil || v != latestVersion {
		t.Fatalf("版本 = %d %v，应为 %d", v, err, latestVersion)
	}
}

// latestVersion 是最后一条迁移的版本号；新增迁移时同步修改。
const latestVersion = 5

// TestMigrateToBaseline：导入旧库时先只迁到基线（没有基线之后新增的表），再迁到最新。
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
		t.Fatalf("版本 = %d %v，应为 %d", v, err, schema.Baseline)
	}
	if ts := tables(t, d); len(ts) != 90 {
		t.Fatalf("基线业务表 %d 张，应为 90", len(ts))
	}
	// 迁移 2 重建 agent_folder_git_pulls（失败码新增两个）：已有记录原样保留。
	st := store.New(d)
	dc, err := st.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.CreateAgent(ctx, store.ProjectScope(dc.Project.ID), "a", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	pull := func(path string) uuid.UUID {
		t.Helper()
		p, created, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: uuid.New(), URL: "https://example.com/r.git", Branch: "main", Path: path})
		if err != nil || !created {
			t.Fatal(err)
		}
		if ok, err := st.StartFolderGitPull(ctx, p.ID); err != nil || !ok {
			t.Fatal(err)
		}
		return p.ID
	}
	old, queued := pull("old"), pull("queued")
	if err := st.FinishFolderGitPull(ctx, old, "clone_failed", "原来的失败"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishFolderGitPull(ctx, queued, "auth_failed", "x"); err == nil {
		t.Fatal("基线不应接受 auth_failed")
	}
	if err := schema.Migrate(ctx, d, log); err != nil {
		t.Fatal(err)
	}
	if v, err := schema.Version(ctx, d); err != nil || v != latestVersion {
		t.Fatalf("版本 = %d %v，应为 %d", v, err, latestVersion)
	}
	if got, err := st.GetFolderGitPull(ctx, a.ID, old); err != nil || got.Status != "failed" || *got.ErrorCode != "clone_failed" || *got.ErrorMessage != "原来的失败" {
		t.Fatalf("重建后旧记录：%+v %v", got, err)
	}
	if got, err := st.GetFolderGitPull(ctx, a.ID, queued); err != nil || got.Status != "running" {
		t.Fatalf("重建后进行中的记录：%+v %v", got, err)
	}
	for _, code := range []string{"auth_failed", "host_key_unverified"} {
		if err := st.FinishFolderGitPull(ctx, pull(code), code, "x"); err != nil {
			t.Fatalf("迁移后应接受 %s：%v", code, err)
		}
	}
	// 进行中的唯一索引仍在：同一路径不能有两个进行中的拉取。
	if _, _, err := st.CreateFolderGitPull(ctx, store.FolderGitPull{ID: uuid.Must(uuid.NewV7()), AgentID: a.ID, ActionID: uuid.New(), URL: "https://example.com/r.git", Branch: "main", Path: "queued"}); err == nil {
		t.Fatal("同一路径的第二个进行中拉取应被唯一索引拒绝")
	}
	var fk string
	if err := d.QueryRow(ctx, `SELECT count(*) FROM pragma_foreign_key_list('agent_folder_git_pulls')`).Scan(&fk); err != nil || fk != "1" {
		t.Fatalf("外键应保留：%q %v", fk, err)
	}
}

// 13 个正则 CHECK 换成了 GLOB；这里取出约束里由正则换来的那一段，用同样的正则（Go 与 PG 对这些模式
// 语义相同）逐个比对。有的约束还带别的条件（如名字不能是 console），只比正则那一段。
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

// regexFragment 从建表语句里名为 name 的约束中取出由正则换来的那一段：以 "(length(列)" 或 "(列 GLOB" 开头的括号。
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

// balanced 返回以左括号开头、到与之配对的右括号为止的前缀。
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
		t.Fatal("基线里没有 GLOB 检查")
	}
	r := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._- é\n")
	for name, c := range regexChecks {
		expr, ok := regexFragment(ddl, name, c.column)
		if !ok {
			// 名字对不上说明 PG 侧改了约束：先修正这张表。
			t.Errorf("基线里找不到约束 %s 中由正则换来的部分", name)
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
				t.Errorf("%s(%q) = %v，PG 正则 %s 为 %v", name, s, got, c.pattern, want)
			}
		}
	}
}

// rawConn 打开迁移后的 data.db 的一个独立连接：关外键、忽略 CHECK，便于只填 NOT NULL 列造数据。
// rawDriver 是只给 rawConn 用的驱动：表的时间默认值调用 now()，这里按调用时刻提供它。
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

// insertDummy 给表插一行：overrides 之外的 NOT NULL 且没有默认值的列按类型填占位值。
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

	// 飞书凭证绑定只追加。
	insertDummy(t, raw, "task_lark_credential_bindings", map[string]any{"task_id": "t1", "revision": 1})
	insertDummy(t, raw, "task_lark_binding_members", map[string]any{"task_id": "t1", "revision": 1})
	for _, q := range []string{
		`UPDATE task_lark_credential_bindings SET revision = 2`,
		`DELETE FROM task_lark_credential_bindings`,
		`UPDATE task_lark_binding_members SET revision = 2`,
		`DELETE FROM task_lark_binding_members`,
	} {
		if _, err := raw.Exec(q); !db.IsRaise(err, "task_lark_binding_is_append_only") {
			t.Errorf("%s 应被拒绝，得到 %v", q, err)
		}
	}

	// 列级 SET NULL：删版本时只把 live_version_id 置空，外键开着也能删。
	insertDummy(t, raw, "project_cli_versions", map[string]any{"id": "v1", "account_id": "a1", "cli_name": "gh", "auth_method": "oauth"})
	insertDummy(t, raw, "cli_credential_verifications", map[string]any{"id": "c1", "account_id": "a1", "version_id": "v0", "live_version_id": "v1", "cli_name": "gh", "auth_method": "oauth"})
	insertDummy(t, raw, "cli_credential_import_receipts", map[string]any{"project_id": "p1", "provenance_key_id": "k", "origin_mac": []byte{1}, "account_id": "a1", "version_id": "v0", "live_version_id": "v1"})
	if _, err := raw.Exec(`PRAGMA foreign_keys = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM project_cli_versions WHERE id = 'v1'`); err != nil {
		t.Fatalf("删除被引用的版本应由触发器先置空引用：%v", err)
	}
	var live1, live2 sql.NullString
	var account string
	if err := raw.QueryRow(`SELECT v.live_version_id, v.account_id, r.live_version_id FROM cli_credential_verifications v, cli_credential_import_receipts r`).Scan(&live1, &account, &live2); err != nil {
		t.Fatal(err)
	}
	if live1.Valid || live2.Valid || account != "a1" {
		t.Errorf("应只置空 live_version_id：%v %q %v", live1, account, live2)
	}
	if _, err := raw.Exec(`PRAGMA foreign_keys = 0`); err != nil {
		t.Fatal(err)
	}

	// UNIQUE NULLS NOT DISTINCT：project_id 为 NULL 的两行视为重复。
	insertDummy(t, raw, "plugin_state", map[string]any{"plugin_id": "p", "project_id": nil, "key": "k"})
	_, err := raw.Exec(`INSERT INTO plugin_state (plugin_id, project_id, key, value) VALUES ('p', NULL, 'k', 'x')`)
	if !db.IsUniqueIndexViolation(err, "plugin_state_plugin_id_project_id_key_key") {
		t.Errorf("NULL 项目的重复键应冲突：%v", err)
	}
	if _, err := raw.Exec(`INSERT INTO plugin_state (plugin_id, project_id, key, value) VALUES ('p', 'proj', 'k', 'x')
		ON CONFLICT (plugin_id, coalesce(project_id, ''), key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Errorf("表达式冲突目标应可用：%v", err)
	}

	// stop_requested 默认 0。
	for _, table := range []string{"runs", "task_turns"} {
		var dflt string
		if err := raw.QueryRow(`SELECT dflt_value FROM pragma_table_info(?) WHERE name = 'stop_requested'`, table).Scan(&dflt); err != nil || dflt != "0" {
			t.Errorf("%s.stop_requested 默认值：%q %v", table, dflt, err)
		}
	}
}
