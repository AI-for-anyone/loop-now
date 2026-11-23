package sqlitetest_test

import (
	"context"
	"errors"
	"testing"

	"loop-now/internal/server/db"
	"loop-now/internal/testutil/sqlitetest"
)

func TestNewEmptyDBIsolatedAndDetectsNestedWrites(t *testing.T) {
	ctx := context.Background()
	a, b := sqlitetest.NewEmptyDB(t), sqlitetest.NewEmptyDB(t)
	if a.Dir() == b.Dir() {
		t.Fatal("two calls should get different data directories")
	}
	if _, err := a.Exec(ctx, `CREATE TABLE t (v INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec(ctx, `SELECT * FROM t`); err == nil {
		t.Fatal("the other database should not have the table created in a")
	}
	err := a.InTx(ctx, func(tx *db.Tx) error {
		_, err := a.Exec(ctx, `INSERT INTO t VALUES (1)`)
		return err
	})
	if !errors.Is(err, db.ErrNestedWrite) {
		t.Fatalf("the fixture should turn on nested write detection, got %v", err)
	}
}

func TestNewDBMigrated(t *testing.T) {
	d := sqlitetest.NewDB(t)
	var n int
	if err := d.QueryRow(context.Background(), `SELECT count(*) FROM sequences`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("sequences should have 3 rows after migration: %d %v", n, err)
	}
}
