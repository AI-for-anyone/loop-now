package store

import (
	"context"
	"io"
	"log/slog"
)

// MigrateUpTo 只供测试：迁移到指定版本。
func (s *Store) MigrateUpTo(ctx context.Context, version int64) error {
	p, closeDB, err := s.migrationProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	defer closeDB()
	_, err = p.UpTo(ctx, version)
	return err
}

// MigrateDownTo 只供测试：回退到指定版本。
func (s *Store) MigrateDownTo(ctx context.Context, version int64) error {
	p, closeDB, err := s.migrationProvider(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	defer closeDB()
	_, err = p.DownTo(ctx, version)
	return err
}
