package runrecord

import (
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

// Process events carry the runtime (old records have none, meaning pi); Claude Code rate limit events become
// model_rate_limit entries, with info kept as is.
func TestRuntimeTimelineEntries(t *testing.T) {
	at := time.Now()
	got, ok := eventEntry(store.LifecycleEvent{Type: store.EventPiStarted, At: at, Data: map[string]any{"pid": float64(7), "reason": "no_process", "runtime": "claude-code"}})
	if e, _ := got.(apitypes.TimelinePiStartedEntry); !ok || e.Runtime == nil || *e.Runtime != "claude-code" {
		t.Fatalf("pi_started: %+v", got)
	}
	got, _ = eventEntry(store.LifecycleEvent{Type: store.EventPiExited, At: at, Data: map[string]any{"pid": float64(7)}})
	if e, _ := got.(apitypes.TimelinePiExitedEntry); e.Runtime != nil {
		t.Fatalf("old records should have no runtime: %+v", e)
	}
	got, _ = eventEntry(store.LifecycleEvent{Type: store.EventPiStopped, At: at, Data: map[string]any{"runtime": "bogus"}})
	if e, _ := got.(apitypes.TimelinePiStoppedEntry); e.Runtime != nil {
		t.Fatalf("an unknown runtime should be left out: %+v", e)
	}
	info := map[string]any{"status": "allowed", "rateLimitType": "five_hour"}
	got, ok = eventEntry(store.LifecycleEvent{Type: store.EventModelRateLimit, At: at, Data: map[string]any{"info": info}})
	if e, _ := got.(apitypes.TimelineModelRateLimitEntry); !ok || e.Info["rateLimitType"] != "five_hour" {
		t.Fatalf("model_rate_limit: %+v %v", got, ok)
	}
	if _, ok = eventEntry(store.LifecycleEvent{Type: store.EventModelRateLimit, At: at, Data: map[string]any{}}); ok {
		t.Fatal("a rate limit event without info should stay out of the timeline")
	}
}
