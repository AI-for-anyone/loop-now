package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// PendingRuntimeHandoff comes only from the persisted pending switch action,
// never from a new registry's apparent absence of processes. Both IDs are exact.
type PendingRuntimeHandoff struct {
	ActionID   uuid.UUID    `json:"actionId"`
	RuntimeIDs [2]uuid.UUID `json:"runtimeIds"`
}

// RuntimeProcessIdentity contains no credentials or process environment. Start
// is the kernel process birth time, not the platform's wall-clock launch time.
type RuntimeProcessIdentity struct {
	PID               int    `json:"pid"`
	PGID              int    `json:"pgid"`
	UID               uint32 `json:"uid"`
	StartSeconds      int64  `json:"startSeconds"`
	StartMicroseconds int64  `json:"startMicroseconds"`
}
type RuntimeRecoveryMember struct {
	RuntimeID uuid.UUID               `json:"runtimeId"`
	Absent    bool                    `json:"absent"`
	Process   *RuntimeProcessIdentity `json:"process,omitempty"`
}

// RuntimeRecoveryRecord is captured under the ORIGINAL two-member local gate
// before any StopIdle. The owner persists it with that action, not its public
// successful receipt. Missing records are unknown, not proof of old OS death.
type RuntimeRecoveryRecord struct {
	Version    int                     `json:"version"`
	ActionID   uuid.UUID               `json:"actionId"`
	InstanceID uuid.UUID               `json:"instanceId"`
	Hostname   string                  `json:"hostname"`
	BootID     string                  `json:"bootId"`
	WorkRoot   string                  `json:"workRoot"`
	Members    []RuntimeRecoveryMember `json:"members"`
}

type RuntimeRecoveryReceipt struct {
	ActionID    uuid.UUID    `json:"actionId"`
	RuntimeIDs  [2]uuid.UUID `json:"runtimeIds"`
	GroupAbsent [2]bool      `json:"groupAbsent"`
	CheckedAt   time.Time    `json:"checkedAt"`
}

func validPending(p PendingRuntimeHandoff) bool {
	return p.ActionID != uuid.Nil && p.RuntimeIDs[0] != uuid.Nil && p.RuntimeIDs[1] != uuid.Nil && p.RuntimeIDs[0] != p.RuntimeIDs[1]
}

func (h *RuntimeHandoff) CaptureRecovery(ctx context.Context, actionID uuid.UUID) (RuntimeRecoveryRecord, error) {
	var out RuntimeRecoveryRecord
	if ctx == nil || ctx.Err() != nil || h == nil || h.e == nil || actionID == uuid.Nil {
		return out, ErrRuntimeStopUnconfirmed
	}
	host, err := os.Hostname()
	if err != nil {
		return out, ErrRuntimeStopUnconfirmed
	}
	boot, err := recoveryBootID()
	if err != nil {
		return out, ErrRuntimeStopUnconfirmed
	}
	e := h.e
	e.mu.Lock()
	defer e.mu.Unlock()
	if h.closed || e.closed || e.o.InstanceID == uuid.Nil || !filepath.IsAbs(e.o.WorkRoot) || len(h.ids) != 2 || len(h.jobs) != 0 || h.recoveryAction != uuid.Nil {
		return out, ErrRuntimeStopUnconfirmed
	}
	out = RuntimeRecoveryRecord{Version: 1, ActionID: actionID, InstanceID: e.o.InstanceID, Hostname: host, BootID: boot, WorkRoot: filepath.Clean(e.o.WorkRoot)}
	for _, id := range h.ids {
		if e.handoffs[id] != h || e.runs[id] != nil || e.pending[id] != 0 {
			return RuntimeRecoveryRecord{}, ErrRuntimeStopUnconfirmed
		}
		p := h.expected[id]
		member := RuntimeRecoveryMember{RuntimeID: id, Absent: p == nil}
		if p != nil {
			if e.procs[id] != p || !alive(p) || p.busy || p.recoveryIdentity == nil {
				return RuntimeRecoveryRecord{}, ErrRuntimeStopUnconfirmed
			}
			current, err := readRecoveryProcess(p.client.Pid())
			if err != nil || current != *p.recoveryIdentity {
				return RuntimeRecoveryRecord{}, ErrRuntimeStopUnconfirmed
			}
			identity := current
			member.Process = &identity
		}
		out.Members = append(out.Members, member)
	}
	if ctx.Err() != nil {
		return RuntimeRecoveryRecord{}, ctx.Err()
	}
	return out, nil
}

