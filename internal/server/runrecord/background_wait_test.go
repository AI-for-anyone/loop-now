package runrecord

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

// A turn that stopped waiting for its runtime's background commands says so in the timeline, with how long it heard
// nothing; in a task's record too.
func TestBackgroundWaitTimeoutEntry(t *testing.T) {
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	ev := store.LifecycleEvent{Type: store.EventBackgroundWaitTimeout, At: at, Data: map[string]any{"limitMs": float64(1800000)}}
	for _, mapped := range []func(store.LifecycleEvent) (any, bool){eventEntry, taskNodeEventEntry} {
		got, ok := mapped(ev)
		e, isEntry := got.(apitypes.TimelineBackgroundWaitTimeoutEntry)
		if !ok || !isEntry || e.Type != "background_wait_timeout" || !e.At.Equal(at) || e.LimitMs != 1800000 {
			t.Fatalf("%+v", got)
		}
	}
}

// A tool closed without a result keeps that state in a task's record, with the time it was closed.
func TestTaskNodeToolNoResult(t *testing.T) {
	at := time.Now().UTC()
	events := []store.LifecycleEvent{
		{ID: uuid.New(), At: at, Type: "tool.changed", Data: map[string]any{"toolCallId": "one", "toolName": "execute", "state": "started"}},
		{ID: uuid.New(), At: at.Add(time.Second), Type: "tool.changed", Data: map[string]any{"toolCallId": "one", "toolName": "execute", "state": "no_result"}},
	}
	projected := taskNodeEntries(events)
	if len(projected) != 1 {
		t.Fatalf("%+v", projected)
	}
	tool := projected[0].value.(apitypes.TimelineToolEntry)
	if tool.State != "no_result" || tool.EndedAt == nil || tool.DurationMs == nil || *tool.DurationMs != 1000 {
		t.Fatalf("%+v", tool)
	}
}
