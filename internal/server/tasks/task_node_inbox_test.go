package tasks

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
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
