// Package pgimport 把 PostgreSQL 时期的库一次性导入到新的 SQLite 数据目录（loop-server import-postgres）。
//
// 源库必须已经由最后一个 PostgreSQL 版本迁移到 pgschema.Latest，并且服务已经停下；导入在源库的一个
// 可重复读只读事务里读取，目标库在一个写事务里写入，外键推迟到提交时整体检查。目标库目录里不能已经有库：
// 导入只建新库，失败时删掉本次建的文件，不会改动源库。
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
)

// EnvSourceURL 是源库连接串所在的环境变量：沿用 PostgreSQL 时期服务端读的那个。
const EnvSourceURL = "LOOP_NOW_DATABASE_URL"

// 基线有意与 PG 不同的地方（与 schema 包的 parity 测试一致）：这些只在 SQLite 里有，导入时取默认值或留空。
var (
	sqliteOnlyTables  = []string{"sequences", "plugin_changes", "plugin_lifecycle_changes"}
	sqliteOnlyColumns = map[string][]string{"runs": {"stop_requested"}, "task_turns": {"stop_requested"}}
)

// Options 是一次导入的输入。
type Options struct {
	SourceURL string // PostgreSQL 连接串；不会出现在日志与错误里
	DBDir     string // 目标库目录（config.DBDir(数据目录)）
	Log       *slog.Logger
}

// Table 是一张表导入的行数。
type Table struct {
	Name string
	Rows int64
}

// Report 是导入结果：源库版本与每张表的行数（只有计数，没有任何行内容）。
type Report struct {
	Version int64
	Tables  []Table
}

// Import 执行导入。
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
	// 可重复读：整次导入看到同一个快照，计数与数据一致。
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
	// 库文件是本次建的（开头已确认原来没有）：失败时整个删掉。
	defer func() {
		st.Close()
		if err != nil {
			removeDB(o.DBDir)
		}
	}()
	if err = st.Migrate(ctx, o.Log); err != nil {
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
		// 外键推迟到提交时检查：表可以按任意顺序写，约束照样全部生效。
		if _, err := tx.Exec(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return err
		}
		for _, p := range plans {
			// 基线建库时写入的种子行（单例设置）由源库的行代替。
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
		// 插件捕获触发器把每一行导入都记成了一次变更；这些不是新事件，提交前清掉（源库的插件事件已原样导入）。
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
	if _, err = st.Pool.Checkpoint(ctx, db.CheckpointTruncate); err != nil {
		return Report{}, err
	}
	if err = st.Pool.Optimize(ctx); err != nil {
		return Report{}, err
	}
	return report, nil
}

// connectCause 去掉 pgx 连接错误里带的连接参数（主机、用户、库名），只留下原因。
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

// sourceVersion 读源库 goose 表里最后一条记录的版本；最后一条是回退记录时视为不完整。
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

// targetTables 列出新库里要导入的表与列（不含生成列与 SQLite 独有的表）。
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

// sourceColumns 返回源库每张表的非生成列及其类型（information_schema 的 data_type）。
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
	selects       []string // 每列在 PG 里的取值表达式，结果已是 SQLite 存储的形态
}

func (p tablePlan) target() string {
	if p.schema == db.EventsSchema {
		return db.EventsSchema + "." + p.table
	}
	return p.table
}

// plan 核对两边的表与列一一对应，并为每列选出换写表达式；有任何对不上就拒绝导入。
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

// convert 返回把 PG 列取成 SQLite 存储形态的表达式：uuid 小写文本，时刻 UTC 微秒整数，json/jsonb 原样文本，
// 数组 JSON 数组文本，bytea 原样字节，布尔与整数照取（写入时转成整数）。
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

// batchRows 是每条 INSERT 最多写的行数；同时受 SQLite 每条语句的参数个数上限约束。
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

// storable 把 pgx 解出的值换成 SQLite STRICT 列接受的 Go 类型；错误里不带值本身。
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

// setSequences 让新库的序列从源库用到的位置之后继续：取源序列的当前值与列里最大值中较大的一个。
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

// verify 提交后复核：每张表的行数与读到的一致，外键与库文件完整。
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
