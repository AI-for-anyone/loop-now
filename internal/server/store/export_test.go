package store

import (
	"context"

	"errors"
)

var errNoMigrationHistory = errors.New("SQLITE-PORT(history): SQLite 只有基线，没有可升降的迁移历史")

// MigrateUpTo 原来只供测试迁移到指定的 PostgreSQL 版本；SQLite 下没有这些版本。
func (s *Store) MigrateUpTo(context.Context, int64) error { return errNoMigrationHistory }

// MigrateDownTo 原来只供测试回退到指定的 PostgreSQL 版本；SQLite 下没有这些版本。
func (s *Store) MigrateDownTo(context.Context, int64) error { return errNoMigrationHistory }
