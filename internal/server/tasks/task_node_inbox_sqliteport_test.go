//go:build sqliteport

// SQLITE-PORT(G5): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 task_node_inbox_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：TestTaskNodeInboxActionDatabaseFailureIsNotNotFound

package tasks

import (
	"context"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A lazy, closed pool fails before any network connection. An unavailable
// database must not make an accepted action appear absent to the client.
func TestTaskNodeInboxActionDatabaseFailureIsNotNotFound(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	s := &Service{Store: &store.Store{Pool: pool}}
	_, err = s.NodeInboxAction(ctx, auth.Console(), uuid.New(), uuid.New(), uuid.New())
	if err == nil || apperr.Is(err, "not_found") {
		t.Fatalf("database failure was hidden as action absence: %v", err)
	}
}
