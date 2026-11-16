package store_test

import (
	"context"
	"testing"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// goldenStore and goldenTx are the SQLite fixtures for the plugin event comparison scenarios
// (plugin_events_golden_test.go).
func goldenStore(t *testing.T) *store.Store { return sqlitetest.NewStore(t) }

// goldenTx runs the statements given by fn, in order, in one write transaction.
func goldenTx(t *testing.T, s *store.Store, fn func(exec func(string, ...any))) {
	t.Helper()
	ctx := context.Background()
	if err := s.Pool.InTx(ctx, func(tx *db.Tx) error {
		var err error
		fn(func(q string, args ...any) {
			if err == nil {
				_, err = tx.Exec(ctx, q, args...)
			}
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
