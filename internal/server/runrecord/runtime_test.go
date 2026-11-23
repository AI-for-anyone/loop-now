package runrecord

import (
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

// Process events carry the runtime (old records have none and are read as pi); Claude Code rate limit events become
// model_rate_limit entries, with info kept as is.
func TestRuntimeTimelineEntries(t *testing.T) {
	at := time.Now()
	got, ok := eventEntry(store.LifecycleEvent{Type: store.EventPiStarted, At: at, Data: map[string]any{"pid": float64(7), "reason": "no_process", "runtime": "claude-code"}})
	if e, _ := got.(apitypes.TimelinePiStartedEntry); !ok || e.Runtime != "claude-code" {
		t.Fatalf("pi_started: %+v", got)
	}
	got, _ = eventEntry(store.LifecycleEvent{Type: store.EventPiExited, At: at, Data: map[string]any{"pid": float64(7)}})
	if e, _ := got.(apitypes.TimelinePiExitedEntry); e.Runtime != "pi" {
		t.Fatalf("old records should be read as pi: %+v", e)
	}
	got, _ = eventEntry(store.LifecycleEvent{Type: store.EventPiStopped, At: at, Data: map[string]any{"runtime": "bogus"}})
	if e, _ := got.(apitypes.TimelinePiStoppedEntry); e.Runtime != "pi" {
		t.Fatalf("an unknown runtime should be read as pi: %+v", e)
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

// Phase entries keep the runtime recorded with them. Old phase entries without one get it by the old-records rule: the
// first pi_started or pi_reused after the phase, else claude-code when the run ended with a runtime_* code, else pi.
func TestFillPhaseRuntimes(t *testing.T) {
	at := time.Now()
	phase := func(runtime string) any {
		return apitypes.TimelinePhaseEntry{TimelineBase: base(apitypes.TimelinePhase, at), Phase: "launching", Runtime: runtime}
	}
	code := func(c string) any {
		return apitypes.TimelineEndedEntry{TimelineBase: base(apitypes.TimelineEnded, at), Status: "failed", ErrorCode: &c}
	}
	started := apitypes.TimelinePiStartedEntry{TimelineBase: base(apitypes.TimelinePiStarted, at), Runtime: "claude-code"}
	reused := apitypes.TimelinePiReusedEntry{TimelineBase: base(apitypes.TimelinePiReused, at), Runtime: "pi"}
	stopped := apitypes.TimelinePiStoppedEntry{TimelineBase: base(apitypes.TimelinePiStopped, at), Runtime: "claude-code"}
	for name, c := range map[string]struct {
		entries []any
		want    string
	}{
		"recorded is kept":                    {[]any{phase("claude-code"), reused}, "claude-code"},
		"first process entry after it":        {[]any{phase(""), stopped, started, reused}, "claude-code"},
		"reused process":                      {[]any{phase(""), reused}, "pi"},
		"runtime_ failure without a process":  {[]any{phase(""), code("runtime_unavailable")}, "claude-code"},
		"other failure without a process":     {[]any{phase(""), code("cli_install_failed")}, "pi"},
		"process before the end wins":         {[]any{phase(""), started, code("pi_exited")}, "claude-code"},
		"nothing after it":                    {[]any{phase("")}, "pi"},
		"a process after the end is not used": {[]any{phase(""), code("internal_error"), started}, "pi"},
	} {
		fillPhaseRuntimes(len(c.entries), func(i int) any { return c.entries[i] }, func(i int, v any) { c.entries[i] = v })
		if got := c.entries[0].(apitypes.TimelinePhaseEntry).Runtime; got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
	got, _ := eventEntry(store.LifecycleEvent{Type: store.EventRunPhase, At: at, Data: map[string]any{"phase": "preparing", "runtime": "claude-code"}})
	if e, _ := got.(apitypes.TimelinePhaseEntry); e.Runtime != "claude-code" || e.Phase != "preparing" {
		t.Fatalf("a recorded phase runtime: %+v", got)
	}
}
