package larksidecar

import (
	"context"
	"errors"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/google/uuid"
)

var ErrNativeRecoveryUnknown = errors.New("lark native recovery unconfirmed")

// These records contain no capabilities, credentials, environment or argv.
// They come only from an original held gate and its trusted action transaction.
type NativeProcessIdentity struct {
	PID               int    `json:"pid"`
	PGID              int    `json:"pgid"`
	UID               uint32 `json:"uid"`
	StartSeconds      int64  `json:"startSeconds"`
	StartMicroseconds int64  `json:"startMicroseconds"`
}

type NativeRecoveryMember struct {
	RuntimeID  uuid.UUID              `json:"runtimeId"`
	Absent     bool                   `json:"absent"`
	ScopeEpoch uuid.UUID              `json:"scopeEpoch"`
	Accepted   uint64                 `json:"accepted"`
	Started    uint64                 `json:"started"`
	Confirmed  uint64                 `json:"confirmed"`
	LastNative *NativeProcessIdentity `json:"lastNative,omitempty"`
}

type NativeRecoveryRecord struct {
	Version      int                    `json:"version"`
	ActionID     uuid.UUID              `json:"actionId"`
	ManagerEpoch uuid.UUID              `json:"managerEpoch"`
	Hostname     string                 `json:"hostname"`
	BootID       string                 `json:"bootId"`
	Owner        NativeProcessIdentity  `json:"owner"`
	Members      []NativeRecoveryMember `json:"members"`
}

type PendingNativeHandoff struct {
	ActionID   uuid.UUID
	RuntimeIDs [2]uuid.UUID
}

type NativeRecoveryReceipt struct {
	ActionID     uuid.UUID
	RuntimeIDs   [2]uuid.UUID
	OwnerAbsent  bool
	GroupsAbsent [2]bool
	CheckedAt    time.Time
}

func copyNativeRecord(in NativeRecoveryRecord) NativeRecoveryRecord {
	in.Members = slices.Clone(in.Members)
	for i := range in.Members {
		if p := in.Members[i].LastNative; p != nil {
			v := *p
			in.Members[i].LastNative = &v
		}
	}
	return in
}

