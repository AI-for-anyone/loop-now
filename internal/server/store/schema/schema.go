// Package schema 持有 SQLite 的表结构迁移并负责执行。版本 1 是基线（等同于 PostgreSQL 迁移 001–034
// 执行完后的结构），之后的迁移照常追加。
//
// 迁移由 goose（sqlite3 方言）在唯一的写连接上执行，每条迁移一个 BEGIN IMMEDIATE 事务。同一数据目录只有
// 持实例锁的进程（服务或导入）执行迁移，所以不需要 goose 的表锁。
//
// 以后写迁移要记住 SQLite 的 ALTER TABLE 能力有限：改列类型、删改约束要按“新建表、复制、删旧表、改名”
// 重建（https://sqlite.org/lang_altertable.html#otheralter），这类迁移标 -- +goose NO TRANSACTION，
// 在迁移里自己处理 PRAGMA foreign_keys。
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

// Baseline 是基线迁移的版本号。
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

// Migrate 按顺序执行尚未执行的迁移。
func Migrate(ctx context.Context, d *db.DB, logger *slog.Logger) error {
	p, err := provider(d, logger)
	if err != nil {
		return fmt.Errorf("准备数据库迁移失败：%w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("执行数据库迁移失败：%w", err)
	}
	for _, r := range results {
		logger.Info("已执行数据库迁移", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// Version 返回已执行到的版本号；还没迁移时为 0。
func Version(ctx context.Context, d *db.DB) (int64, error) {
	p, err := provider(d, slog.New(slog.DiscardHandler))
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}
