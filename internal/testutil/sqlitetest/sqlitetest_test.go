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
		t.Fatal("两次调用应得到不同的数据目录")
	}
	if _, err := a.Exec(ctx, `CREATE TABLE t (v INTEGER) STRICT`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec(ctx, `SELECT * FROM t`); err == nil {
		t.Fatal("另一个库里不应有 a 建的表")
	}
	err := a.InTx(ctx, func(tx *db.Tx) error {
		_, err := a.Exec(ctx, `INSERT INTO t VALUES (1)`)
		return err
	})
	if !errors.Is(err, db.ErrNestedWrite) {
		t.Fatalf("夹具应打开嵌套写检测，得到 %v", err)
	}
}

func TestNewDBMigrated(t *testing.T) {
	d := sqlitetest.NewDB(t)
	var n int
	if err := d.QueryRow(context.Background(), `SELECT count(*) FROM sequences`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("迁移后应有 sequences 的 3 行：%d %v", n, err)
	}
}
