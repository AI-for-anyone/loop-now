// Package sqlitetest 为组件测试提供隔离的 SQLite 环境：每次调用在 t.TempDir() 下建独立的数据目录，
// 测试结束时关闭并随临时目录删除。不需要外部服务，测试之间互不影响，可以放心 t.Parallel()。
//
// 夹具打开 db.DetectNestedWrites：写事务回调里误经 *db.DB 写入会立即报错，而不是等到超时。
package sqlitetest

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
	"loop-now/internal/server/store/schema"
)

func init() { db.DetectNestedWrites.Store(true) }

// NewDB 返回一个已执行全部迁移的库（基线约 20ms，不需要模板复制）。
func NewDB(t testing.TB) *db.DB {
	t.Helper()
	d := open(t, t.TempDir())
	if err := schema.Migrate(context.Background(), d, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("迁移测试库失败：%v", err)
	}
	return d
}

// NewStore 返回一个已迁移、独立数据目录的 Store（对应原来的 pgtest.NewStore）。
func NewStore(t testing.TB) *store.Store {
	t.Helper()
	return store.New(NewDB(t))
}

// NewStoreIn 在给定数据目录打开并迁移 Store，供需要在同一目录重开库的测试使用；调用方负责关闭。
func NewStoreIn(t testing.TB, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), db.Options{Dir: dir})
	if err != nil {
		t.Fatalf("打开测试库失败：%v", err)
	}
	if err := s.Migrate(context.Background(), slog.New(slog.DiscardHandler)); err != nil {
		s.Close()
		t.Fatalf("迁移测试库失败：%v", err)
	}
	return s
}

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

// HoldWriter 在一个写事务里占住唯一的写连接，返回时已经占住；调用返回的函数放开它（可以多次调用）。
// 测试用它让并发的写入停在“等写连接”上，代替 PostgreSQL 时期持有行锁的做法。测试结束时自动放开。
func HoldWriter(t testing.TB, d *db.DB) (release func()) {
	t.Helper()
	held, done := make(chan struct{}), make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		ended <- d.InTx(context.Background(), func(*db.Tx) error {
			close(held)
			<-done
			return nil
		})
	}()
	select {
	case <-held:
	case err := <-ended:
		t.Fatalf("占住写连接失败：%v", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			close(done)
			if err := <-ended; err != nil {
				t.Errorf("放开写连接失败：%v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
}
