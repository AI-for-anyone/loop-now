package larkauth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"loop-now/internal/server/clicredential"
	"loop-now/internal/server/store"
	"time"
)

type failCommitStore struct {
	*store.Store
	after bool
}

type blockedClaimStore struct {
	*store.Store
	entered, release chan struct{}
}

func (s blockedClaimStore) ClaimLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, g int64, l uuid.UUID, until time.Time) (bool, error) {
	close(s.entered)
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-s.release:
	}
	return s.Store.ClaimLarkCLIRefresh(ctx, b, g, l, until)
}

func (s failCommitStore) CommitLarkCLIRefresh(ctx context.Context, b clicredential.LarkBinding, g int64, l uuid.UUID, secret []byte) error {
	if s.after {
		if e := s.Store.CommitLarkCLIRefresh(ctx, b, g, l, secret); e != nil {
			return e
		}
	}
	return errors.New("synthetic commit acknowledgement loss")
}
