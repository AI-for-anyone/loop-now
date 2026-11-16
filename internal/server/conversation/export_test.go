package conversation

import (
	"context"

	"github.com/google/uuid"
)

// SetTestHookBeforeInterrupt sets the test hook called before an interrupt; nil clears it.
func SetTestHookBeforeInterrupt(h func(runID uuid.UUID)) {
	if h == nil {
		testHookBeforeInterrupt.Store(nil)
		return
	}
	testHookBeforeInterrupt.Store(&h)
}

// SetTestHookBeforeMarkAborting sets the test hook called before marking a run aborting; nil clears it.
func SetTestHookBeforeMarkAborting(h func(byRunID uuid.UUID)) {
	if h == nil {
		testHookBeforeMarkAborting.Store(nil)
		return
	}
	testHookBeforeMarkAborting.Store(&h)
}

// SetTestHookAfterDriveHandoff sets the test hook called after a handoff with an empty queue; nil clears it.
func SetTestHookAfterDriveHandoff(h func(agentID uuid.UUID)) {
	if h == nil {
		testHookAfterDriveHandoff.Store(nil)
		return
	}
	testHookAfterDriveHandoff.Store(&h)
}

// SetTestHookAfterDriverPending sets the test hook called after a pendingDrivers registration; nil clears it.
func SetTestHookAfterDriverPending(h func(runID uuid.UUID)) {
	if h == nil {
		testHookAfterDriverPending.Store(nil)
		return
	}
	testHookAfterDriverPending.Store(&h)
}

// SetTestHookBeforeRestoreLock sets the test hook called before re-setting the busy lock; nil clears it.
func SetTestHookBeforeRestoreLock(h func(runID uuid.UUID)) {
	if h == nil {
		testHookBeforeRestoreLock.Store(nil)
		return
	}
	testHookBeforeRestoreLock.Store(&h)
}

// SetTestHookAfterRunEndPublished sets the test hook called after a run's end has been announced; nil clears it.
func SetTestHookAfterRunEndPublished(h func(runID uuid.UUID)) {
	if h == nil {
		testHookAfterRunEndPublished.Store(nil)
		return
	}
	testHookAfterRunEndPublished.Store(&h)
}

// ReconcileForTest runs one reconciliation right away.
func (s *Service) ReconcileForTest(ctx context.Context) { s.reconcile(ctx) }
