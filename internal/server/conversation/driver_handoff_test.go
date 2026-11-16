package conversation_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/conversation"
	"loop-now/internal/server/store"
)

// When a Handoff with an empty queue has released the lock and the old driver has not yet removed its drivers marker,
// the new run already holds the lock and the kick after exiting cannot take it over; the old driver must continue with
// this start directly.
func TestNewRunStartsWhenHandoffPrecedesDriverRemoval(t *testing.T) {
	for _, kind := range []string{"send", "compact"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			e.mustSend("block first", "")
			e.waitStarted("block first")

			reached := make(chan struct{})
			release := make(chan struct{})
			var armed atomic.Bool
			armed.Store(true)
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			conversation.SetTestHookAfterDriveHandoff(func(agentID uuid.UUID) {
				if agentID == e.agent && armed.CompareAndSwap(true, false) {
					close(reached)
					<-release
				}
			})
			t.Cleanup(func() {
				conversation.SetTestHookAfterDriveHandoff(nil)
				unblock()
			})
			if !e.ex.signal(e.agent, "release") {
				t.Fatal("failed to release the first run")
			}
			select {
			case <-reached:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the empty-queue Handoff")
			}

			text := "normal message after the handoff"
			origin := "console"
			type compactOutcome struct {
				result store.Run
				err    error
			}
			var compactDone chan compactOutcome
			var pending chan uuid.UUID
			if kind == "compact" {
				text = "keep the key decisions"
				origin = "console_compact"
				if _, err := e.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
					t.Fatal(err)
				}
				pending = make(chan uuid.UUID, 1)
				conversation.SetTestHookAfterDriverPending(func(runID uuid.UUID) { pending <- runID })
				t.Cleanup(func() { conversation.SetTestHookAfterDriverPending(nil) })
				compactDone = make(chan compactOutcome, 1)
				go func() {
					result, err := e.svc.Compact(context.Background(), e.agent, text)
					compactDone <- compactOutcome{result: result, err: err}
				}()
			} else if _, err := e.svc.Send(context.Background(), e.agent, text, ""); err != nil {
				t.Fatal(err)
			}

			var active *store.Run
			deadline := time.Now().Add(5 * time.Second)
			for active == nil && time.Now().Before(deadline) {
				run, err := e.st.ActiveRun(context.Background(), e.agent)
				if err != nil {
					t.Fatal(err)
				}
				if run != nil && run.Origin == origin && run.Status == "starting" {
					active = run
					break
				}
				select {
				case out := <-compactDone:
					t.Fatalf("compact returned before the new run started: %+v", out)
				default:
				}
				time.Sleep(10 * time.Millisecond)
			}
			if active == nil {
				t.Fatal("the new run did not reach starting")
			}
			if owner, err := e.co.LockOwner(context.Background(), e.agent.String()); err != nil || owner == "" {
				t.Fatalf("the new run should already hold the busy lock: %q %v", owner, err)
			}
			if pending != nil {
				select {
				case runID := <-pending:
					if runID != active.ID {
						t.Fatalf("pending run = %s, active run = %s", runID, active.ID)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for compact to register in pendingDrivers")
				}
			}
			select {
			case got := <-e.ex.started:
				t.Fatalf("the new run should not execute before the old driver exits: %q", got)
			default:
			}

			unblock()
			e.waitStarted(text)
			if compactDone != nil {
				select {
				case out := <-compactDone:
					c := store.ParseCompaction(out.result.Compaction)
					if out.err != nil || c.Outcome != store.CompactionCompacted || c.TokensBefore == nil || *c.TokensBefore != 100 || *c.EstimatedTokensAfter != 40 || c.Instructions != text {
						t.Fatalf("unexpected compact result: %+v %+v", out, c)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for compact to finish")
				}
			}
			e.waitIdle()
		})
	}
}

// The request that started the compaction disconnects before the driver reads the instructions (page refresh, proxy
// timeout): the instructions are recorded on the run, the compaction goes on as usual, and the result is read from the
// run afterwards.
func TestCompactSurvivesCancelledRequest(t *testing.T) {
	e := newEnv(t)
	e.mustSend("block first", "")
	e.waitStarted("block first")
	reached, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	armed.Store(true)
	conversation.SetTestHookAfterDriveHandoff(func(agentID uuid.UUID) {
		if agentID == e.agent && armed.CompareAndSwap(true, false) {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() { conversation.SetTestHookAfterDriveHandoff(nil) })
	if !e.ex.signal(e.agent, "release") {
		t.Fatal("failed to release the first run")
	}
	<-reached
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	pending := make(chan uuid.UUID, 1)
	conversation.SetTestHookAfterDriverPending(func(runID uuid.UUID) { pending <- runID })
	t.Cleanup(func() { conversation.SetTestHookAfterDriverPending(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Compact(ctx, e.agent, "keep only the decisions")
		done <- err
	}()
	var runID uuid.UUID
	select {
	case runID = <-pending: // the compaction run has been created, but the driver has not started it yet
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the compaction run to register")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Compact should return an error when the request disconnects")
	}
	close(release)
	e.waitStarted("keep only the decisions")
	e.waitIdle()
	last, err := e.svc.LastCompaction(context.Background(), e.agent)
	if err != nil || last == nil || last.ID != runID || last.Status != "completed" {
		t.Fatalf("the compaction run should be readable afterwards: %+v %v", last, err)
	}
	if c := store.ParseCompaction(last.Compaction); c.Instructions != "keep only the decisions" || c.Outcome != store.CompactionCompacted || c.TokensBefore == nil || *c.TokensBefore != 100 {
		t.Fatalf("compaction result: %+v", c)
	}
}
