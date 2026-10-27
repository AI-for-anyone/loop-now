package tasks

import (
	"bytes"
	"context"
	"slices"

	"github.com/google/uuid"
	"loop-now/internal/server/executor/local"
)

// acquireTaskLarkHandoff is phase B's concrete local process operation, outside
// any database transaction. The caller must already own both persistent fences
// and authoritative native/broker consumer gates. It never prepares or releases
// a runtime. Even absent processes retain admission gates through phase C.
//
// A non-nil lease remains owned by the caller even when err != nil. Do not
// defer Close: stop or COMMIT uncertainty must retain coordination until its
// result is independently known. Stop/revoke/drain must be sealed-ready before
// C; only then may confirmed C be followed by pure in-memory gate release.
// If C is unknown, retain this lease and pause dispatch. The PG fence may
// already have been atomically removed by C: do not claim it still exists.
func acquireTaskLarkHandoff(ctx context.Context, ex *local.Executor, runtimes []uuid.UUID) (*local.RuntimeHandoff, error) {
	return acquireTaskLarkHandoffBeforeStop(ctx, ex, runtimes, nil)
}

// persist runs under both original creation gates after capturing the exact
// handles, before ANY StopIdle. Persistence failure must not discard the lease.
func acquireTaskLarkHandoffBeforeStop(ctx context.Context, ex *local.Executor, runtimes []uuid.UUID, persist func(*local.RuntimeHandoff) error) (*local.RuntimeHandoff, error) {
	if ex == nil || len(runtimes) != 2 {
		return nil, local.ErrRuntimeHandleMismatch
	}
	ids := slices.Clone(runtimes)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	lease, err := ex.AcquireRuntimeHandoff(ctx, ids)
	if err != nil {
		return nil, err
	}
	handles := make([]local.RuntimeHandle, len(ids))
	for i, id := range ids {
		handles[i], err = lease.Handle(id)
		if err != nil {
			return lease, err
		}
	}
	if persist != nil {
		if err = persist(lease); err != nil {
			return lease, err
		}
	}
	for i, id := range ids {
		if err = lease.StopIdle(ctx, id, handles[i]); err != nil {
			return lease, err
		}
	}
	return lease, nil
}
