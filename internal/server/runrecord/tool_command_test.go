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
	commands := toolCommands(events)
	for _, tc := range []struct{ id, name, want string }{
		{"claude", "Bash", "git status --short"}, {"acp", "execute", "ls -la"}, {"codex", "shell", "go test ./..."}, {"pi", "bash", "make build"},
		{"secret", "Bash", "[Command hidden because it contains credentials]"},
		// Old records and tools without a recorded command stay as they were.
		{"missing", "Bash", ""}, {"read", "Read", ""},
	} {
		got := withToolCommand(apitypes.TimelineToolEntry{ToolCallID: tc.id, ToolName: tc.name}, commands).(apitypes.TimelineToolEntry)
		if tc.want == "" && got.Command != nil || tc.want != "" && (got.Command == nil || *got.Command != tc.want) {
			t.Fatal("wrong tool association", tc.id, got.Command)
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "PRIVATE") {
			t.Fatal("private input/output escaped")
		}
	}
	if other := withToolCommand(apitypes.TimelineQueuedEntry{}, commands); other != (apitypes.TimelineQueuedEntry{}) {
		t.Fatal("non-tool entry changed")
	}
}
