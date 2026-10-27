package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"loop-now/internal/server/content"
)

// putBlob 写入内容寻址存储，相同内容只保留一份，返回 sha256。
func putBlob(ctx context.Context, tx pgx.Tx, data []byte) (string, error) {
	sum := content.SHA256Hex(data)
	_, err := tx.Exec(ctx, `INSERT INTO blobs (sha256, size, data) VALUES ($1, $2, $3) ON CONFLICT (sha256) DO NOTHING`, sum, len(data), data)
	return sum, err
}

// ReadBlob 按 sha256 读取内容。
func (s *Store) ReadBlob(ctx context.Context, sha string) ([]byte, error) {
	var data []byte
	err := s.Pool.QueryRow(ctx, `SELECT data FROM blobs WHERE sha256 = $1`, sha).Scan(&data)
	return data, notFoundIfNoRows(err, "内容")
}

// PutBlob 在事务外写入内容（测试与工具使用）。
func (s *Store) PutBlob(ctx context.Context, data []byte) (string, error) {
	var sum string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		sum, err = putBlob(ctx, tx, data)
		return err
	})
	return sum, err
}