// RecoverPendingHandoff must run before startup opens dispatch/claims and before
// legacy CleanupStale touches these runtimes. It does NOT release directories,
// archives, sessions, pid files, native groups or credentials. The returned gate
// stays held on any unknown result; Close refuses release until BOTH groups are
// proven absent. Even success must be held until the owner resolves the action.
func (e *Executor) RecoverPendingHandoff(ctx context.Context, pending PendingRuntimeHandoff, record RuntimeRecoveryRecord) (*RuntimeHandoff, RuntimeRecoveryReceipt, error) {
	var out RuntimeRecoveryReceipt
	if ctx == nil || !validPending(pending) {
		return nil, out, ErrRuntimeStopUnconfirmed
	}
	h, err := e.AcquireRuntimeHandoff(ctx, pending.RuntimeIDs[:])
	if err != nil {
		return nil, out, err
	}
	e.mu.Lock()
	h.recoveryAction = pending.ActionID
	h.recoveryUnknown = true
	e.mu.Unlock()
	unknown := func() (*RuntimeHandoff, RuntimeRecoveryReceipt, error) {
		return h, RuntimeRecoveryReceipt{}, ErrRuntimeStopUnconfirmed
	}
	// No newly started process may be treated as the old generation, or stopped
	// by this recovery path. A real startup registry should have no live entries.
	for _, p := range h.expected {
		if p != nil {
			return unknown()
		}
	}
	host, err := os.Hostname()
	if err != nil {
		return unknown()
	}
	boot, err := recoveryBootID()
	if err != nil {
		return unknown()
	}
	if record.Version != 1 || record.ActionID != pending.ActionID || e.o.InstanceID == uuid.Nil || record.InstanceID == uuid.Nil || record.InstanceID == e.o.InstanceID || record.Hostname != host || record.BootID != boot || !filepath.IsAbs(record.WorkRoot) || record.WorkRoot != filepath.Clean(e.o.WorkRoot) || len(record.Members) != 2 {
		return unknown()
	}
	members := make(map[uuid.UUID]RuntimeRecoveryMember, 2)
	processes := make(map[int]bool, 2)
	for _, m := range record.Members {
		if !slices.Contains(pending.RuntimeIDs[:], m.RuntimeID) {
			return unknown()
		}
		if _, exists := members[m.RuntimeID]; exists {
			return unknown()
		}
		if m.Absent {
			if m.Process != nil {
				return unknown()
			}
		} else if !validRecoveryProcess(m.Process) {
			return unknown()
		} else if processes[m.Process.PID] {
			return unknown()
		} else {
			processes[m.Process.PID] = true
		}
		members[m.RuntimeID] = m
	}
	// Validate the entire pair before sending ANY signal. An unrelated or reused
	// PID in either member must leave all other members untouched.
	for _, id := range pending.RuntimeIDs {
		m := members[id]
		if !m.Absent {
			if err := verifyRecoveryProcess(*m.Process); err != nil {
				return unknown()
			}
		}
	}
	bounded, cancel := context.WithTimeout(ctx, e.handoffTimeout())
	defer cancel()
	out = RuntimeRecoveryReceipt{ActionID: pending.ActionID, RuntimeIDs: pending.RuntimeIDs}
	for i, id := range pending.RuntimeIDs {
		m := members[id]
		if !m.Absent {
			if err := stopRecoveryGroup(bounded, *m.Process, e.o.StopGrace); err != nil {
				return unknown()
			}
		}
		out.GroupAbsent[i] = true
	}
	if bounded.Err() != nil {
		return unknown()
	}
	out.CheckedAt = time.Now().UTC()
	e.mu.Lock()
	h.recoveryUnknown = false
	e.mu.Unlock()
	return h, out, nil
}

func validRecoveryProcess(p *RuntimeProcessIdentity) bool {
	return p != nil && p.PID > 1 && p.PGID == p.PID && p.UID == uint32(os.Getuid()) && p.StartSeconds > 0 && p.StartMicroseconds >= 0 && p.StartMicroseconds < 1_000_000
}

func verifyRecoveryProcess(p RuntimeProcessIdentity) error {
	current, err := readRecoveryProcess(p.PID)
	if err == nil {
		if current != p {
			return ErrRuntimeStopUnconfirmed
		}
		return nil
	}
	if !errors.Is(err, syscall.ESRCH) {
		return ErrRuntimeStopUnconfirmed
	}
	if err = syscall.Kill(-p.PGID, 0); !errors.Is(err, syscall.ESRCH) {
		return ErrRuntimeStopUnconfirmed
	}
	return nil
}

func stopRecoveryGroup(ctx context.Context, p RuntimeProcessIdentity, grace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyRecoveryProcess(p); err != nil {
		return err
	}
	if err := syscall.Kill(-p.PGID, 0); errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return ErrRuntimeStopUnconfirmed
	}
	// Revalidate kernel birth immediately before every signal, never just PID.
	if err := signalRecoveryGroup(ctx, p, syscall.SIGTERM); err != nil {
		return err
	}
	if grace <= 0 {
		grace = 100 * time.Millisecond
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		err := syscall.Kill(-p.PGID, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return ErrRuntimeStopUnconfirmed
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		case <-timer.C:
			if err := signalRecoveryGroup(ctx, p, syscall.SIGKILL); err != nil {
				return err
			}
			for {
				err := syscall.Kill(-p.PGID, 0)
				if errors.Is(err, syscall.ESRCH) {
					return nil
				}
				if err != nil {
					return ErrRuntimeStopUnconfirmed
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-poll.C:
				}
			}
		}
	}
}

func signalRecoveryGroup(ctx context.Context, p RuntimeProcessIdentity, signal syscall.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := readRecoveryProcess(p.PID)
	if err != nil || current != p {
		return ErrRuntimeStopUnconfirmed
	}
	if err = syscall.Kill(-p.PGID, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return ErrRuntimeStopUnconfirmed
	}
	return nil
}
