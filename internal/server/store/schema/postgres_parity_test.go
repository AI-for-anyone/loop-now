package schema_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/db"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/sqlitetest"
)

// 迁移期间 main 上的 PostgreSQL 结构还在演进。这个测试把 PG 迁移跑完后的结构与 SQLite 基线逐表、逐列、
// 逐个约束名比对，main 新增或修改 PG 迁移而基线没跟上时会失败（需要 LOOP_NOW_TEST_DATABASE_URL）。
// PG 代码删除时（G14）由导入器的列映射测试接替。

// 基线有意与 PG 不同的地方。
var (
	sqliteOnlyColumns = map[string][]string{"runs": {"stop_requested"}, "task_turns": {"stop_requested"}}
	sqliteOnlyTables  = []string{"sequences"}
	// events.db 不能有指向 data.db 的外键。
	droppedForeignKeys = []string{"lifecycle_events_agent_id_fkey", "lifecycle_events_run_id_fkey", "lifecycle_events_task_turn_id_fkey", "run_tool_events_run_id_fkey"}
	// 自增主键写成 INTEGER PRIMARY KEY，没有约束名。
	inlinePrimaryKeys     = []string{"plugin_host_log_pkey", "task_events_pkey"}
	sqliteOnlyConstraints = []string{"runs_stop_requested_check", "task_turns_stop_requested_check"}
)

func expectedSQLiteType(pgType string) string {
	switch pgType {
	case "uuid", "text", "jsonb", "json", "ARRAY":
		return "TEXT"
	case "timestamp with time zone", "bigint", "integer", "smallint", "boolean":
		return "INTEGER"
	case "bytea":
		return "BLOB"
	}
	return "?" + pgType
}

type column struct {
	name       string
	typ        string
	notNull    bool
	hasDefault bool
	generated  bool
	pk         bool
}

