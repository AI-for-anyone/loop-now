// Package pgimport imports a database from the PostgreSQL era into a new SQLite data directory in one go
// (loop-server import-postgres).
//
// The source database must already have been migrated to pgschema.Latest by the last PostgreSQL release, and the
// server must be stopped; the import reads from the source in one repeatable-read, read-only transaction and writes
// the target in one write transaction, with foreign keys deferred and checked as a whole at commit. The target
// directory must not already contain a database: the import only creates new databases, deletes the files it created
// on failure, and never changes the source database.
package pgimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"loop-now/internal/server/db"
	"loop-now/internal/server/pgimport/pgschema"
	"loop-now/internal/server/store"
	"loop-now/internal/server/store/schema"
)

// EnvSourceURL is the environment variable holding the source connection string: the same one the server read in the
// PostgreSQL era.
const EnvSourceURL = "LOOP_NOW_DATABASE_URL"

// Where the baseline deliberately differs from PG (consistent with the parity test in the schema package): these
// exist only in SQLite and are left at their defaults or empty on import.
var (
	sqliteOnlyTables  = []string{"sequences", "plugin_changes", "plugin_lifecycle_changes"}
	sqliteOnlyColumns = map[string][]string{"runs": {"stop_requested"}, "task_turns": {"stop_requested"}}
)

// Options is the input of one import.
type Options struct {
	SourceURL string // PostgreSQL connection string; never appears in logs or errors
	DBDir     string // target database directory (config.DBDir(data directory))
	Log       *slog.Logger
}

// Table is the number of rows imported for one table.
type Table struct {
	Name string
	Rows int64
}

// Report is the import result: the source database version and each table's row count (only counts, no row
// contents at all).
type Report struct {
	Version int64
	Tables  []Table
}

