package store_test

import (
	"context"
	"testing"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// goldenStore 与 goldenTx 是插件事件对拍场景（plugin_events_golden_test.go）在 SQLite 上的夹具。
func goldenStore(t *testing.T) *store.Store { return sqlitetest.NewStore(t) }

// goldenTx 在一个写事务里依次执行 fn 给出的语句。
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
