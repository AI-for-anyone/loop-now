package store

import (
	"context"
	"time"
)

// RecoverStaleModelAuthRefresh only fences abandoned attempts as unavailable.
// It never retries a provider call or promotes old ciphertext back to ready.
func (s *Store) RecoverStaleModelAuthRefresh(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE model_auth_versions SET refresh_state='unavailable',refresh_attempt_id=NULL,refresh_started_at=NULL,error_code='refresh_uncertain',updated_at=clock_timestamp() WHERE refresh_state='refreshing' AND refresh_started_at < $1`, before)
	return tag.RowsAffected(), err
}
