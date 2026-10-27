package local

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"syscall"
	"time"

	"github.com/google/uuid"
)

var (
	ErrRuntimeBusy            = errors.New("runtime_busy")
	ErrRuntimeHandoffClosed   = errors.New("runtime_handoff_closed")
	ErrRuntimeHandleMismatch  = errors.New("runtime_handle_mismatch")
	ErrRuntimeStopUnconfirmed = errors.New("runtime_stop_unconfirmed")
	ErrRuntimeSessionMissing  = errors.New("runtime_session_unconfirmed")
)

// RuntimeHandle is an in-memory, lease-bound process generation, never a PID.
// A handle for an absent process is also bound to its lease and runtime.
type RuntimeHandle struct {
	lease   *RuntimeHandoff
	runtime uuid.UUID
	process *proc
}

type handoffStop struct {
	done   chan struct{}
	err    error
	unsafe bool // a stop began but its completion/revocation could not be confirmed
}

// RuntimeHandoff fences execution admission, not PostgreSQL queues or native
// consumers. The caller must own their persistent/consumer fences first, and
// retain this lease through the final transaction's independently known result.
// All fields are protected by Executor.mu; handles remain immutable.
type RuntimeHandoff struct {
	e               *Executor
	ids             []uuid.UUID
	expected        map[uuid.UUID]*proc
	jobs            map[uuid.UUID]*handoffStop
	done            chan struct{}
	closed          bool
	recoveryAction  uuid.UUID // only a persisted pending-switch startup recovery
	recoveryUnknown bool      // any missing/ambiguous OS proof forbids Close
}

