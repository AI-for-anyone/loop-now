package store

import (
	"context"

	"loop-now/internal/server/content"
	"loop-now/internal/server/db"
)

// putBlob writes to the content-addressed store, keeping a single copy of identical content, and returns the sha256.
func putBlob(ctx context.Context, tx *db.Tx, data []byte) (string, error) {
	sum := content.SHA256Hex(data)
	_, err := tx.Exec(ctx, `INSERT INTO blobs (sha256, size, data) VALUES ($1, $2, $3) ON CONFLICT (sha256) DO NOTHING`, sum, len(data), data)
	return sum, err
}

// ReadBlob reads content by sha256.
func (s *Store) ReadBlob(ctx context.Context, sha string) ([]byte, error) {
	var data []byte
	err := s.Pool.QueryRow(ctx, `SELECT data FROM blobs WHERE sha256 = $1`, sha).Scan(&data)
	return data, notFoundIfNoRows(err, "内容")
}

// PutBlob writes content outside a transaction (used by tests and tools).
func (s *Store) PutBlob(ctx context.Context, data []byte) (string, error) {
	var sum string
	err := s.inTx(ctx, func(tx *db.Tx) error {
		var err error
		sum, err = putBlob(ctx, tx, data)
		return err
	})
	return sum, err
}
