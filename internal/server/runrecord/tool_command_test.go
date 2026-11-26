package runrecord

import (
	"encoding/json"
	"strings"
	"testing"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/store"
)

func TestToolCommandScopedProjection(t *testing.T) {
	// Each runtime names its shell tool differently; the tool.command event always records it as the runtime sent it.
	events := []store.LifecycleEvent{
		{Type: "tool.command", Data: map[string]any{"toolCallId": "claude", "toolName": "bash", "command": "git status --short", "args": "PRIVATE", "output": "PRIVATE"}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "acp", "toolName": "execute", "command": "ls -la"}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "codex", "toolName": "shell", "command": "go test ./..."}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "pi", "toolName": "bash", "command": "make build"}},
		{Type: "tool.command", Data: map[string]any{"toolCallId": "secret", "toolName": "Bash", "command": "TOKEN=PRIVATE curl localhost"}},
	}
	commands := toolDetailsOf(events)
	for _, tc := range []struct{ id, name, want string }{
		{"claude", "Bash", "git status --short"}, {"acp", "execute", "ls -la"}, {"codex", "shell", "go test ./..."}, {"pi", "bash", "make build"},
		{"secret", "Bash", "[Command hidden because it contains credentials]"},
		// Old records and tools without a recorded command stay as they were.
		{"missing", "Bash", ""}, {"read", "Read", ""},
	} {
		got := withToolDetails(apitypes.TimelineToolEntry{ToolCallID: tc.id, ToolName: tc.name}, commands).(apitypes.TimelineToolEntry)
		if tc.want == "" && got.Command != nil || tc.want != "" && (got.Command == nil || *got.Command != tc.want) {
			t.Fatal("wrong tool association", tc.id, got.Command)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private input/output escaped")
		}
	}
	if other := withToolDetails(apitypes.TimelineQueuedEntry{}, commands); other != (apitypes.TimelineQueuedEntry{}) {
		t.Fatal("non-tool entry changed")
	}
}

// A recorded target goes to the entry with the same toolCallId, projected again on read; entries without one, and old
// records, stay as they were.
func TestToolTargetProjection(t *testing.T) {
	long := strings.Repeat("d/", 300) + "x.go"
	details := toolDetailsOf([]store.LifecycleEvent{
		{Type: "tool.target", Data: map[string]any{"toolCallId": "read", "toolName": "Read", "kind": "files", "target": "internal/main.go"}},
		{Type: "tool.target", Data: map[string]any{"toolCallId": "grep", "toolName": "Grep", "kind": "query", "target": "func main"}},
		{Type: "tool.target", Data: map[string]any{"toolCallId": "secret", "toolName": "Grep", "kind": "query", "target": "token=PRIVATE"}},
		{Type: "tool.target", Data: map[string]any{"toolCallId": "long", "toolName": "Write", "kind": "files", "target": long}},
		{Type: "tool.target", Data: map[string]any{"toolName": "Read", "kind": "files", "target": "no-id.go"}},
	})
	for _, tc := range []struct{ id, want string }{
		{"read", "internal/main.go"}, {"grep", "func main"}, {"secret", "[Hidden because it contains credentials]"}, {"missing", ""},
	} {
		got := withToolDetails(apitypes.TimelineToolEntry{ToolCallID: tc.id, ToolName: "Read"}, details).(apitypes.TimelineToolEntry)
		if tc.want == "" && got.Target != nil || tc.want != "" && (got.Target == nil || *got.Target != tc.want) || got.Command != nil {
			t.Fatal("wrong target", tc.id, got.Target)
		}
	}
	got := withToolDetails(apitypes.TimelineToolEntry{ToolCallID: "long"}, details).(apitypes.TimelineToolEntry)
	if got.Target == nil || len([]rune(*got.Target)) != 512 || !strings.HasSuffix(*got.Target, "/x.go") {
		t.Fatal("long target not bounded")
	}
}
