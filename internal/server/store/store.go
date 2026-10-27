// Package store 是服务端对 PostgreSQL 的访问层：连接池、启动时迁移，以及各业务表的读写。
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Store 持有连接池。
type Store struct {
	ImageMaxCount int
	// sleepScan tracks a bounded round of due candidates; it is not sleep authority.
	sleepScan sleepScanProgress
	// TaskCommitted在任务事务提交后唤醒Redis中继；启动时配置，运行中不修改。
	TaskCommitted func()
	// VerificationInvalidated runs only after a credential/project mutation commits.
	VerificationInvalidated func(uuid.UUID, *uuid.UUID)
	// Import hooks are configured only by an App that assembled schema017 import.
	// They are not allow-missing-table fallbacks and never open a nested tx.
	CLIImportDeletingTx func(context.Context, pgx.Tx, uuid.UUID) error
	CLIImportDeleted    func(uuid.UUID)
	Pool                *pgxpool.Pool
	// InstanceID 写入运行记录事件，表示由哪个服务端实例记录（多实例预留）；为空时不记录。
	InstanceID *uuid.UUID
}

// OpenOptions 控制连接方式；测试用 SearchPath 把连接限定在独立 schema 中。
type OpenOptions struct {
	SearchPath string
}

// Open 建立连接池并确认数据库可连接。
func Open(ctx context.Context, databaseURL string, opts OpenOptions) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("解析数据库连接串失败")
	}
	if opts.SearchPath != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = opts.SearchPath
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接池失败：%w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("PostgreSQL 不可用：%w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close 关闭连接池。
func (s *Store) Close() { s.Pool.Close() }

// Ping 用于健康检查。
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// Migrate 按顺序执行尚未执行的迁移。并发执行时由 goose 的表锁保证同一迁移只执行一次；
// 每个迁移在事务中执行，失败时数据库保持在该迁移之前的状态。
func (s *Store) Migrate(ctx context.Context, logger *slog.Logger) error {
	return s.MigrateThrough(ctx, logger, 0)
}

// MigrateThrough supports an explicit Go-only isolated fixture ceiling. A zero
// ceiling retains normal production Up; no runtime/HTTP allow-missing switch.
func (s *Store) MigrateThrough(ctx context.Context, logger *slog.Logger, ceiling int64) error {
	if ceiling < 0 {
		return errors.New("迁移上限无效")
	}
	provider, closeDB, err := s.migrationProvider(logger)
	if err != nil {
		return err
	}
	defer closeDB()
	var results []*goose.MigrationResult
	if ceiling > 0 {
		results, err = provider.UpTo(ctx, ceiling)
	} else {
		results, err = provider.Up(ctx)
	}
	if err != nil {
		return fmt.Errorf("数据库迁移失败：%w", err)
	}
	for _, r := range results {
		logger.Info("已执行数据库迁移", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// migrationProvider 返回带表锁的 goose provider；调用方用完后调用 closeDB。
func (s *Store) migrationProvider(logger *slog.Logger) (*goose.Provider, func(), error) {
	locker, err := lock.NewPostgresTableLocker(
		lock.WithTableLockTimeout(500*time.Millisecond, 600),
		lock.WithTableLogger(logger),
	)
	if err != nil {
		return nil, nil, err
	}
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, nil, err
	}
	db := stdlib.OpenDBFromPool(s.Pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithLocker(locker),
		goose.WithDisableGlobalRegistry(true),
		goose.WithGoMigrations(s.projectCredentialMigration()),
		goose.WithSlog(logger),
	)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return provider, func() { db.Close() }, nil
}

// isUniqueViolation 判断错误是否为指定约束的唯一冲突；constraint 为空时匹配任意唯一冲突。
func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// inTx 在事务中执行 fn。
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, fn)
}
