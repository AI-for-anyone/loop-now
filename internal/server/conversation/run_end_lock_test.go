package conversation_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/conversation"
)

// The console treats the Agent as idle as soon as it receives a run's final event (for a manual compaction, also when
// the /compact request returns). By then the busy lock must already be released: a message sent right after should
// start immediately, not be rejected with agent_busy (and at that point there is no current run to report either). This
// is how TestPlatformConsoleCompactCopyExportAndEndEventContract failed intermittently on CI: the driver emitted the
// final event first and released the lock afterwards.
func TestRunEndIsPublishedAfterLockRelease(t *testing.T) {
	for _, kind := range []string{"reply", "compact"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			reached, release := make(chan uuid.UUID, 1), make(chan struct{})
			var armed atomic.Bool
			armed.Store(true)
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			conversation.SetTestHookAfterRunEndPublished(func(runID uuid.UUID) {
				if armed.CompareAndSwap(true, false) {
					reached <- runID
					<-release
				}
			})
			t.Cleanup(func() {
				conversation.SetTestHookAfterRunEndPublished(nil)
				unblock()
			})

			var first uuid.UUID
			if kind == "reply" {
				first = e.mustSend("first message", "").Run.ID
				e.waitStarted("first message")
			} else {
				if _, err := e.st.Pool.Exec(ctx, `UPDATE agent_sessions SET state='active', session_file=$2 WHERE agent_id=$1`, e.agent, "/tmp/fake-session.jsonl"); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					run, err := e.svc.Compact(ctx, e.agent, "keep the key decisions")
					first = run.ID
					done <- err
				}()
				e.waitStarted("keep the key decisions")
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("compaction failed: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the /compact request should return once the compaction ends")
				}
			}
			select {
			case id := <-reached:
				if id != first {
					t.Fatalf("wrong run ended: %s, want %s", id, first)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the run to end")
			}
			if !hasTerminalEvent(t, e, first) {
				t.Fatal("the terminal event should already exist when the run end is announced")
			}

			msg, err := e.send("follow-up message", "")
			if err != nil {
				var details any
				if ae, ok := apperr.As(err); ok {
					details = ae.Details
				}
				t.Fatalf("a send after the terminal event should start right away: %v %+v", err, details)
			}
			if msg.Run.Status != "starting" || msg.Run.WhenBusy != nil {
				t.Fatalf("the follow-up message should start right away, not queue: %+v", msg.Run)
			}
			unblock()
			e.waitStarted("follow-up message")
			e.waitIdle()
			if h := e.history(); len(h) == 0 || h[len(h)-1].Text != "Reply: follow-up message" {
				t.Fatalf("the follow-up message should get a reply: %+v", h)
			}
		})
	}
}

// hasTerminalEvent reports whether the event stream has this run's final event: run.ended for a regular reply, a final
// run.updated for a manual compaction.
func hasTerminalEvent(t *testing.T, e *env, runID uuid.UUID) bool {
	t.Helper()
	for _, ev := range e.newestEvents() {
		var run apitypes.Run
		switch ev.Type {
		case apitypes.EventRunEnded:
			var ended apitypes.SseRunEnded
			if err := json.Unmarshal(ev.Data, &ended); err != nil {
				t.Fatal(err)
			}
			run = ended.Run
		case apitypes.EventRunUpdated:
			var updated apitypes.SseRunUpdated
			if err := json.Unmarshal(ev.Data, &updated); err != nil {
				t.Fatal(err)
			}
			run = updated.Run
		default:
			continue
		}
		if run.ID == runID {
			switch run.Status {
			case "completed", "failed", "interrupted":
				return true
			}
		}
	}
	return false
}
