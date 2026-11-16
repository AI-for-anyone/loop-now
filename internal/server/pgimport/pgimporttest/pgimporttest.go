// Package pgimporttest provides isolated PostgreSQL source databases for the importer's tests: each call creates a
// separate schema in the test database and runs all PostgreSQL migrations, and the schema is dropped when the test
// ends. It only touches the database LOOP_NOW_TEST_DATABASE_URL points to; when that is unset, the tests are skipped.
package pgimporttest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/pgimport/pgschema"
	"loop-now/internal/testutil/testenv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jackc/pgx/v5/stdlib"
)

// EnvTestDatabaseURL points to the test database.
const EnvTestDatabaseURL = "LOOP_NOW_TEST_DATABASE_URL"

const (
	schemaPrefix = "t_"
	staleAfter   = 24 * time.Hour
)

var sweepOnce sync.Once

// URL returns the test database connection string; when it is not configured, it skips the test and says why.
func URL(t testing.TB) string {
	t.Helper()
	u := testenv.Get(EnvTestDatabaseURL)
	if u == "" {
		t.Skipf("%s is not set (and there is no .env.local); skipping tests that need PostgreSQL", EnvTestDatabaseURL)
	}
	return u
}

// NewSchema creates a separate schema and returns its name; it is dropped when the test ends.
func NewSchema(t testing.TB) string {
	t.Helper()
	url := URL(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer conn.Close(ctx)

	sweepOnce.Do(func() { sweepStale(ctx, t, conn) })

	var b [4]byte
	_, _ = rand.Read(b[:])
	name := fmt.Sprintf("%s%d_%s", schemaPrefix, time.Now().Unix(), hex.EncodeToString(b[:]))
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create the test schema: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), url)
		if err != nil {
			t.Errorf("connect to clean up the test schema: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{name}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop test schema %s: %v", name, err)
		}
	})
	return name
}

// NewMigratedPool creates a separate schema, runs all PostgreSQL migrations and returns a pool connected to that
// schema.
func NewMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	schema := NewSchema(t)
	cfg, err := pgxpool.ParseConfig(URL(t))
	if err != nil {
		t.Fatalf("parse the test database URL: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if err := pgschema.Migrate(ctx, sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate the test schema: %v", err)
	}
	return pool
}

// sweepStale drops leftover test schemas older than 24 hours (only names matching t_<unix>_<hex>).
func sweepStale(ctx context.Context, t testing.TB, conn *pgx.Conn) {
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 't\_%'`)
	if err != nil {
		t.Logf("list leftover test schemas: %v", err)
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
			t.Logf("drop leftover test schema %s: %v", n, err)
		}
	}
}

// Source returns a separate source database migrated to the latest PostgreSQL version: a pool (for writing test
// data) and a connection string pointing to that schema (for the importer).
func Source(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	pool := NewMigratedPool(t)
	u := URL(t)
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return pool, u + sep + "search_path=" + url.QueryEscape(pool.Config().ConnConfig.RuntimeParams["search_path"])
}
