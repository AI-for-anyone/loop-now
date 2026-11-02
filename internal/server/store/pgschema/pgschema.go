// Package pgschema 是迁移到 SQLite 期间临时保留的 PostgreSQL 表结构迁移（001–034，含版本 12 的 Go 迁移），
// 只给两处用：parity 测试据此核对 SQLite 基线，import-postgres 据此确认源库的版本。PG 代码删除时（G14）一并删除。
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

// Latest 是 SQLite 基线对应的 PostgreSQL 迁移版本。
const Latest = 34

// Migrate 在 sqlDB 上执行全部 PostgreSQL 迁移（带 goose 表锁）。
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
