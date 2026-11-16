package conversation_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/conversation"
)

// Reconciliation has read an in-flight run but not yet re-set the lock when that run ends and its driver releases the
// lock; the lock re-set afterwards belongs to a finished run and nobody will release it, so the Agent would show as
// busy forever (reconciliation only re-sets locks for in-flight runs and only hands off for Agents that have queued
// runs). This is how TestCancelRacesDequeueExactlyOnce hung intermittently on CI: the queue was empty but the lock
// stayed taken.
func TestReconcileDoesNotLeaveLockOfFinishedRun(t *testing.T) {
	e, _ := newMaintEnv(t) // reconciliation runs once an hour; only the manual one below happens
	e.mustSend("block current", "")
	e.waitStarted("block current")
	reached, release := make(chan uuid.UUID, 1), make(chan struct{})
	var once sync.Once
	conversation.SetTestHookBeforeRestoreLock(func(runID uuid.UUID) {
		once.Do(func() {
			reached <- runID
			<-release
		})
	})
	t.Cleanup(func() { conversation.SetTestHookBeforeRestoreLock(nil) })
	done := make(chan struct{})
	go func() {
		e.svc.ReconcileForTest(context.Background())
		close(done)
	}()
	select {
	case <-reached: // reconciliation has read the in-flight run and is about to re-set the lock
	case <-time.After(5 * time.Second):
		t.Fatal("reconciliation did not read the in-flight run")
	}
	e.ex.signal(e.agent, "release")
	deadline := time.Now().Add(5 * time.Second)
	for {
		owner, _ := e.co.LockOwner(context.Background(), e.agent.String())
		if owner == "" {
			break // the run has ended and its driver has released the lock
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lock should be released after the run ends: %q", owner)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	<-done
	e.waitIdle()
	e.mustSend("next", "")
	e.waitStarted("next")
	e.waitIdle()
}