// AcquireRuntimeHandoff atomically reserves the entire sorted group. It rejects
// active, starting, waiting-for-slot, or not-yet-reaped processes without
// interrupting them. No process is stopped until the caller invokes StopIdle.
func (e *Executor) AcquireRuntimeHandoff(ctx context.Context, ids []uuid.UUID) (*RuntimeHandoff, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids = slices.Clone(ids)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	if len(ids) == 0 {
		return nil, ErrRuntimeHandleMismatch
	}
	for i, id := range ids {
		if id == uuid.Nil || i > 0 && id == ids[i-1] {
			return nil, ErrRuntimeHandleMismatch
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrRuntimeHandoffClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if e.handoffs[id] != nil || e.runs[id] != nil || e.pending[id] != 0 {
			return nil, ErrRuntimeBusy
		}
		p := e.procs[id]
		if p != nil && (p.busy || p.stopping.Load() || !alive(p)) {
			return nil, ErrRuntimeBusy
		}
		for other := range e.live {
			if other.agentID == id && other != p {
				return nil, ErrRuntimeBusy
			}
		}
	}
	h := &RuntimeHandoff{e: e, ids: ids, expected: make(map[uuid.UUID]*proc), jobs: make(map[uuid.UUID]*handoffStop), done: make(chan struct{})}
	for _, id := range ids {
		h.expected[id] = e.procs[id]
		e.handoffs[id] = h
	}
	return h, nil
}

func (h *RuntimeHandoff) Handle(id uuid.UUID) (RuntimeHandle, error) {
	if h == nil || h.e == nil {
		return RuntimeHandle{}, ErrRuntimeHandoffClosed
	}
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	if h.closed || h.e.closed {
		return RuntimeHandle{}, ErrRuntimeHandoffClosed
	}
	p, ok := h.expected[id]
	if !ok {
		return RuntimeHandle{}, ErrRuntimeHandleMismatch
	}
	return RuntimeHandle{lease: h, runtime: id, process: p}, nil
}

// StopIdle only stops the captured process, preserving HOME, session, workspace
// and artifacts. The context (or configured default bound) limits the caller's
// wait; timeout retains the lease until completion can be checked safely.
func (h *RuntimeHandoff) StopIdle(ctx context.Context, id uuid.UUID, expected RuntimeHandle) error {
	if h == nil || h.e == nil {
		return ErrRuntimeHandoffClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e := h.e
	ctx, cancel := context.WithTimeout(ctx, e.handoffTimeout())
	defer cancel()
	e.mu.Lock()
	if h.closed || e.closed || e.handoffs[id] != h {
		e.mu.Unlock()
		return ErrRuntimeHandoffClosed
	}
	p, member := h.expected[id]
	if !member || expected.lease != h || expected.runtime != id || expected.process != p {
		e.mu.Unlock()
		return ErrRuntimeHandleMismatch
	}
	job := h.jobs[id]
	if job == nil {
		if e.runs[id] != nil || p != nil && p.busy {
			e.mu.Unlock()
			return ErrRuntimeBusy
		}
		current := e.procs[id]
		if current != p && (current != nil || p == nil || alive(p)) {
			e.mu.Unlock()
			return ErrRuntimeHandleMismatch
		}
		job = &handoffStop{done: make(chan struct{})}
		h.jobs[id] = job
		go h.stopCaptured(id, p, job)
	}
	e.mu.Unlock()
	select {
	case <-job.done:
		if err := ctx.Err(); err != nil {
			return err
		}
		e.mu.Lock()
		closed := e.closed
		e.mu.Unlock()
		if closed {
			return ErrRuntimeHandoffClosed
		}
		return job.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *RuntimeHandoff) stopCaptured(id uuid.UUID, p *proc, job *handoffStop) {
	e := h.e
	var err error
	unsafe := false
	defer func() {
		e.mu.Lock()
		job.err, job.unsafe = err, unsafe
		close(job.done)
		e.mu.Unlock()
	}()
	if p == nil { // unprepared followup: do not create or inspect a session
		return
	}
	if e.recordSession(p) != nil {
		err = ErrRuntimeSessionMissing
		return
	}
	e.mu.Lock()
	if e.procs[id] == p {
		delete(e.procs, id)
	}
	e.mu.Unlock()
	e.stop(p, stopCause{reason: stopConfigChanged})
	<-p.stopDone
	groupErr := syscall.Kill(-p.client.Pid(), 0)
	if p.stopErr != nil || alive(p) || !errors.Is(groupErr, syscall.ESRCH) {
		err, unsafe = ErrRuntimeStopUnconfirmed, true
	}
}

// Close refuses to unblock execution after an unconfirmed/in-progress stop.
// It is safe to retry after StopIdle timed out and the same job later finished.
func (h *RuntimeHandoff) Close() error {
	if h == nil || h.e == nil {
		return ErrRuntimeHandoffClosed
	}
	e := h.e
	e.mu.Lock()
	defer e.mu.Unlock()
	if h.closed {
		return nil
	}
	if h.recoveryUnknown {
		return ErrRuntimeStopUnconfirmed
	}
	for _, job := range h.jobs {
		select {
		case <-job.done:
			if job.unsafe {
				return ErrRuntimeStopUnconfirmed
			}
		default:
			return ErrRuntimeStopUnconfirmed
		}
	}
	for _, id := range h.ids {
		if e.handoffs[id] == h {
			delete(e.handoffs, id)
			if p := e.procs[id]; p != nil && p.idleTimer != nil {
				p.idleTimer.Reset(e.o.IdleTimeout)
			}
		}
	}
	h.closed = true
	close(h.done)
	return nil
}

func (e *Executor) handoffTimeout() time.Duration {
	return max(e.o.StartTimeout, 10*time.Second) + 3*max(e.o.StopGrace, time.Second) + 5*time.Second
}

// admitRun is before ExecutionSnapshot: no stale snapshot can wait across a
// handoff and then launch with the old credential identity.
func (e *Executor) admitRun(ctx context.Context, id uuid.UUID, r *run) error {
	e.mu.Lock()
	e.pending[id]++
	defer func() { e.pending[id]--; e.mu.Unlock() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.closed {
			return ErrRuntimeHandoffClosed
		}
		if e.runs[id] != nil {
			return ErrRuntimeBusy
		}
		h := e.handoffs[id]
		if h == nil {
			e.runs[id] = r
			return nil
		}
		e.mu.Unlock()
		select {
		case <-h.done:
		case <-e.closedCh:
		case <-ctx.Done():
		}
		e.mu.Lock()
	}
}

// Existing freeze/release operations join the same admission fence, without
// changing their own stopping/cleanup semantics.
func (e *Executor) reserveMaintenance(ctx context.Context, id uuid.UUID) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handoffs[id] != nil {
		return nil, ErrRuntimeBusy
	}
	h := &RuntimeHandoff{e: e, ids: []uuid.UUID{id}, done: make(chan struct{})}
	e.handoffs[id] = h
	return func() { _ = h.Close() }, nil
}
