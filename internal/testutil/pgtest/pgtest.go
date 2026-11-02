// Package pgtest 为迁移到 SQLite 期间的 parity 与导入测试提供隔离的 PostgreSQL 环境：
// 每次调用在测试库 loop_now_test 中创建独立 schema，测试结束后删除。
// 只操作 LOOP_NOW_TEST_DATABASE_URL 指向的数据库，不触碰其他库。
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/store/pgschema"
	"loop-now/internal/testutil/testenv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5/stdlib"
)

// EnvTestDatabaseURL 指向测试库。
const EnvTestDatabaseURL = "LOOP_NOW_TEST_DATABASE_URL"

const (
	schemaPrefix = "t_"
	staleAfter   = 24 * time.Hour
)

var sweepOnce sync.Once

// URL 返回测试库连接串；未配置时跳过测试并说明原因。
func URL(t testing.TB) string {
	t.Helper()
	u := testenv.Get(EnvTestDatabaseURL)
	if u == "" {
		t.Skipf("未设置 %s（也没有 .env.local），跳过需要 PostgreSQL 的测试", EnvTestDatabaseURL)
	}
	return u
}

// NewSchema 创建独立 schema 并返回其名称，测试结束时删除。
func NewSchema(t testing.TB) string {
	t.Helper()
	url := URL(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("连接测试库失败：%v", err)
	}
	defer conn.Close(ctx)

	sweepOnce.Do(func() { sweepStale(ctx, t, conn) })

	var b [4]byte
	_, _ = rand.Read(b[:])
	name := fmt.Sprintf("%s%d_%s", schemaPrefix, time.Now().Unix(), hex.EncodeToString(b[:]))
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("创建测试 schema 失败：%v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), url)
		if err != nil {
			t.Errorf("清理测试 schema 时连接失败：%v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("删除测试 schema %s 失败：%v", name, err)
		}
	})
	return name
}

// NewMigratedPool 创建独立 schema、执行全部 PostgreSQL 迁移，并返回连接到该 schema 的连接池。
// 迁移到 SQLite 期间只给 parity 测试与导入器测试使用。
func NewMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	schema := NewSchema(t)
	cfg, err := pgxpool.ParseConfig(URL(t))
	if err != nil {
		t.Fatalf("解析测试库连接串失败：%v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("打开测试库失败：%v", err)
	}
	t.Cleanup(pool.Close)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if err := pgschema.Migrate(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("迁移测试 schema 失败：%v", err)
	}
	return pool
}

// sweepStale 删除超过 24 小时的遗留测试 schema（只匹配 t_<unix>_<hex> 格式）。
func sweepStale(ctx context.Context, t testing.TB, conn *pgx.Conn) {
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 't\_%'`)
	if err != nil {
		t.Logf("查询遗留测试 schema 失败：%v", err)
		return
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return
	}
	for _, n := range names {
		parts := strings.SplitN(strings.TrimPrefix(n, schemaPrefix), "_", 2)
		if len(parts) != 2 {
			continue
		}
		sec, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || time.Since(time.Unix(sec, 0)) < staleAfter {
			continue
		}
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{n}.Sanitize()+" CASCADE"); err != nil {
			t.Logf("删除遗留测试 schema %s 失败：%v", n, err)
		}
	}
}