// Import runs the import.
func Import(ctx context.Context, o Options) (_ Report, err error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(o.DBDir, 0o700); err != nil {
		return Report{}, err
	}
	lock, err := db.LockDir(o.DBDir)
	if err != nil {
		if errors.Is(err, db.ErrLocked) {
			return Report{}, fmt.Errorf("库目录 %s 正被服务端使用：先停掉服务再导入", o.DBDir)
		}
		return Report{}, err
	}
	defer lock.Release()
	for _, name := range []string{db.DataFile, db.EventsFile} {
		if _, err := os.Stat(filepath.Join(o.DBDir, name)); err == nil {
			return Report{}, fmt.Errorf("库目录 %s 里已经有 %s：导入只写新库，不覆盖已有数据", o.DBDir, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Report{}, err
		}
	}

	src, err := pgx.Connect(ctx, o.SourceURL)
	if err != nil {
		return Report{}, fmt.Errorf("连接 PostgreSQL 失败：%w", connectCause(err))
	}
	defer src.Close(context.WithoutCancel(ctx))
	// Repeatable read: the whole import sees one snapshot, so counts and data agree.
	stx, err := src.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, fmt.Errorf("开始读取 PostgreSQL 失败：%w", err)
	}
	defer stx.Rollback(context.WithoutCancel(ctx))
	version, err := sourceVersion(ctx, stx)
	if err != nil {
		return Report{}, err
	}
	if version != pgschema.Latest {
		return Report{}, fmt.Errorf("源库的迁移版本是 %d，导入要求 %d：先用最后一个 PostgreSQL 版本启动一次完成迁移、停掉服务，再导入", version, pgschema.Latest)
	}

	st, err := store.Open(ctx, db.Options{Dir: o.DBDir, Logger: o.Log})
	if err != nil {
		return Report{}, err
	}
	// The database files were created by this run (we confirmed at the start that none existed): delete them all on
	// failure.
	defer func() {
		st.Close()
		if err != nil {
			removeDB(o.DBDir)
		}
	}()
	// First migrate only to the SQLite baseline (which matches the final structure of the PG source), and run the
	// migrations after the baseline once the import is done and verified: tables and columns added by later
	// migrations do not exist in the source, so migrating to the latest version before comparing structures would
	// reject the import.
	if err = st.MigrateTo(ctx, o.Log, schema.Baseline); err != nil {
		return Report{}, err
	}
	targets, err := targetTables(ctx, st.Pool)
	if err != nil {
		return Report{}, err
	}
	sources, err := sourceColumns(ctx, stx)
	if err != nil {
		return Report{}, err
	}
	plans, err := plan(targets, sources)
	if err != nil {
		return Report{}, err
	}

	report := Report{Version: version}
	err = st.Pool.InTx(ctx, func(tx *db.Tx) error {
		// Foreign keys are deferred to commit: tables can be written in any order and every constraint still applies.
		if _, err := tx.Exec(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return err
		}
		for _, p := range plans {
			// Seed rows written when the baseline created the database (singleton settings) are replaced by the source's
			// rows.
			if _, err := tx.Exec(ctx, `DELETE FROM `+p.target()); err != nil {
				return fmt.Errorf("清空 %s：%w", p.table, err)
			}
			n, err := copyTable(ctx, stx, tx, p)
			if err != nil {
				return fmt.Errorf("导入 %s：%w", p.table, err)
			}
			o.Log.Info("已导入", "table", p.table, "rows", n)
			report.Tables = append(report.Tables, Table{Name: p.table, Rows: n})
		}
		if err := setSequences(ctx, stx, tx); err != nil {
			return err
		}
		// The plugin capture triggers recorded every imported row as a change; these are not new events, so clear them
		// before commit (the source's plugin events were imported as they are).
		for _, t := range []string{"plugin_changes", db.EventsSchema + ".plugin_lifecycle_changes"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	if err = verify(ctx, st.Pool, plans, report); err != nil {
		return Report{}, err
	}
	if err = st.Migrate(ctx, o.Log); err != nil {
		return Report{}, err
	}
	if _, err = st.Pool.Checkpoint(ctx, db.CheckpointTruncate); err != nil {
		return Report{}, err
	}
	if err = st.Pool.Optimize(ctx); err != nil {
		return Report{}, err
	}
	return report, nil
}

// connectCause strips the connection parameters (host, user, database name) carried in a pgx connection error and
// keeps only the cause.
func connectCause(err error) error {
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		if cause := errors.Unwrap(ce); cause != nil {
			return cause
		}
		return errors.New("无法连接")
	}
	return err
}

func removeDB(dir string) {
	for _, name := range []string{db.DataFile, db.EventsFile} {
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			_ = os.Remove(filepath.Join(dir, name+suffix))
		}
	}
}

// sourceVersion reads the version of the last record in the source's goose table; if the last record is a rollback,
// the source is treated as incomplete.
func sourceVersion(ctx context.Context, tx pgx.Tx) (int64, error) {
	var version int64
	var applied bool
	err := tx.QueryRow(ctx, `SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC LIMIT 1`).Scan(&version, &applied)
	if err != nil {
		return 0, fmt.Errorf("读取源库的迁移版本失败（不是 Loop Now 的 PostgreSQL 库？）：%w", err)
	}
	if !applied {
		return 0, fmt.Errorf("源库最后一次迁移操作是回退到 %d 之前：先用最后一个 PostgreSQL 版本完成迁移", version)
	}
	return version, nil
}

type targetColumn struct {
	name, typ string
}

type targetTable struct {
	schema, name string
	columns      []targetColumn
}

// targetTables lists the tables and columns to import in the new database (excluding generated columns and
// SQLite-only tables).
func targetTables(ctx context.Context, d *db.DB) ([]targetTable, error) {
	var out []targetTable
	for _, schema := range []string{"main", db.EventsSchema} {
		rows, err := d.Query(ctx, fmt.Sprintf(`SELECT name FROM %s.sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite%%' AND name NOT LIKE 'goose%%' ORDER BY name`, schema))
		if err != nil {
			return nil, err
		}
		names, err := db.CollectRows(rows, db.RowTo[string])
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if slices.Contains(sqliteOnlyTables, name) {
				continue
			}
			t := targetTable{schema: schema, name: name}
			rows, err := d.Query(ctx, `SELECT name, type, hidden FROM pragma_table_xinfo(?, ?) ORDER BY cid`, name, schema)
			if err != nil {
				return nil, err
			}
			var c targetColumn
			var hidden int64
			if _, err := db.ForEachRow(rows, []any{&c.name, &c.typ, &hidden}, func() error {
				if hidden == 0 && !slices.Contains(sqliteOnlyColumns[name], c.name) {
					t.columns = append(t.columns, c)
				}
				return nil
			}); err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// sourceColumns returns the non-generated columns of every source table with their types (data_type from
// information_schema).
func sourceColumns(ctx context.Context, tx pgx.Tx) (map[string]map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE' AND c.table_name NOT LIKE 'goose%' AND c.is_generated = 'NEVER'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var table, column, typ string
		if err := rows.Scan(&table, &column, &typ); err != nil {
			return nil, err
		}
		if out[table] == nil {
			out[table] = map[string]string{}
		}
		out[table][column] = typ
	}
	return out, rows.Err()
}

type tablePlan struct {
	schema, table string
	columns       []string
	selects       []string // per-column select expression in PG, whose result is already in SQLite storage form
}

func (p tablePlan) target() string {
	if p.schema == db.EventsSchema {
		return db.EventsSchema + "." + p.table
	}
	return p.table
}

// plan checks that tables and columns on both sides match one to one and picks a conversion expression for each
// column; any mismatch rejects the import.
func plan(targets []targetTable, sources map[string]map[string]string) ([]tablePlan, error) {
	var problems []string
	seen := map[string]bool{}
	var out []tablePlan
	for _, t := range targets {
		seen[t.name] = true
		cols, ok := sources[t.name]
		if !ok {
			problems = append(problems, "源库没有表 "+t.name)
			continue
		}
		p := tablePlan{schema: t.schema, table: t.name}
		for _, c := range t.columns {
			pgType, ok := cols[c.name]
			if !ok {
				problems = append(problems, fmt.Sprintf("源库的 %s 没有列 %s", t.name, c.name))
				continue
			}
			expr, err := convert(pgx.Identifier{c.name}.Sanitize(), pgType, c.typ)
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s.%s：%v", t.name, c.name, err))
				continue
			}
			p.columns = append(p.columns, c.name)
			p.selects = append(p.selects, expr)
		}
		for name := range cols {
			if !slices.ContainsFunc(t.columns, func(c targetColumn) bool { return c.name == name }) {
				problems = append(problems, fmt.Sprintf("新库的 %s 没有列 %s", t.name, name))
			}
		}
		out = append(out, p)
	}
	for name := range sources {
		if !seen[name] {
			problems = append(problems, "新库没有表 "+name)
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("源库结构与 SQLite 基线对不上，拒绝导入：\n  %s", strings.Join(problems, "\n  "))
	}
	return out, nil
}

// convert returns the expression that reads a PG column in SQLite storage form: uuid as lowercase text, timestamps as
// UTC microsecond integers, json/jsonb as their text unchanged, arrays as JSON array text, bytea as the raw bytes,
// booleans and integers as they are (converted to integers on write).
func convert(col, pgType, sqliteType string) (string, error) {
	want := map[string]string{
		"uuid": "TEXT", "text": "TEXT", "jsonb": "TEXT", "json": "TEXT", "ARRAY": "TEXT",
		"timestamp with time zone": "INTEGER", "bigint": "INTEGER", "integer": "INTEGER", "smallint": "INTEGER", "boolean": "INTEGER",
		"bytea": "BLOB",
	}[pgType]
	if want == "" {
		return "", fmt.Errorf("不支持的 PostgreSQL 类型 %s", pgType)
	}
	if want != sqliteType {
		return "", fmt.Errorf("类型 %s 应对应 %s，基线里是 %s", pgType, want, sqliteType)
	}
	switch pgType {
	case "uuid", "jsonb", "json":
		return col + "::text", nil
	case "ARRAY":
		return "to_json(" + col + ")::text", nil
	case "timestamp with time zone":
		return "(extract(epoch FROM " + col + ") * 1000000)::bigint", nil
	}
	return col, nil
}

// batchRows is the maximum number of rows per INSERT; it is also bounded by SQLite's limit on parameters per
// statement.
const batchRows = 500

func copyTable(ctx context.Context, src pgx.Tx, dst *db.Tx, p tablePlan) (int64, error) {
	if len(p.columns) == 0 {
		return 0, nil
	}
	rows, err := src.Query(ctx, `SELECT `+strings.Join(p.selects, ", ")+` FROM `+pgx.Identifier{p.table}.Sanitize())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	per := max(1, min(batchRows, 30000/len(p.columns)))
	quoted := make([]string, len(p.columns))
	for i, c := range p.columns {
		quoted[i] = `"` + c + `"`
	}
	head := `INSERT INTO ` + p.target() + ` (` + strings.Join(quoted, ", ") + `) VALUES `
	one := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(p.columns)), ", ") + ")"
	statements := map[int]string{}
	insert := func(args []any) error {
		n := len(args) / len(p.columns)
		q, ok := statements[n]
		if !ok {
			q = head + strings.TrimSuffix(strings.Repeat(one+", ", n), ", ")
			statements[n] = q
		}
		_, err := dst.Exec(ctx, q, args...)
		return err
	}
	var total int64
	args := make([]any, 0, per*len(p.columns))
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return total, err
		}
		for i, v := range values {
			v, err := storable(v)
			if err != nil {
				return total, fmt.Errorf("列 %s：%w", p.columns[i], err)
			}
			args = append(args, v)
		}
		total++
		if len(args) == per*len(p.columns) {
			if err := insert(args); err != nil {
				return total, err
			}
			args = args[:0]
		}
	}
	if err := rows.Err(); err != nil {
		return total, err
	}
	if len(args) > 0 {
		if err := insert(args); err != nil {
			return total, err
		}
	}
	return total, nil
}

