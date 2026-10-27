package runrecord

import (
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
	"strings"
	"testing"
	"time"
)

func TestTaskNodeSafeEventMapping(t *testing.T) {
	at := time.Now().UTC()
	ev := store.LifecycleEvent{At: at, Type: "tool.changed", Data: map[string]any{"toolCallId": "one", "toolName": "bash", "state": "failed", "arguments": "SECRET", "output": "SECRET"}}
	item, ok := taskNodeEventEntry(ev)
	if !ok {
		t.Fatal("dropped actual failed observation")
	}
	tool := item.(apitypes.TimelineToolEntry)
	if tool.State != "failed" || tool.DurationMs != nil || tool.EndedAt == nil {
		t.Fatal(tool)
	}
	raw, _ := json.Marshal(item)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatal("raw tool payload leaked")
	}
	ended, ok := taskNodeEventEntry(store.LifecycleEvent{At: at, Type: store.EventRunEnded, Data: map[string]any{"outcome": "interrupted", "errorCode": "server_restarted"}})
	if !ok || ended.(apitypes.TimelineEndedEntry).Status != "interrupted" {
		t.Fatal(ended)
	}
	if ended.(apitypes.TimelineEndedEntry).ErrorCode != nil {
		t.Fatal("task-only error escaped the shared TimelineEnded enum")
	}
	if _, ok = taskNodeEventEntry(store.LifecycleEvent{Type: "provider.secret", Data: map[string]any{"token": "SECRET"}}); ok {
		t.Fatal("unknown raw event projected")
	}
}

func TestTaskNodeEndedErrorCodeContract(t *testing.T) {
	for _, code := range []string{"credential_unavailable", "cli_install_failed", "workspace_sync_failed", "session_missing", "pi_start_failed", "pi_exited", "pi_error", "internal_error", "task_conflict", "server_restarted", "unknown_private_error", ""} {
		item, ok := taskNodeEventEntry(store.LifecycleEvent{Type: store.EventRunEnded, Data: map[string]any{"outcome": "failed", "errorCode": code}})
		if !ok {
			t.Fatal("ended event dropped")
		}
		got := item.(apitypes.TimelineEndedEntry).ErrorCode
		switch code {
		case "task_conflict", "server_restarted", "unknown_private_error", "":
			if got != nil {
				t.Fatalf("unsupported code %q leaked", code)
			}
		default:
			if got == nil || *got != code {
				t.Fatalf("supported code %q lost", code)
			}
		}
	}
}

func TestTaskNodeEndedTerminalStatuses(t *testing.T) {
	// These are the actual outcomes accepted by the task execution drivers
	// and persisted by taskEndedEvent, not task aggregate business statuses.
	for _, outcome := range []string{"completed", "failed", "interrupted"} {
		item, ok := taskNodeEventEntry(store.LifecycleEvent{Type: store.EventRunEnded, Data: map[string]any{"outcome": outcome}})
		if !ok || item.(apitypes.TimelineEndedEntry).Status != outcome {
			t.Fatalf("actual terminal outcome %q changed", outcome)
		}
	}
}

func TestTaskNodeToolsCoalesceAcrossRawEventPages(t *testing.T) {
	at := time.Now().UTC()
	events := []store.LifecycleEvent{{ID: uuid.New(), At: at, Type: "tool.changed", Data: map[string]any{"toolCallId": "one", "toolName": "bash", "state": "started"}}}
	for i := 1; i <= 230; i++ {
		events = append(events, store.LifecycleEvent{ID: uuid.New(), At: at.Add(time.Duration(i) * time.Millisecond), Type: store.EventRunPhase, Data: map[string]any{"phase": "launching"}})
	}
	events = append(events, store.LifecycleEvent{ID: uuid.New(), At: at.Add(time.Second), Type: "tool.changed", Data: map[string]any{"toolCallId": "one", "toolName": "bash", "state": "succeeded"}})
	projected := taskNodeEntries(events)
	tools := 0
	for _, v := range projected {
		if tool, ok := v.value.(apitypes.TimelineToolEntry); ok {
			tools++
			if tool.State != "succeeded" || !tool.At.Equal(at) || tool.DurationMs == nil || *tool.DurationMs != 1000 {
				t.Fatal("coalesced facts wrong", tool)
			}
		}
	}
	if tools != 1 || len(projected) != 231 {
		t.Fatal("one tool call became repeated rows", tools, len(projected))
	}
}
