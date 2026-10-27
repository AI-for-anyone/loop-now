package modelauth

import (
	"context"
	"github.com/google/uuid"
)

// CancelAgentLogins is invoked only after the caller's deletion transaction has
// committed. It drains login workers, including helper process/port cleanup,
// without depending on HTTP cancellation or holding the service mutex. It must
// never close/cancel Coordinator refresh workers: rotating grants have a
// separate independent lifetime and durable-write contract.
func (s *Service) CancelAgentLogins(ctx context.Context, ids []uuid.UUID) error {
	selected := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	s.mu.Lock()
	workers := make([]*loginWorker, 0)
	for _, work := range s.operations {
		if selected[work.agentID] {
			workers = append(workers, work)
		}
	}
	s.mu.Unlock()
	for _, work := range workers {
		work.cancel()
	}
	drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.Options.LoginDrainTimeout)
	defer cancel()
	for _, work := range workers {
		select {
		case <-work.done:
		case <-drain.Done():
			return drain.Err()
		}
	}
	return nil
}
