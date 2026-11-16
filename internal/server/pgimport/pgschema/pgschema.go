// Package pgschema holds the schema migrations of the PostgreSQL era (001-034, including the Go migration for version
// 12), used only by the importer: the importer uses it to confirm the source version, and the importer's tests use it
// to build source databases. Runtime code does not reference it; it will be deleted together with the importer once
// users have switched to SQLite.
package pgschema

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Latest is the PostgreSQL migration version the SQLite baseline corresponds to.
const Latest = 34

// Migrate runs all PostgreSQL migrations on sqlDB (with the goose table lock).
func Migrate(ctx context.Context, sqlDB *sql.DB, logger *slog.Logger) error {
	locker, err := lock.NewPostgresTableLocker(
		lock.WithTableLockTimeout(500*time.Millisecond, 600),
		lock.WithTableLogger(logger),
	)
	if err != nil {
		return err
	}
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub,
		goose.WithLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(projectCredentialMigration()),
		goose.WithSlog(logger),
	)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("PostgreSQL 迁移失败：%w", err)
	}
	return nil
}
