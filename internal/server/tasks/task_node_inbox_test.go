package tasks

import (
	"context"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/testutil/sqlitetest"
	"testing"

	"github.com/google/uuid"
)

// A closed store fails every query. An unavailable database must not make an
// accepted action appear absent to the client.
func TestTaskNodeInboxActionDatabaseFailureIsNotNotFound(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.NewStore(t)
	st.Close()
	s := &Service{Store: st}
	_, err := s.NodeInboxAction(ctx, auth.Console(), uuid.New(), uuid.New(), uuid.New())
	if err == nil || apperr.Is(err, "not_found") {
		t.Fatalf("database failure was hidden as action absence: %v", err)
	}
}