// CaptureRecovery must precede any StopIdle. It cannot capture active native
// groups: acquisition already requires preparation/Start/Wait to be confirmed
// idle. The original persisted fact must never be recreated from a new registry.
func (g *RuntimeGate) CaptureRecovery(ctx context.Context, actionID uuid.UUID) (NativeRecoveryRecord, error) {
	var zero NativeRecoveryRecord
	if g == nil || ctx == nil || ctx.Err() != nil || actionID == uuid.Nil {
		return zero, ErrNativeRecoveryUnknown
	}
	host, err := os.Hostname()
	if err != nil {
		return zero, ErrNativeRecoveryUnknown
	}
	boot, err := nativeBootID()
	if err != nil {
		return zero, ErrNativeRecoveryUnknown
	}
	owner, err := readNativeProcess(os.Getpid())
	if err != nil {
		return zero, ErrNativeRecoveryUnknown
	}
	m := g.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if g.released || g.recoveryAction != uuid.Nil || m.epoch == uuid.Nil || len(g.ids) != 2 {
		return zero, ErrNativeRecoveryUnknown
	}
	if g.captured != nil {
		if g.captured.ActionID != actionID {
			return zero, ErrNativeRecoveryUnknown
		}
		return copyNativeRecord(*g.captured), nil
	}
	if g.attempted || g.sealed {
		return zero, ErrNativeRecoveryUnknown
	}
	out := NativeRecoveryRecord{Version: 1, ActionID: actionID, ManagerEpoch: m.epoch, Hostname: host, BootID: boot, Owner: owner}
	for _, id := range g.ids {
		if m.gates[id] != g || m.creating[id] || m.active[id] != 0 {
			return zero, ErrNativeRecoveryUnknown
		}
		member := NativeRecoveryMember{RuntimeID: uuid.MustParse(id), Absent: true}
		var original *ManagedScope
		for _, s := range g.scopes {
			if s.id == id {
				original = s
			}
		}
		if s := m.scopes[id]; s != original {
			return zero, ErrNativeRecoveryUnknown
		}
		if original != nil {
			s := original
			s.broker.mu.Lock()
			closed := s.broker.closed
			s.broker.mu.Unlock()
			if closed || s.closed || s.epoch == uuid.Nil || s.active != 0 || len(s.processes) != 0 || s.identityUnknown || s.started != s.confirmed || s.confirmed > s.accepted {
				return zero, ErrNativeRecoveryUnknown
			}
			member.Absent, member.ScopeEpoch = false, s.epoch
			member.Accepted, member.Started, member.Confirmed = s.accepted, s.started, s.confirmed
			if s.started != 0 {
				if !validNativeGroup(s.lastNative) || !nativeGroupAbsent(*s.lastNative) {
					return zero, ErrNativeRecoveryUnknown
				}
				p := *s.lastNative
				member.LastNative = &p
			}
		}
		out.Members = append(out.Members, member)
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	stored := copyNativeRecord(out)
	g.captured = &stored
	return out, nil
}

func validNativeIdentity(p NativeProcessIdentity) bool {
	return p.PID > 1 && p.PGID > 1 && p.UID == uint32(os.Getuid()) && p.StartSeconds > 0 && p.StartMicroseconds >= 0 && p.StartMicroseconds < 1_000_000
}
func validNativeGroup(p *NativeProcessIdentity) bool {
	return p != nil && validNativeIdentity(*p) && p.PID == p.PGID
}
func nativeGroupAbsent(p NativeProcessIdentity) bool {
	return errors.Is(syscall.Kill(-p.PGID, 0), syscall.ESRCH)
}

// RecoverPendingHandoff is strictly pre-C. consumePreCommit is a trusted Go
// closure, never a request flag: it must verify the ORIGINAL record, pending/
// handoff action, both original fences and consume that proof in ONE original
// task/action transaction while startup dispatch remains paused. A lock-free
// status check is insufficient. A C-success/unknown path cannot consume this
// idle watermark (post-release native launches would make it stale).
//
// This method never signals the old server or guesses native PIDs. An old owner
// still present, reused PID, missing record, live group or failed/unknown commit
// returns a HELD gate and no receipt. Seal/Release cannot bypass that uncertainty.
func (m *Manager) RecoverPendingHandoff(ctx context.Context, pending PendingNativeHandoff, record NativeRecoveryRecord, consumePreCommit func(context.Context) error) (*RuntimeGate, NativeRecoveryReceipt, error) {
	var zero NativeRecoveryReceipt
	if m == nil || ctx == nil || pending.ActionID == uuid.Nil || pending.RuntimeIDs[0] == uuid.Nil || pending.RuntimeIDs[1] == uuid.Nil || pending.RuntimeIDs[0] == pending.RuntimeIDs[1] {
		return nil, zero, ErrNativeRecoveryUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, execCleanupTimeout)
	defer cancel()
	ids := []string{pending.RuntimeIDs[0].String(), pending.RuntimeIDs[1].String()}
	m.mu.Lock()
	g := m.gates[ids[0]]
	if g != nil && (m.gates[ids[1]] != g || g.recoveryAction != pending.ActionID) {
		m.mu.Unlock()
		return nil, zero, ErrNativeBusy
	}
	m.mu.Unlock()
	if g == nil {
		var err error
		g, err = m.AcquireRuntimeGate(ctx, pending.RuntimeIDs[:])
		if err != nil {
			return nil, zero, err
		}
		m.mu.Lock()
		g.recoveryAction, g.recoveryUnknown, g.attempted = pending.ActionID, true, true
		m.mu.Unlock()
	}
	unknown := func() (*RuntimeGate, NativeRecoveryReceipt, error) { return g, zero, ErrNativeRecoveryUnknown }
	select {
	case g.sealToken <- struct{}{}:
		defer func() { <-g.sealToken }()
	case <-ctx.Done():
		return unknown()
	}
	m.mu.Lock()
	invalid := g.released || g.sealed || len(g.scopes) != 0 || m.gates[ids[0]] != g || m.gates[ids[1]] != g
	m.mu.Unlock()
	if invalid || consumePreCommit == nil || ctx.Err() != nil {
		return unknown()
	}
	host, err := os.Hostname()
	if err != nil {
		return unknown()
	}
	boot, err := nativeBootID()
	if err != nil {
		return unknown()
	}
	if record.Version != 1 || record.ActionID != pending.ActionID || record.ManagerEpoch == uuid.Nil || record.ManagerEpoch == m.epoch || record.Hostname != host || record.BootID != boot || !validNativeIdentity(record.Owner) || len(record.Members) != 2 {
		return unknown()
	}
	seen := make(map[uuid.UUID]bool, 2)
	for _, member := range record.Members {
		if (member.RuntimeID != pending.RuntimeIDs[0] && member.RuntimeID != pending.RuntimeIDs[1]) || seen[member.RuntimeID] {
			return unknown()
		}
		seen[member.RuntimeID] = true
		if member.Absent {
			if member.ScopeEpoch != uuid.Nil || member.Accepted != 0 || member.Started != 0 || member.Confirmed != 0 || member.LastNative != nil {
				return unknown()
			}
		} else {
			if member.ScopeEpoch == uuid.Nil || member.Started != member.Confirmed || member.Started > member.Accepted {
				return unknown()
			}
			if member.Started == 0 {
				if member.LastNative != nil {
					return unknown()
				}
			} else if !validNativeGroup(member.LastNative) || !nativeGroupAbsent(*member.LastNative) {
				return unknown()
			}
		}
	}
	// ESRCH only: PID reuse/mismatched birth cannot authorize signals or success.
	if _, err := readNativeProcess(record.Owner.PID); !errors.Is(err, syscall.ESRCH) {
		return unknown()
	}
	if ctx.Err() != nil {
		return unknown()
	}
	// The callback includes the DB consumption/known outcome, not just a check.
	// Never hold manager.mu across that transaction.
	if err := consumePreCommit(ctx); err != nil {
		return unknown()
	}
	// A confirmed commit is not undone by a caller cancelling afterwards. All
	// fallible I/O is finished; only this in-memory ready transition remains.
	m.mu.Lock()
	g.recoveryUnknown, g.sealed = false, true
	m.mu.Unlock()
	return g, NativeRecoveryReceipt{ActionID: pending.ActionID, RuntimeIDs: pending.RuntimeIDs, OwnerAbsent: true, GroupsAbsent: [2]bool{true, true}, CheckedAt: time.Now().UTC()}, nil
}
