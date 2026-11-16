package conversation

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// PauseDispatch takes the dispatch gate once the current turn has fully exited. Cancelling the wait neither blocks the
// old turn nor leaks the gate. The returned release may be called repeatedly; while the gate is held, the queue and the
// lock are still managed by the original driver loop.
func (s *Service) PauseDispatch(ctx context.Context, agentID uuid.UUID) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, _ := s.dispatchGates.LoadOrStore(agentID, make(chan struct{}, 1))
	gate := v.(chan struct{})
	select {
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { <-gate }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
