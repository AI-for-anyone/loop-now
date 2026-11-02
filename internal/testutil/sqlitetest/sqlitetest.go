// Package sqlitetest 为组件测试提供隔离的 SQLite 环境：每次调用在 t.TempDir() 下建独立的数据目录，
// 测试结束时关闭并随临时目录删除。不需要外部服务，测试之间互不影响，可以放心 t.Parallel()。
//
// 夹具打开 db.DetectNestedWrites：写事务回调里误经 *db.DB 写入会立即报错，而不是等到超时。
package sqlitetest

import (
	"context"
	"testing"

	"loop-now/internal/server/db"
)

func init() { db.DetectNestedWrites.Store(true) }

// NewEmptyDB 返回一个没有任何表的库，供只测封装层或自建表的测试使用。
func NewEmptyDB(t testing.TB) *db.DB {
	t.Helper()
	return open(t, t.TempDir())
}

func open(t testing.TB, dir string) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), db.Options{Dir: dir})
	if err != nil {
		t.Fatalf("打开测试库失败：%v", err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Errorf("关闭测试库失败：%v", err)
		}
	})
	return d
}