// storable converts a value decoded by pgx into a Go type that a SQLite STRICT column accepts; errors never include
// the value itself.
func storable(v any) (any, error) {
	switch x := v.(type) {
	case nil, string, []byte, int64:
		return x, nil
	case int32:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case bool:
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	}
	return nil, fmt.Errorf("意外的值类型 %T", v)
}

// setSequences makes the new database's sequences continue after the point the source reached: it takes the larger
// of the source sequence's current value and the column's maximum value.
func setSequences(ctx context.Context, src pgx.Tx, dst *db.Tx) error {
	rows, err := dst.Query(ctx, `SELECT name FROM sequences ORDER BY name`)
	if err != nil {
		return err
	}
	names, err := db.CollectRows(rows, db.RowTo[string])
	if err != nil {
		return err
	}
	for _, name := range names {
		table, column, ok := strings.Cut(name, ".")
		if !ok {
			return fmt.Errorf("序列名 %q 不是 表.列", name)
		}
		var value int64
		if err := src.QueryRow(ctx, `SELECT coalesce(max(`+pgx.Identifier{column}.Sanitize()+`), 0) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&value); err != nil {
			return fmt.Errorf("读取 %s 的最大值：%w", name, err)
		}
		var seq *string
		if err := src.QueryRow(ctx, `SELECT pg_get_serial_sequence($1, $2)`, table, column).Scan(&seq); err != nil {
			return fmt.Errorf("查找 %s 的序列：%w", name, err)
		}
		if seq != nil {
			var last int64
			var called bool
			if err := src.QueryRow(ctx, `SELECT last_value, is_called FROM `+*seq).Scan(&last, &called); err != nil {
				return fmt.Errorf("读取序列 %s：%w", name, err)
			}
			if called && last > value {
				value = last
			}
		}
		if _, err := dst.Exec(ctx, `UPDATE sequences SET value = ? WHERE name = ?`, value, name); err != nil {
			return err
		}
	}
	return nil
}

// verify re-checks after commit: every table's row count matches what was read, and foreign keys and the database
// files are intact.
func verify(ctx context.Context, d *db.DB, plans []tablePlan, r Report) error {
	for i, p := range plans {
		var n int64
		if err := d.QueryRow(ctx, `SELECT count(*) FROM `+p.target()).Scan(&n); err != nil {
			return err
		}
		if n != r.Tables[i].Rows {
			return fmt.Errorf("%s 导入后有 %d 行，源库读到 %d 行", p.table, n, r.Tables[i].Rows)
		}
	}
	for _, schema := range []string{"main", db.EventsSchema} {
		rows, err := d.Query(ctx, fmt.Sprintf(`PRAGMA %s.foreign_key_check`, schema))
		if err != nil {
			return err
		}
		var bad int
		for rows.Next() {
			bad++
		}
		rows.Close()
		if bad > 0 {
			return fmt.Errorf("%s 里有 %d 处外键不成立", schema, bad)
		}
		var result string
		if err := d.QueryRow(ctx, fmt.Sprintf(`PRAGMA %s.quick_check`, schema)).Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return fmt.Errorf("%s 完整性检查未通过", schema)
		}
	}
	return nil
}
