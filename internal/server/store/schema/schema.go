// Package schema holds the SQLite schema migrations and runs them. Version 1 is the baseline (equivalent to the schema
// after PostgreSQL migrations 001-034 have run), and later migrations are appended as usual.
//
// Migrations are run by goose (sqlite3 dialect) on the single write connection, one BEGIN IMMEDIATE transaction per
// migration. Within one data directory only the process holding the instance lock (the server or an import) runs
// migrations, so goose's table lock is not needed.
//
// When writing future migrations, remember that SQLite's ALTER TABLE is limited: changing a column type, or dropping
// or changing a constraint, requires a rebuild as "create a new table, copy, drop the old table, rename"
// (https://sqlite.org/lang_altertable.html#otheralter); such migrations are marked -- +goose NO TRANSACTION and handle
// PRAGMA foreign_keys themselves.
package schema

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/pressly/goose/v3"

	"loop-now/internal/server/db"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Baseline is the version of the baseline migration.
const Baseline = 1

func provider(d *db.DB, logger *slog.Logger) (*goose.Provider, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, d.Writer(), sub,
		goose.WithDisableGlobalRegistry(true),
		goose.WithSlog(logger),
	)
}

// Migrate runs the migrations that have not run yet, in order.
func Migrate(ctx context.Context, d *db.DB, logger *slog.Logger) error {
	p, err := provider(d, logger)
	if err != nil {
		return fmt.Errorf("prepare database migrations: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("applied database migrations", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// MigrateTo runs only the migrations up to and including version; when importing an old database it first stops at the
// baseline, and after the import Migrate runs the remaining migrations.
func MigrateTo(ctx context.Context, d *db.DB, logger *slog.Logger, version int64) error {
	p, err := provider(d, logger)
	if err != nil {
		return fmt.Errorf("prepare database migrations: %w", err)
	}
	results, err := p.UpTo(ctx, version)
	if err != nil {
		return fmt.Errorf("run database migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("applied database migrations", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// Version returns the version migrated to so far; 0 when nothing has been migrated yet.
func Version(ctx context.Context, d *db.DB) (int64, error) {
	p, err := provider(d, slog.New(slog.DiscardHandler))
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}