func TestBaselineMatchesPostgres(t *testing.T) {
	pg := pgtest.NewStore(t)
	lite := sqlitetest.NewDB(t)
	ctx := context.Background()

	// ---- PostgreSQL ----
	pgCols := map[string][]column{}
	rows, err := pg.Pool.Query(ctx, `SELECT c.table_name, c.column_name, c.data_type, c.is_nullable = 'NO', c.column_default IS NOT NULL, c.is_generated = 'ALWAYS'
		FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE' AND c.table_name NOT LIKE 'goose%'
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var table string
		var c column
		if err := rows.Scan(&table, &c.name, &c.typ, &c.notNull, &c.hasDefault, &c.generated); err != nil {
			t.Fatal(err)
		}
		pgCols[table] = append(pgCols[table], c)
	}
	rows.Close()
	if len(pgCols) == 0 {
		t.Fatal("PG 侧没有读到表")
	}
	var pgConstraints, pgIndexes []string
	for _, q := range []struct {
		dst *[]string
		sql string
	}{
		// 具名约束：主键、唯一、外键、CHECK（PG 18 的 NOT NULL 约束与约束触发器不算）。
		{&pgConstraints, `SELECT k.conname FROM pg_constraint k JOIN pg_class r ON r.oid = k.conrelid
			WHERE r.relnamespace = current_schema()::regnamespace AND r.relname NOT LIKE 'goose%' AND k.contype IN ('p','u','f','c')
			AND NOT (k.contype = 'u' AND EXISTS (SELECT 1 FROM pg_index i WHERE i.indexrelid = k.conindid AND i.indnullsnotdistinct))`},
		// 独立索引，加上 UNIQUE NULLS NOT DISTINCT（基线里变成表达式唯一索引）。
		{&pgIndexes, `SELECT ic.relname FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_class r ON r.oid = i.indrelid
			WHERE r.relnamespace = current_schema()::regnamespace AND r.relname NOT LIKE 'goose%'
			AND (NOT EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid = i.indexrelid) OR i.indnullsnotdistinct)`},
	} {
		rows, err := pg.Pool.Query(ctx, q.sql)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			*q.dst = append(*q.dst, s)
		}
		rows.Close()
	}

	// ---- SQLite ----
	liteTables := tables(t, lite)
	liteCols := map[string][]column{}
	var liteConstraints, liteIndexes []string
	named := regexp.MustCompile(`CONSTRAINT (\w+) `)
	for table, ti := range liteTables {
		rows, err := lite.Query(ctx, `SELECT name, type, "notnull", dflt_value IS NOT NULL, hidden = 3, pk > 0 FROM pragma_table_xinfo(?, ?) ORDER BY cid`, table, ti.schema)
		if err != nil {
			t.Fatal(err)
		}
		var c column
		if err := db.ForEachRow(rows, []any{&c.name, &c.typ, &c.notNull, &c.hasDefault, &c.generated, &c.pk}, func() error {
			liteCols[table] = append(liteCols[table], c)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, m := range named.FindAllStringSubmatch(ti.sql, -1) {
			liteConstraints = append(liteConstraints, m[1])
		}
	}
	for _, s := range []string{"main", db.EventsSchema} {
		rows, err := lite.Query(ctx, fmt.Sprintf(`SELECT name FROM %s.sqlite_schema WHERE type = 'index' AND sql IS NOT NULL`, s))
		if err != nil {
			t.Fatal(err)
		}
		got, err := db.CollectRows(rows, db.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		liteIndexes = append(liteIndexes, got...)
	}

	// ---- 比对 ----
	for table := range liteTables {
		if _, ok := pgCols[table]; !ok && !slices.Contains(sqliteOnlyTables, table) {
			t.Errorf("基线多出表 %s", table)
		}
	}
	for table, pcs := range pgCols {
		lcs, ok := liteCols[table]
		if !ok {
			t.Errorf("基线缺少表 %s（PG 新增了迁移？）", table)
			continue
		}
		var extra []string
		lcs = slices.DeleteFunc(slices.Clone(lcs), func(c column) bool {
			if slices.Contains(sqliteOnlyColumns[table], c.name) {
				extra = append(extra, c.name)
				return true
			}
			return false
		})
		if !slices.Equal(extra, sqliteOnlyColumns[table]) {
			t.Errorf("%s 的 SQLite 新增列 = %v，应为 %v", table, extra, sqliteOnlyColumns[table])
		}
		if len(lcs) != len(pcs) {
			t.Errorf("%s 列数：PG %d，基线 %d", table, len(pcs), len(lcs))
			continue
		}
		for i, p := range pcs {
			l := lcs[i]
			where := fmt.Sprintf("%s.%s", table, p.name)
			if l.name != p.name {
				t.Errorf("%s 第 %d 列：PG %s，基线 %s（列序须一致，导入按列名对应）", table, i+1, p.name, l.name)
				continue
			}
			if want := expectedSQLiteType(p.typ); l.typ != want {
				t.Errorf("%s 类型：PG %s 应为 %s，基线 %s", where, p.typ, want, l.typ)
			}
			// INTEGER PRIMARY KEY 本身不能为 NULL，pragma 不报 notnull。
			if l.notNull != p.notNull && !(l.pk && l.typ == "INTEGER" && p.notNull) {
				t.Errorf("%s NOT NULL：PG %v，基线 %v", where, p.notNull, l.notNull)
			}
			if l.hasDefault != p.hasDefault {
				t.Errorf("%s 默认值：PG 有=%v，基线有=%v", where, p.hasDefault, l.hasDefault)
			}
			if l.generated != p.generated {
				t.Errorf("%s 生成列：PG %v，基线 %v", where, p.generated, l.generated)
			}
		}
	}
	wantConstraints := slices.DeleteFunc(slices.Clone(pgConstraints), func(n string) bool {
		return slices.Contains(droppedForeignKeys, n) || slices.Contains(inlinePrimaryKeys, n)
	})
	wantConstraints = append(wantConstraints, sqliteOnlyConstraints...)
	diffSets(t, "约束名", wantConstraints, liteConstraints)
	diffSets(t, "索引名", pgIndexes, liteIndexes)
	n := 0
	for _, cs := range pgCols {
		n += len(cs)
	}
	t.Logf("比对了 %d 张表、%d 列、%d 个约束名、%d 个索引名", len(pgCols), n, len(wantConstraints), len(pgIndexes))
}

func diffSets(t *testing.T, what string, want, got []string) {
	t.Helper()
	var missing, extra []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			extra = append(extra, g)
		}
	}
	if len(missing) > 0 {
		t.Errorf("基线缺少%s：%s", what, strings.Join(missing, "、"))
	}
	if len(extra) > 0 {
		t.Errorf("基线多出%s：%s", what, strings.Join(extra, "、"))
	}
}
